package game

import (
	"aitown/internal/sim"
)

// Memory 一条记忆（事件/对话/洞察），用于 prompt 注入与界面回看。
type Memory struct {
	Day        int     `json:"day"`
	Hour       float64 `json:"hour"`
	Text       string  `json:"text"`
	Kind       string  `json:"kind"` // event | chat
	Importance int     `json:"importance"`
}

// WorkEntry 一条工作履历（planner 上下文："你最近做过什么"）。
type WorkEntry struct {
	// Type 任务类型（workflow 模板 ID）。
	Type string
	// Title 显示标题。
	Title string
	// Outcome 结果：done | failed。
	Outcome string
	// Day 完成日的游戏天数。
	Day int
}

// 村民阶层：官员拥有完整 LLM 大脑（决策/社交/提议），平民纯机械劳作（零 token）。
const (
	TierCommoner = 0 // 平民：认领工作单/职业劳作，吃饭睡觉，不调 LLM
	TierOfficial = 1 // 官员：LLM 决策、对话、提议建造、（未来）结婚生子
)

// Citizen 村民的"大脑"侧数据：身份、记忆、关系、工作履历。
// 数据由该村民的 agent goroutine 独占读写（快照经 BrainView 原子发布），
// 物理存在（位置/移动/背包）在 sim.Actor，二者以 ID 关联。
type Citizen struct {
	ID          string
	Name        string
	Role        string // woodcutter/farmer/builder/forager/villager
	Tier        int    // TierCommoner 平民 / TierOfficial 官员
	Personality string
	Traits      []string // 特质标签（勤劳/懒散/健谈/胆小/爱吃…），LLM 生成
	HomeID      string
	Mood        int // 心情 -100~100，>0 高效 <0 低效；事件加减、缓慢回归 0
	Mem         []Memory
	Rel         map[string]int // 关系值（对话对象ID→好感）
	WorkLog     []WorkEntry
}

// RelLevel 根据好感分数推导关系层级。
// 曾经预留过"未婚双方好感≥20 → lover"的婚姻分支：读取方从未处理该层级，
// 是个误导性死分支，随婚姻系统一起移除（2026-09）。真做婚姻时重新设计，不复活半截实现。
func RelLevel(score int) string {
	switch {
	case score < 0:
		return "rival"
	case score >= 12:
		return "best_friend"
	case score >= 5:
		return "friend"
	default:
		return "acquaintance"
	}
}

// AdjustMood 调整心情并钳制到 [-100, 100]。
func (c *Citizen) AdjustMood(delta int) {
	c.Mood += delta
	if c.Mood > 100 {
		c.Mood = 100
	}
	if c.Mood < -100 {
		c.Mood = -100
	}
}

// moodLabel 心情的档位文案（进 prompt 给 LLM 感知，不暴露裸数字）。
func moodLabel(m int) string {
	switch {
	case m >= 60:
		return "极好"
	case m >= 20:
		return "不错"
	case m > -20:
		return "平淡"
	case m > -60:
		return "有点烦"
	default:
		return "很低落"
	}
}

// AdjustRel 调整对某人的好感并钳制到 [-100, 100]。
func (c *Citizen) AdjustRel(id string, delta int) {
	c.Rel[id] += delta
	if c.Rel[id] > 100 {
		c.Rel[id] = 100
	}
	if c.Rel[id] < -100 {
		c.Rel[id] = -100
	}
}

// AddWorkLog 追加工作履历（上限 20 条环形）。仅村民自己的 goroutine 调用。
func (c *Citizen) AddWorkLog(day int, typ, title, outcome string) {
	c.WorkLog = append(c.WorkLog, WorkEntry{Type: typ, Title: title, Outcome: outcome, Day: day})
	if len(c.WorkLog) > 20 {
		c.WorkLog = c.WorkLog[len(c.WorkLog)-20:]
	}
}

// RecentWork 取最近 n 条工作履历。
func (c *Citizen) RecentWork(n int) []WorkEntry {
	if n < 0 {
		n = 0
	}
	if n > len(c.WorkLog) {
		n = len(c.WorkLog)
	}
	out := make([]WorkEntry, n)
	copy(out, c.WorkLog[len(c.WorkLog)-n:])
	return out
}

// AddMemory 追加记忆（上限 48 条，越旧越先淘汰）。世界线程在持锁上下文调用。
func (c *Citizen) AddMemory(w *sim.World, kind, text string, importance int) {
	c.AddMemoryAt(w.Day(), w.Hour(), kind, text, importance)
}

// AddMemoryAt 追加一条带显式时间戳的记忆（时间来自事件盖章，村民无需读世界时钟）。
func (c *Citizen) AddMemoryAt(day int, hour float64, kind, text string, importance int) {
	c.Mem = append(c.Mem, Memory{
		Day: day, Hour: hour, Text: text, Kind: kind, Importance: importance,
	})
	if len(c.Mem) > 48 {
		c.Mem = c.Mem[len(c.Mem)-48:]
	}
}

// RecentMemories 取最近 n 条记忆的文本。
func (c *Citizen) RecentMemories(n int) []string {
	out := []string{}
	start := len(c.Mem) - n
	if start < 0 {
		start = 0
	}
	for _, m := range c.Mem[start:] {
		out = append(out, m.Text)
	}
	return out
}
