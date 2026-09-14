package game

import (
	"context"
	"fmt"

	"aitown/internal/sim"
)

// host_work.go —— "干活与生存"：劳作（BeginWork）、存料（DepositAll）、取料（Withdraw）、吃饭（Eat）。
//
// BeginWork 是这里的主角：先锁内校验现实（邻接/季节/目标状态），再锁外等待完成；
// 建造类还带"夜间收工"——把工作单搁置回 pending，明早续建。

// BeginWork 校验邻接与目标状态后开始劳作，阻塞到劳作完成。
func (h *agentHost) BeginWork(ctx context.Context, actorID, kind, targetID string, maxUnits int) error {
	err := h.withWorld(func() error {
		w := h.e.world
		a := w.Actors[actorID]
		if a == nil {
			return fmt.Errorf("实体不存在")
		}
		switch kind {
		case sim.WorkHarvest:
			r := w.Res[targetID]
			if r == nil || r.Depleted {
				return fmt.Errorf("目标资源不可用")
			}
			if !w.AdjacentToTile(a, r.X, r.Y) {
				return fmt.Errorf("离得太远，无法采集")
			}
		case sim.WorkTend:
			if w.Season() == sim.SeasonWinter {
				return fmt.Errorf("冬季农田休耕")
			}
			b := w.Bld[targetID]
			if b == nil || !b.Complete || b.Kind != sim.BFarm {
				return fmt.Errorf("农田不可用")
			}
			if !w.AdjacentToBuilding(a, b) {
				return fmt.Errorf("离田太远")
			}
		case sim.WorkGrind, sim.WorkBake:
			b := w.Bld[targetID]
			if b == nil || !b.Complete || b.Kind != sim.BMill {
				return fmt.Errorf("磨坊不可用")
			}
			if !w.AdjacentToBuilding(a, b) {
				return fmt.Errorf("离磨坊太远")
			}
		case sim.WorkBuild:
			b := w.Bld[targetID]
			if b == nil || b.Complete {
				return fmt.Errorf("工地不可用")
			}
			if !w.AdjacentToBuilding(a, b) {
				return fmt.Errorf("离工地太远")
			}
		default:
			return fmt.Errorf("未知劳作类型 %s", kind)
		}
		w.BeginWork(a, kind, targetID, maxUnits)
		return nil
	})
	if err != nil {
		return err
	}
	// 双通道等待：EvWorkDone 事件快速唤醒 + 轮询 Work==nil 状态兜底（工期无关，
	// 进展=单位数增长；build 3 分钟工期每 7.2s 必有进展，30s 只在真停摆时判死）
	lastUnits := -1
	nightStop := false
	_, err = h.a.waitWorld(ctx, func(ev Event) bool { return ev.Kind == EvWorkDone },
		func(w *sim.World) (done, progressed bool) {
			act := w.Actors[actorID]
			if act == nil || act.Work == nil {
				return true, true // 劳作结束（世界线程完成时清 Work）
			}
			// 建造夜间收工：进度保存在建筑上，工作单搁置回 pending，明早重新认领续建。
			// 其余劳作工期很短（秒级），干完自然去睡，不受此限。
			if kind == sim.WorkBuild && w.IsNight() {
				nightStop = true
				return true, true
			}
			prog := act.Work.Units != lastUnits
			lastUnits = act.Work.Units
			return false, prog
		})
	if nightStop && err == nil {
		// 走到这里已在锁外：设打断原因并取消上下文 → workflow 按"被打断"处理 →
		// 工作单搁置（不算失败、不退料）→ 村民下次心跳经 nightRest 回家睡觉。
		h.a.cancelWorkflow("夜深了，收工回家")
		return fmt.Errorf("夜深了，收工回家")
	}
	return err
}

// DepositAll 背包全部存入仓库（按资源独立容量，溢出部分丢弃并在返回值里列出）。
func (h *agentHost) DepositAll(_ context.Context, actorID string) (map[string]int, bool) {
	var (
		deposit map[string]int
		full    []string
		ok      bool
	)
	h.e.mu.Lock()
	if a := h.e.world.Actors[actorID]; a != nil {
		deposit, full, ok = h.e.world.DepositAll(a)
	}
	h.e.mu.Unlock()
	if ok && len(full) > 0 {
		h.e.notifyStorageFull(h.a.cit.Name, full)
	}
	return deposit, ok
}

// Withdraw 从仓库取料（锁内短操作）。
func (h *agentHost) Withdraw(_ context.Context, actorID, res string, amount int) bool {
	h.e.mu.Lock()
	defer h.e.mu.Unlock()
	return h.e.world.Withdraw(res, amount)
}

// Eat 吃一顿：优先面包，否则浆果；没有生计则抱怨一句（纯生活循环）。
func (h *agentHost) Eat(_ context.Context, actorID string) bool {
	var meal string
	var ok bool
	h.e.mu.Lock()
	if h.e.world != nil {
		meal, ok = h.e.world.EatMeal() // 优先面包，否则浆果（吃饭消耗库存，纯生活循环）
	}
	h.e.mu.Unlock()
	if ok {
		food := "浆果"
		if meal == "bread" {
			food = "面包"
		}
		h.a.cit.AdjustMood(2) // 吃上饭：心情小涨
		h.Say(context.Background(), actorID, food+"真香，肚子舒坦了。", 3)
	} else {
		h.a.cit.AdjustMood(-1) // 饿肚子：心情小挫
		h.Say(context.Background(), actorID, "仓库里一粒粮食都不剩了…", 4)
	}
	return ok
}
