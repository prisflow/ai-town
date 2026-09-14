package game

// 世界事件系统：表驱动定义 + 每游戏小时掷骰调度。
// 基调权重：好事 : 麻烦 ≈ 7:3；同类事件按 gapDays 冷却，不重复骚扰。
// 叙事用 narrate 角色 LLM 生成（失败回退固定文案）；效果为确定性结算。

import (
	"fmt"
	"math/rand"

	"aitown/internal/llm"
	"aitown/internal/sim"
)

// narrateEvent 用 LLM 生成事件旁白（失败回退固定文案）；结果经 mail 回世界线程广播。
func (e *Engine) narrateEvent(cat, fallback, prompt string) {
	e.gw.CompleteAsync(&llm.Request{
		Role:     llm.RoleNarrate,
		System:   "你是治愈风田园游戏的旁白。温暖、克制、有画面感。",
		Messages: []llm.Message{{Role: "user", Content: prompt + "\n只输出 JSON：{\"text\":\"旁白\"}"}},
		JSONMode: true, Temperature: e.gw.Temperature(llm.RoleNarrate, 0.8),
	}, func(resp *llm.Response, err error) {
		text := fallback
		if err == nil {
			type outT struct {
				Text string `json:"text"`
			}
			if out, perr := llm.ParseData[outT](resp.Text); perr == nil && out.Text != "" {
				text = out.Text
			}
		}
		// mail 闭包在世界线程持锁上下文内执行：必须用不加锁版本
		e.publishEventLocked(cat, text)
	})
}

// worldEventDef 一个世界事件的定义。
type worldEventDef struct {
	id      string
	title   string
	seasons map[sim.Season]int // 各季节权重（0/缺省 = 不出现）
	gapDays int                // 距上次同类事件的最小间隔天数
	cond    func(w *sim.World, e *Engine) bool
	apply   func(e *Engine)
}

// worldEventDefs 首批事件表（迭代 1）。
var worldEventDefs = []worldEventDef{
	{
		id: "merchant", title: "旅行商人来访",
		seasons: map[sim.Season]int{sim.SeasonSummer: 3, sim.SeasonAutumn: 3},
		gapDays: 10,
		cond: func(w *sim.World, e *Engine) bool {
			return w.Inventory["stone"] >= 6
		},
		apply: func(e *Engine) {
			// 调用方（世界线程/持锁测试）已持锁：此处不加锁
			e.world.Inventory["stone"] -= 6
			e.world.AddRes("flour", 4)
			e.publishEventLocked("world", "一位旅行商人用 4 袋面粉换走了 6 块石料，推着吱呀作响的推车远去。")
			if a := e.randomVillager(); a != nil {
				a.AdjustMood(4) // 见到稀罕货：心情小涨
				e.publish(&BubbleMsg{T: "bubble", Actor: a.ID, Text: "商人来啦！快看看有什么好东西。", Secs: 4})
			}
			e.narrateEvent("event", "旅商的推车吱呀作响，带来了远方的面粉。",
				"旅行商人来到宁静的领地，用面粉换了石料离开。写一句不超过20字的治愈风旁白。")
		},
	},
	{
		id: "rain_damage", title: "暴雨损屋",
		seasons: map[sim.Season]int{sim.SeasonSpring: 2, sim.SeasonSummer: 2, sim.SeasonAutumn: 2},
		gapDays: 5,
		cond: func(w *sim.World, e *Engine) bool {
			return e.hasRepairableBuilding() || w.Inventory["food"] >= 4
		},
		apply: func(e *Engine) { e.rainDamage() },
	},
	{
		id: "dog_steal", title: "野狗偷粮",
		seasons: map[sim.Season]int{sim.SeasonWinter: 3, sim.SeasonSpring: 1, sim.SeasonAutumn: 1},
		gapDays: 7,
		cond: func(w *sim.World, e *Engine) bool {
			return w.Inventory["food"] > 0
		},
		apply: func(e *Engine) {
			// 调用方已持锁
			lost := e.world.Inventory["food"]
			if lost > 6 {
				lost = 6
			}
			e.world.Inventory["food"] -= lost
			victim := e.randomVillager()
			e.publishEventLocked("world", fmt.Sprintf("夜里野狗溜进粮仓，叼走了 %d 份食物！", lost))
			if victim != nil {
				victim.AdjustMood(-8) // 被狗偷到头上：心情大挫
				e.deliver(victim.ID, Event{Kind: EvMemo, Outcome: "野狗偷走了库存的粮食，得防着点", Day: e.world.Day()})
				e.publish(&BubbleMsg{T: "bubble", Actor: victim.ID, Text: "有野狗！粮食被叼走了一些……", Secs: 5})
			}
		},
	},
}

// maybeWorldEvent 事件调度：每跨过一个游戏小时掷骰一次（约 10% 触发率）。
// 世界线程调用（持锁环境）。
func (e *Engine) maybeWorldEvent() {
	w := e.world
	key := w.Tick / sim.TicksPerHour
	if key == e.lastEventHour {
		return
	}
	e.lastEventHour = key
	season := w.Season()
	day := w.Day()

	// 测试钩子：强制触发指定事件
	if e.forceEventID != "" {
		id := e.forceEventID
		e.forceEventID = ""
		for i := range worldEventDefs {
			if worldEventDefs[i].id == id {
				e.eventLastDay[id] = day
				worldEventDefs[i].apply(e)
			}
		}
		return
	}

	chance := float64(e.Pacing().EventChance) / 100
	if chance <= 0 || rand.Float64() > chance {
		return // 0% = 事件系统关闭（设置页"节奏"）
	}
	type cand struct {
		d  *worldEventDef
		wt int
	}
	var cands []cand
	total := 0
	for i := range worldEventDefs {
		d := &worldEventDefs[i]
		wt := d.seasons[season]
		if wt <= 0 {
			continue
		}
		if last, ok := e.eventLastDay[d.id]; ok && day-last < d.gapDays {
			continue
		}
		if d.cond != nil && !d.cond(w, e) {
			continue
		}
		cands = append(cands, cand{d: d, wt: wt})
		total += wt
	}
	if total == 0 {
		return
	}
	r := rand.Intn(total)
	for _, c := range cands {
		if r < c.wt {
			e.eventLastDay[c.d.id] = day
			c.d.apply(e)
			return
		}
		r -= c.wt
	}
}

// ---------- 具体事件的实现 ----------

// rainDamage 暴雨：食物受潮 + 随机民居/粮仓受损（生成维修工作单）。
// rainDamage 暴雨：食物受潮 + 随机民居/粮仓受损（生成维修工作单）。由世界线程持锁调用。
func (e *Engine) rainDamage() {
	if e.world.Inventory["food"] > 0 {
		lost := e.world.Inventory["food"]
		if lost > 4 {
			lost = 4
		}
		e.world.Inventory["food"] -= lost
		// 天灾：全村心情受挫（最小闭环）
		for _, c := range e.cits {
			c.AdjustMood(-3)
		}
		e.publishEventLocked("world", fmt.Sprintf("一场暴雨浇透了领地，%d 份食物受了潮。", lost))
	}
	var targetID, kind string
	for _, id := range e.world.BldOrd {
		b := e.world.Bld[id]
		if b.Complete && (b.Kind == sim.BHouse || b.Kind == sim.BGranary) {
			targetID, kind = id, string(b.Kind)
			break
		}
	}
	if targetID == "" {
		return
	}
	b := e.world.Bld[targetID]
	b.Progress -= 30
	if b.Progress < 40 {
		b.Progress = 40
	}
	b.Complete = false
	e.world.RecalcInvCap()
	e.addJob("build_"+kind, map[string]string{"site": targetID}, 4, "暴雨维修", "")
	e.publish(&WorldMsg{T: "world", World: e.worldJSONLocked()})
}

// hasRepairableBuilding 是否存在可被暴雨损坏的民居/粮仓。
func (e *Engine) hasRepairableBuilding() bool {
	for _, id := range e.world.BldOrd {
		b := e.world.Bld[id]
		if b.Complete && (b.Kind == sim.BHouse || b.Kind == sim.BGranary) {
			return true
		}
	}
	return false
}

// randomVillager 随机挑一个村民（找不到返回 nil）。
func (e *Engine) randomVillager() *Citizen {
	if len(e.ord) == 0 {
		return nil
	}
	id := e.ord[e.world.RNG().Intn(len(e.ord))]
	return e.cits[id]
}
