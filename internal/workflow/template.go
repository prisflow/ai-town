package workflow

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"math/rand"
	"strings"
)

// Host 是模板执行期间对游戏世界的全部能力入口，由 game 包（agent 侧）实现。
// 所有带 ctx 的方法都允许阻塞：agent goroutine 在步骤里等待是常态（走到/采完/聊完/LLM 返回），
// ctx 取消（世界重置/关停）会让它们立即返回错误。
type Host interface {
	Log(cat, text string)
	Say(ctx context.Context, actorID, text string, secs float64)
	// PathToEntity 走向实体：keep/home/已解析的实体 ID。阻塞到到达；失败返回错误。
	PathToEntity(ctx context.Context, actorID, entityID string) error
	// PathToNearest 走向最近的指定类型资源/建筑（tree/berry/rock/farm），返回其 ID。
	PathToNearest(ctx context.Context, actorID, kind string, maxRange int) (string, error)
	PathToXY(ctx context.Context, actorID string, x, y int) error
	CanWalk(x, y int) bool
	ActorPos(actorID string) (x, y float64)
	// BeginWork 开始劳作（harvest/tend/build），阻塞到完成（资源空/仓满/建成/劳作结束）。
	BeginWork(ctx context.Context, actorID, kind, targetID string, maxUnits int) error
	DepositAll(ctx context.Context, actorID string) (map[string]int, bool)
	Withdraw(ctx context.Context, actorID, res string, amount int) bool
	Eat(ctx context.Context, actorID string) bool
	// StartConversation 与指定居民开始对话（partnerID 为空 = 自动挑附近空闲者），
	// 阻塞到对话结束。
	StartConversation(ctx context.Context, actorID, partnerID, topic string, rounds int) error
	// SleepUntilMorning 睡到天亮（阻塞）。
	SleepUntilMorning(ctx context.Context, actorID string) error
	// Choose AI 决策节点：阻塞地在 options 中选择，返回选项下标与理由。
	Choose(ctx context.Context, actorID, prompt string, options []string) (int, string, error)
	// SayAI AI 台词节点：阻塞地生成一句台词。
	SayAI(ctx context.Context, actorID, prompt string) (string, error)
	// Check 条件判断（condition 步骤用）：查库存储备/背包/时刻等世界状态。
	Check(actorID string, c CheckSpec) bool
	// Rand 世界随机源：散步落点等需要确定性的"随机"行为使用（同 seed 可复现）。
	Rand() *rand.Rand
}

// CheckSpec 结构化条件：key 支持 domain.wood/food/stone、carrying、hour。
type CheckSpec struct {
	Key   string  `json:"key"`
	Op    string  `json:"op"`
	Value float64 `json:"value"`
}

// StepSpec 一个步骤 = {"type": "...", ...参数}。
type StepSpec map[string]any

// Template 行为模板：一次工作/一段日常的可复用封装。
type Template struct {
	ID       string            `json:"id"`
	Title    string            `json:"title"`
	Roles    []string          `json:"roles"`    // 允许执行的居民角色，空 = 不限
	Requires map[string]int    `json:"requires"` // 启动所需领地库存（如建造材料）
	Params   map[string]string `json:"params"`   // 参数默认值
	Steps    []StepSpec        `json:"steps"`
}

// AllowsRole 角色是否允许执行。
func (t *Template) AllowsRole(role string) bool {
	if len(t.Roles) == 0 {
		return true
	}
	for _, r := range t.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// Engine 模板注册表。模板只读，可被任意 agent goroutine 并发查询；单线程使用注册。
type Engine struct {
	templates map[string]*Template
}

func NewEngine() *Engine {
	return &Engine{templates: map[string]*Template{}}
}

func (e *Engine) Register(t *Template) { e.templates[t.ID] = t }

func (e *Engine) Template(id string) *Template { return e.templates[id] }

func (e *Engine) Templates() []*Template {
	out := make([]*Template, 0, len(e.templates))
	for _, t := range e.templates {
		out = append(out, t)
	}
	return out
}

//go:embed templates/*.json
var tplFS embed.FS

// LoadBuiltin 加载内嵌模板。
func (e *Engine) LoadBuiltin() error {
	entries, err := fs.ReadDir(tplFS, "templates")
	if err != nil {
		return err
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		b, err := fs.ReadFile(tplFS, "templates/"+ent.Name())
		if err != nil {
			return err
		}
		var t Template
		if err := json.Unmarshal(b, &t); err != nil {
			return fmt.Errorf("模板 %s: %w", ent.Name(), err)
		}
		e.Register(&t)
	}
	return nil
}
