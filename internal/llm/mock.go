package llm

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
)

// mockClient 内置演示模式：不联网、不花钱，按角色返回确定性的合理 JSON。
// 让整个游戏在没有 Key 时也能完整跑通，也用于单元/集成测试。
type mockClient struct {
	turn      atomic.Int64
	brainTurn atomic.Int64
}

func (m *mockClient) Complete(ctx context.Context, req *Request) (*Response, error) {
	text := m.respond(req)
	if req.JSONMode && !strings.HasPrefix(strings.TrimSpace(text), "{") {
		text = `{"text": "好的。"}`
	}
	// mock 不消耗真实 token，统计口径返回 0（不编造数字污染仪表）
	return &Response{Text: text, TokensIn: 0, TokensOut: 0}, nil
}

func (m *mockClient) respond(req *Request) string {
	switch req.Role {
	case RoleWorldgen:
		return mockWorldSpec
	case RolePlanner:
		return m.plan(req)
	case RoleDialogue:
		lines := []string{
			"今儿风大，砍柴要趁早。",
			"听说领主大人又要修新房子了？",
			"东边坡上的浆果熟了，又甜又软。",
			"昨晚湖那边好像有狼叫，早点关门。",
			"等发了工钱，想给家里添一口新锅。",
			"你那把斧头该磨磨了，钝得切菜似的。",
		}
		i := m.turn.Add(1)
		return lines[int(i)%len(lines)]
	case RoleChoice:
		return `{"index": 0, "reason": "mock"}`
	case RoleBrain:
		// 规则脑：轮换多种行为保证离线模式下村庄正常运转。
		// 官员才消费 brain 角色（人数少），socialize 占比调高让对话可见。
		intents := []string{"gather_wood", "gather_food", "socialize", "gather_stone", "idle_wander"}
		// orders 也轮换着发（多数轮次为空）：离线演示时村庄自己派活，不依赖真实 LLM
		orders := []string{
			"",
			`{"type":"gather_food","priority":3,"note":"(mock)补点吃的"}`,
			"",
			`{"type":"grind","priority":3,"note":"(mock)磨些面"}`,
			"",
			`{"type":"gather_wood","priority":3,"note":"(mock)备些柴"}`,
		}
		i := m.brainTurn.Add(1)
		return fmt.Sprintf(`{"intent":%q,"params":{},"reason":"(mock)轮换决策","orders":[%s]}`,
			intents[int(i)%len(intents)], orders[int(i)%len(orders)])
	case RoleNarrate:
		return `{"text": "一切井井有条。"}`
	default:
		return `{"text": "好的。"}`
	}
}

// plan 从规划提示词里粗略提取领主指令关键词，生成工作单。
// 只匹配「领主指令：」之后的文本，避免把世界概况里的"粮仓/农田"等词误当成指令。
func (m *mockClient) plan(req *Request) string {
	var sb strings.Builder
	for _, msg := range req.Messages {
		sb.WriteString(msg.Content)
	}
	text := sb.String()
	if i := strings.LastIndex(text, "领主指令："); i >= 0 {
		text = text[i+len("领主指令："):]
	}
	var jobs []string
	add := func(j string) { jobs = append(jobs, j) }
	if strings.ContainsAny(text, "木柴林") {
		add(`{"type":"gather_wood","count":2,"priority":4,"note":"(mock)储备木材"}`)
	}
	if strings.ContainsAny(text, "食物粮吃果") {
		add(`{"type":"gather_food","count":2,"priority":4,"note":"(mock)储备食物"}`)
		add(`{"type":"farm_tend","count":1,"priority":3,"note":"(mock)农田劳作"}`)
	}
	if strings.ContainsAny(text, "石") {
		add(`{"type":"gather_stone","count":1,"priority":4,"note":"(mock)开采石料"}`)
	}
	if strings.Contains(text, "房") {
		add(`{"type":"build_house","count":1,"priority":5,"note":"(mock)建造房屋"}`)
	}
	if strings.Contains(text, "仓") {
		add(`{"type":"build_granary","count":1,"priority":5,"note":"(mock)建造粮仓"}`)
	}
	if strings.Contains(text, "田") && strings.ContainsAny(text, "开垦建造修") {
		add(`{"type":"build_farm","count":1,"priority":5,"note":"(mock)开垦农田"}`)
	}
	if len(jobs) == 0 {
		add(`{"type":"gather_food","count":1,"priority":3,"note":"(mock)默认采集"}`)
	}
	return fmt.Sprintf(`{"understanding":"(mock 模式)按关键词分解指令","jobs":[%s]}`, strings.Join(jobs, ","))
}

// mockWorldSpec 演示世界的世界初始化产物（与 worldgen 的 Spec 结构对应）。
const mockWorldSpec = `{
  "name": "迷雾河谷",
  "lore": "群山之间一条终年起雾的河谷，领主在此扎下领地的第一面旗。土地肥沃，林中猎物与浆果丰饶，但老人们告诫：不要在雾最浓的夜里靠近深水。",
  "villagers": [
    {"name": "老周", "role": "woodcutter", "personality": "沉默寡言的伐木人，斧头从不离身，话少但句句实在"},
    {"name": "杏娘", "role": "farmer", "personality": "泼辣能干的农妇，嗓门大，算账比谁都快"},
    {"name": "石头", "role": "builder", "personality": "憨厚的年轻石匠，力气大，干活不惜力，有点贪吃"},
    {"name": "小满", "role": "forager", "personality": "机灵的采药少女，认得河谷里每一丛浆果，爱讲怪谈"},
    {"name": "陈九", "role": "villager", "personality": "见多识广的老货郎，哪里有路他都知道，爱打听消息"},
    {"name": "阿禾", "role": "farmer", "personality": "细声细气的少年农夫，跟在杏娘身后学种地，胆子小"}
  ]
}`
