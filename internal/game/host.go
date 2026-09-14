package game

import (
	"context"
	"fmt"
	"math/rand"
	"strings"

	"aitown/internal/llm"
	"aitown/internal/workflow"
)

// agentHost workflow.Host 的实现，绑定单个村民 agent。
// 快操作（寻路起手/库存结算/校验）直接拿 e.mu 短临界区执行；
// 慢等待（走到/干完/聊完/LLM 返回）不持锁，通过信箱 select 完成——
// 因此 workflow 步骤可以写成自然的阻塞调用，只阻塞该村民自己。
//
// 【文件地图】（按阅读顺序）
//
//	host.go      基础设施与通用步骤（本文件）：锁包装 / 日志气泡 / 世界查询 / 条件求值 / LLM 步骤
//	host_path.go 走路：目标解析、寻路起程、到达等待
//	host_work.go 干活与生存：劳作、存料、取料、吃饭
//	host_life.go 对话与睡觉：找伴开聊、睡到天亮
type agentHost struct {
	e *Engine
	a *agent
}

// withWorld 在世界锁内执行一段快操作。
func (h *agentHost) withWorld(fn func() error) error {
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	return fn()
}

func (h *agentHost) Log(cat, text string) { h.e.publishEvent(cat, text) }

func (h *agentHost) Say(_ context.Context, actorID, text string, secs float64) {
	h.e.publish(&BubbleMsg{T: "bubble", Actor: actorID, Text: text, Secs: secs})
}

// CanWalk 坐标是否可通行（锁内读世界）。
func (h *agentHost) CanWalk(x, y int) bool {
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	return h.e.world.Passable(x, y)
}

// ActorPos 读居民当前位置（锁内读世界）。
func (h *agentHost) ActorPos(actorID string) (float64, float64) {
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	if a := h.e.world.Actors[actorID]; a != nil {
		return a.X, a.Y
	}
	return 0, 0
}

// Check 结构化条件求值（workflow 的 condition 步骤）。
func (h *agentHost) Check(actorID string, c workflow.CheckSpec) bool {
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	w := h.e.world
	if w == nil {
		return false
	}
	var val float64
	switch c.Key {
	case "domain.wood":
		val = float64(w.Inventory["wood"])
	case "domain.food":
		val = float64(w.Inventory["food"])
	case "domain.stone":
		val = float64(w.Inventory["stone"])
	case "carrying":
		if a := w.Actors[actorID]; a != nil {
			val = float64(a.CarryTotal())
		}
	case "hour":
		val = w.Hour()
	default:
		return false
	}
	switch c.Op {
	case ">=":
		return val >= c.Value
	case "<=":
		return val <= c.Value
	case ">":
		return val > c.Value
	case "<":
		return val < c.Value
	case "==":
		return val == c.Value
	default: // "true"
		return val != 0
	}
}

// Rand 世界级随机源（锁内取；同 seed 世界可复现）。
func (h *agentHost) Rand() *rand.Rand {
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	return h.e.world.RNG()
}

// Choose AI 决策节点：阻塞调用 LLM（只占当前村民）。
func (h *agentHost) Choose(ctx context.Context, actorID, prompt string, options []string) (int, string, error) {
	var sb strings.Builder
	sb.WriteString(prompt + "\n选项：\n")
	for i, o := range options {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i, o))
	}
	sb.WriteString("只输出 JSON：{\"index\": 选项序号, \"reason\": \"10字内理由\"}")
	resp, err := h.e.gw.Complete(ctx, &llm.Request{
		Role:     llm.RoleChoice,
		System:   "你是领地居民的内心决策。根据居民的性格做出选择。",
		Messages: []llm.Message{{Role: "user", Content: h.persona() + "\n" + sb.String()}},
		JSONMode: true, Temperature: h.e.gw.Temperature(llm.RoleChoice, 0.5),
	})
	if err != nil {
		return 0, "", err
	}
	type outT struct {
		Index  int    `json:"index"`
		Reason string `json:"reason"`
	}
	idx, reason := 0, ""
	if out, perr := llm.ParseData[outT](resp.Text); perr == nil {
		idx, reason = out.Index, out.Reason
	}
	if idx < 0 || idx >= len(options) {
		idx = 0
	}
	return idx, reason, nil
}

// SayAI AI 台词节点：阻塞调用 LLM。
func (h *agentHost) SayAI(ctx context.Context, actorID, prompt string) (string, error) {
	resp, err := h.e.gw.Complete(ctx, &llm.Request{
		Role:     llm.RoleNarrate,
		System:   "你是领地居民的台词生成器。符合人设，口语化，简短。",
		Messages: []llm.Message{{Role: "user", Content: h.persona() + "\n" + prompt + "\n只输出 JSON：{\"text\": \"台词\"}"}},
		JSONMode: true, Temperature: h.e.gw.Temperature(llm.RoleNarrate, 0.9),
	})
	if err != nil {
		return "", err
	}
	type outT struct {
		Text string `json:"text"`
	}
	if out, perr := llm.ParseData[outT](resp.Text); perr == nil && out.Text != "" {
		return out.Text, nil
	}
	return "", fmt.Errorf("空台词")
}

// persona 组装居民人设（供 LLM prompt）。
func (h *agentHost) persona() string {
	return fmt.Sprintf("你是 %s（%s）。性格：%s。", h.a.cit.Name, roleLabel(h.a.cit.Role), h.a.cit.Personality)
}

func roleLabel(r string) string {
	switch r {
	case "woodcutter":
		return "伐木工"
	case "farmer":
		return "农夫"
	case "builder":
		return "工匠"
	case "forager":
		return "采集者"
	}
	return "居民"
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
