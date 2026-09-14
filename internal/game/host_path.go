package game

import (
	"context"
	"fmt"
	"math"
	"sort"

	"aitown/internal/sim"
)

// host_path.go —— "走到某处"的全部实现（workflow 的 goto / goto_nearest 步骤）。
//
// 统一模式：解析目标坐标（resolveEntity / 找最近的）→ 锁内起程 pathStart（不等待）
// → 锁外等待到达 waitArrived（双通道：EvArrived 快速唤醒 + 轮询 Busy 兜底）。
//
// 入口方法在前，辅助函数在后。

// PathToEntity 走到某个实体（建筑 / 资源 / 居民 / keep / home）旁边。阻塞到到达。
func (h *agentHost) PathToEntity(ctx context.Context, actorID, entityID string) error {
	var x, y int
	var err error
	if err = h.withWorld(func() error {
		x, y, err = h.resolveEntity(actorID, entityID)
		return err
	}); err != nil {
		return err
	}
	if err = h.withWorld(func() error { return h.pathStart(actorID, x, y) }); err != nil {
		return err
	}
	return h.waitArrived(ctx, actorID)
}

// PathToNearest 走向最近的目标类型（建筑或资源）并起程，返回目标 ID。阻塞到到达。
// 建筑：按到 actor 的距离挑最近的一座，逐个尝试侧边立足格；
// 资源：优先挑没人软认领的（缓解多人挤同一目标），至多试 6 个候选。
func (h *agentHost) PathToNearest(ctx context.Context, actorID, kind string, maxRange int) (string, error) {
	var (
		targetID string
		startErr error
	)
	err := h.withWorld(func() error {
		w := h.e.world
		a := w.Actors[actorID]
		if a == nil {
			return fmt.Errorf("实体不存在")
		}
		ax, ay := int(a.X), int(a.Y)
		// 建筑类目标：农田/磨坊/粮仓/民居——寻路到侧边立足格
		switch sim.BuildingKind(kind) {
		case sim.BFarm, sim.BMill, sim.BGranary, sim.BHouse:
			bk := sim.BuildingKind(kind)
			var best *sim.Building
			bd := 1 << 30
			for _, id := range w.BldOrd {
				b := w.Bld[id]
				if b.Kind != bk || !b.Complete {
					continue
				}
				d := abs(ax-b.X) + abs(ay-b.Y)
				if d < bd {
					best, bd = b, d
				}
			}
			if best == nil {
				return fmt.Errorf("领地还没有%s", buildingWord(bk))
			}
			targetID = best.ID
			tiles := buildingSideTiles(w, a, best)
			if len(tiles) == 0 {
				gx, gy := best.Door()
				startErr = h.pathStart(actorID, gx, gy)
				return startErr
			}
			// 逐个尝试候选立足格（最近优先，至多 6 个）：某侧被围也能从另一侧进入
			var lastErr error
			for i, t := range tiles {
				if i >= 6 {
					break
				}
				if err := h.pathStart(actorID, t.X, t.Y); err == nil {
					return nil
				} else {
					lastErr = err
				}
			}
			return lastErr
		}
		rk := sim.ResourceKind(kind)
		if rk.Yield() == "" {
			return fmt.Errorf("未知资源类型 %s", kind)
		}
		type cand struct {
			id      string
			x, y, d int
		}
		var free, taken []cand
		for _, id := range w.ResOrd {
			r := w.Res[id]
			if r.Kind != rk || r.Depleted {
				continue
			}
			d := abs(ax-r.X) + abs(ay-r.Y)
			if d > maxRange {
				continue
			}
			// 软认领：优先挑没人认领的资源，缓解多人挤同一目标
			if owner := w.ResClaimedBy(id); owner != "" && owner != actorID {
				taken = append(taken, cand{id, r.X, r.Y, d})
				continue
			}
			free = append(free, cand{id, r.X, r.Y, d})
		}
		cands := free
		if len(cands) == 0 {
			cands = taken // 全被认领：允许共用（软限制不硬卡）
		}
		if len(cands) == 0 {
			return fmt.Errorf("附近已没有%s了", rk.Label())
		}
		for i := 0; i < len(cands) && i < 6; i++ { // 近者优先，逐个尝试寻路
			for j := i + 1; j < len(cands); j++ {
				if cands[j].d < cands[i].d {
					cands[i], cands[j] = cands[j], cands[i]
				}
			}
			c := cands[i]
			if err := h.pathStart(actorID, c.x, c.y); err == nil {
				w.ClaimRes(actorID, c.id)
				targetID = c.id
				return nil
			}
		}
		return fmt.Errorf("%s都过不去", rk.Label())
	})
	if err != nil {
		return "", err
	}
	// 双通道等待：EvArrived 快速唤醒 + 轮询 Busy 状态兜底（到达=Busy 离开 Move，进展=坐标变化）
	if err = h.waitArrived(ctx, actorID); err != nil {
		return "", err
	}
	return targetID, nil
}

// PathToXY 走到指定坐标。阻塞到到达。
func (h *agentHost) PathToXY(ctx context.Context, actorID string, x, y int) error {
	if err := h.withWorld(func() error { return h.pathStart(actorID, x, y) }); err != nil {
		return err
	}
	return h.waitArrived(ctx, actorID)
}

// waitArrived 等待移动完成：done = Busy 离开 Move（世界线程到达时置 None）；
// 进展 = 坐标变化（被卡住 30s 判死自愈）。
func (h *agentHost) waitArrived(ctx context.Context, actorID string) error {
	lastX, lastY := math.NaN(), math.NaN()
	_, err := h.a.waitWorld(ctx, func(ev Event) bool { return ev.Kind == EvArrived },
		func(w *sim.World) (done, progressed bool) {
			act := w.Actors[actorID]
			if act == nil {
				return true, true
			}
			if act.Busy != sim.BusyMove {
				return true, true // 到达（或被物理清空——打断场景 ctx 已先行取消）
			}
			prog := act.X != lastX || act.Y != lastY
			lastX, lastY = act.X, act.Y
			return false, prog
		})
	return err
}

// resolveEntity 把 keep/home/实体ID 统一解析成 (x, y) 参照点。需持锁。
func (h *agentHost) resolveEntity(actorID, entity string) (int, int, error) {
	w := h.e.world
	switch entity {
	case "keep":
		keep := w.Keep()
		if keep == nil {
			return 0, 0, fmt.Errorf("领地没有仓库")
		}
		x, y := keep.Door()
		return x, y, nil
	case "home":
		c := h.e.cits[actorID]
		if c == nil || c.HomeID == "" {
			return 0, 0, fmt.Errorf("没有住处")
		}
		b := w.Bld[c.HomeID]
		if b == nil {
			return 0, 0, fmt.Errorf("家不见了")
		}
		x, y := b.Door()
		return x, y, nil
	default:
		if b := w.Bld[entity]; b != nil {
			// 建筑目标走侧边立足格（保证到达后邻接校验必过）
			if a := w.Actors[actorID]; a != nil {
				if ts := buildingSideTiles(w, a, b); len(ts) > 0 {
					return ts[0].X, ts[0].Y, nil
				}
			}
			x, y := b.Door()
			return x, y, nil
		}
		if r := w.Res[entity]; r != nil {
			return r.X, r.Y, nil
		}
		if a := w.Actors[entity]; a != nil {
			return int(a.X), int(a.Y), nil
		}
		return 0, 0, fmt.Errorf("未知目标 %s", entity)
	}
}

// pathStart 解析目标并立即起程（不等待到达）。需持锁。
func (h *agentHost) pathStart(actorID string, tx, ty int) error {
	w := h.e.world
	a := w.Actors[actorID]
	if a == nil {
		return fmt.Errorf("实体不存在")
	}
	if !w.InBounds(tx, ty) {
		return fmt.Errorf("目标在地图之外")
	}
	if !w.Passable(tx, ty) {
		t, ok := w.NearestFreeAround(tx, ty)
		if !ok {
			return fmt.Errorf("无法接近目标")
		}
		tx, ty = t.X, t.Y
	}
	path, ok := w.FindPath(int(a.X), int(a.Y), tx, ty)
	if !ok {
		return fmt.Errorf("无路可达")
	}
	w.BeginMove(a, path)
	return nil
}

// buildingSideTiles 建筑四周（不含四角）紧贴 footprint 的可通行立足格，
// 按离 actor 的曼哈顿距离升序返回。站在这类格子上必然满足 AdjacentToBuilding。
// 返回**全部候选**而非仅仅最近一个：最近格可能被别的建筑围住不可达，
// 调用方逐个尝试寻路（曾因只试最近一格，出现"无路可达"而放弃整座建筑）。
// 需持锁。
func buildingSideTiles(w *sim.World, a *sim.Actor, b *sim.Building) []sim.Pt {
	ax, ay := int(a.X), int(a.Y)
	var out []sim.Pt
	add := func(x, y int) {
		if x < 0 || y < 0 || x >= w.W || y >= w.H || !w.Passable(x, y) {
			return
		}
		out = append(out, sim.Pt{X: x, Y: y})
	}
	for x := b.X; x < b.X+b.W; x++ {
		add(x, b.Y-1)
		add(x, b.Y+b.H)
	}
	for y := b.Y; y < b.Y+b.H; y++ {
		add(b.X-1, y)
		add(b.X+b.W, y)
	}
	sort.Slice(out, func(i, j int) bool {
		di := abs(ax-out[i].X) + abs(ay-out[i].Y)
		dj := abs(ax-out[j].X) + abs(ay-out[j].Y)
		return di < dj
	})
	return out
}

// buildingWord 建筑类型中文名（错误文案用）。
func buildingWord(k sim.BuildingKind) string {
	switch k {
	case sim.BFarm:
		return "农田"
	case sim.BMill:
		return "磨坊"
	case sim.BGranary:
		return "粮仓"
	case sim.BHouse:
		return "民居"
	}
	return string(k)
}
