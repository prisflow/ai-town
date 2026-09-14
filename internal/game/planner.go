package game

import (
	"fmt"
	"strings"

	"aitown/internal/llm"
	"aitown/internal/xlog"
)

// requestPlan 领主指令 → LLM 规划器 → 工作单。锁内调用（异步回调经邮箱回锁）。
func (e *Engine) requestPlan(text string) {
	e.gw.CompleteAsync(&llm.Request{
		Role:     llm.RolePlanner,
		System:   plannerSystem,
		Messages: []llm.Message{{Role: "user", Content: e.worldSummary() + "\n\n领主指令：" + text}},
		JSONMode: true, Temperature: e.gw.Temperature(llm.RolePlanner, 0.3),
	}, func(resp *llm.Response, err error) {
		e.mail <- func() { e.applyPlan(text, resp, err) }
	})
}

const plannerSystem = `你是领地的总管。把领主的自然语言指令分解为村民可执行的工作单。
可用的任务类型（type 字段只能取以下值）：
- gather_wood：伐木获得木材
- gather_food：采集浆果获得食物
- gather_stone：开采岩石获得石料
- farm_tend：在农田劳作获得小麦（领地需已有农田）
- grind：在磨坊把小麦磨成面粉（需磨坊与小麦）
- bake_bread：烘焙面包（需磨坊；消耗面粉与浆果）
- build_house：建造一座民居（总管自动选址）
- build_granary：建造一座粮仓（总管自动选址）
- build_farm：开垦一片农田（总管自动选址）
- build_mill：建造一座磨坊（总管自动选址）
只输出 JSON：
{"understanding":"你对指令的理解（一句话）","jobs":[{"type":"gather_wood","count":1,"priority":3,"note":"简短说明"}]}
规则：count 为份数(1-4)；priority 1-5，5 最紧急；建造类工作先确保材料来源（材料不足时先安排采集）；不要发明表格之外的 type。`

// worldSummary 组装规划上下文。锁内调用。
func (e *Engine) worldSummary() string {
	w := e.world
	var sb strings.Builder
	fmt.Fprintf(&sb, "领地「%s」，第%d天 %.0f点。库存：木材%d 食物%d 石料%d。\n", w.Name, w.Day(), w.Hour(), w.Inventory["wood"], w.Inventory["food"], w.Inventory["stone"])
	roleCount := map[string]int{}
	for _, c := range e.cits {
		roleCount[c.Role]++
	}
	fmt.Fprintf(&sb, "居民：%d人（伐木%d 农夫%d 工匠%d 采集%d 普通%d）。\n", len(e.cits),
		roleCount["woodcutter"], roleCount["farmer"], roleCount["builder"], roleCount["forager"], roleCount["villager"])
	bc := map[string]int{}
	for _, id := range w.BldOrd {
		bc[string(w.Bld[id].Kind)]++
	}
	fmt.Fprintf(&sb, "建筑：领主堡%d 民居%d 粮仓%d 农田%d。\n", bc["keep"], bc["house"], bc["granary"], bc["farm"])
	pending := 0
	for _, j := range e.jobs {
		if j.Status == "pending" || j.Status == "claimed" {
			pending++
		}
	}
	if pending > 0 {
		fmt.Fprintf(&sb, "进行中的工作单：%d 项。\n", pending)
	} else {
		sb.WriteString("当前没有进行中的工作。\n")
	}
	return sb.String()
}

// planOut LLM 规划产物。
type planOut struct {
	Understanding string    `json:"understanding"`
	Jobs          []planJob `json:"jobs"`
}

type planJob struct {
	Type     string `json:"type"`
	Count    int    `json:"count"`
	Priority int    `json:"priority"`
	Note     string `json:"note"`
}

// applyPlan 落地规划结果；LLM 失败则回退关键词规则。锁内调用。
func (e *Engine) applyPlan(text string, resp *llm.Response, err error) {
	plan, perr := parsePlan(resp, err)
	mode := ""
	if perr != nil {
		plan = rulePlan(text)
		mode = "（离线规则模式）"
		// 诊断：回退原因此前完全静默（无法回答"为什么是离线规则模式"）
		respHead := ""
		if resp != nil {
			respHead = xlog.Trunc(resp.Text, 150)
		}
		xlog.Warn("总管规划失败，已回退关键词规则", "err", perr, "resp", respHead)
	}
	n := 0
	merged := map[string]int{}
	for _, j := range plan.Jobs {
		if merged[j.Type] >= 4 {
			continue
		}
		merged[j.Type]++
		count := j.Count
		if count < 1 {
			count = 1
		}
		if count > 4 {
			count = 4
		}
		for i := 0; i < count; i++ {
			if e.addJob(j.Type, nil, j.Priority, j.Note, "lord") != nil {
				n++
			}
		}
	}
	und := plan.Understanding
	if und == "" {
		und = "已安排人手。"
	}
	e.publishEventLocked("plan", fmt.Sprintf("总管：%s%s（分解为 %d 项工作）", und, mode, n))
}

func parsePlan(resp *llm.Response, err error) (planOut, error) {
	if err != nil {
		return planOut{}, err
	}
	out, perr := llm.ParseData[planOut](resp.Text)
	if perr != nil {
		return planOut{}, perr
	}
	if len(out.Jobs) == 0 {
		return planOut{}, fmt.Errorf("规划为空")
	}
	return out, nil
}

// rulePlan LLM 不可用时的关键词规划，保证领主指令永远有回应。
func rulePlan(text string) planOut {
	var jobs []planJob
	add := func(t string, count, pri int, note string) {
		jobs = append(jobs, planJob{Type: t, Count: count, Priority: pri, Note: note})
	}
	if strings.ContainsAny(text, "木柴林") {
		add("gather_wood", 2, 4, "储备木材")
	}
	if strings.ContainsAny(text, "食粮吃果") {
		add("gather_food", 2, 4, "储备食物")
	}
	if strings.ContainsAny(text, "石") {
		add("gather_stone", 1, 4, "开采石料")
	}
	if strings.Contains(text, "磨坊") {
		add("build_mill", 1, 5, "建造磨坊")
	}
	if strings.Contains(text, "面包") {
		add("bake_bread", 1, 4, "烤些面包")
	}
	if strings.ContainsAny(text, "田农") && strings.ContainsAny(text, "开垦种") {
		add("build_farm", 1, 5, "开垦新田")
	}
	if strings.Contains(text, "仓") {
		add("build_granary", 1, 5, "扩建仓储")
	}
	if strings.Contains(text, "房") {
		add("build_house", 1, 5, "添置居所")
	}
	if strings.ContainsAny(text, "田农耕作") {
		add("farm_tend", 1, 3, "田间劳作")
	}
	if len(jobs) == 0 {
		add("gather_food", 1, 3, "默认补充食物")
	}
	return planOut{Understanding: "按关键词安排了工作", Jobs: jobs}
}
