package sim

import (
	"encoding/json"
	"fmt"
	"math/rand"
)

// worldstate.go —— 世界存档序列化：把 sim.World 完整搬进 JSON 再完整搬回来。
// 设计取舍：
//   - rng 不可序列化：存种子（Seed），读档重建随机源；
//   - 运行态（移动路径/劳作状态/忙碌/隐藏/暂停）不入档——读档时所有人从
//     "站在原地的空闲状态"重新生活，避免半截工作流数据在存档里互相矛盾；
//   - 背包（Carrying）保留：人手里的东西属于世界事实，不是运行态。

// WorldState 世界存档载体（与 World 字段一一对应；未导出字段在此显式列出）。
type WorldState struct {
	Name                 string
	Lore                 string
	W, H                 int
	Tiles                []Tile
	Res                  map[string]*Resource
	ResOrd               []string
	Bld                  map[string]*Building
	BldOrd               []string
	BGrid                []string
	Actors               map[string]*Actor
	ActOrd               []string
	Inventory            map[string]int
	Tick                 int64
	NightStart, NightEnd float64
	ResCaps              map[string]int
	Seed                 int64
	NextID               int
	Claims               map[string]string
}

// MarshalState 序列化世界为存档字节。归一化只作用于导出的副本，不改动内存中的世界。
func (w *World) MarshalState() ([]byte, error) {
	if w == nil {
		return nil, fmt.Errorf("世界不存在")
	}
	acts := make(map[string]*Actor, len(w.Actors))
	for id, a := range w.Actors {
		if a == nil {
			continue
		}
		cp := *a
		cp.Path = nil
		cp.Work = nil
		cp.Busy = BusyNone
		cp.Hidden = false
		if cp.Carrying == nil {
			cp.Carrying = map[string]int{}
		}
		acts[id] = &cp
	}
	claims := make(map[string]string, len(w.claims))
	for k, v := range w.claims {
		claims[k] = v
	}
	st := WorldState{
		Name: w.Name, Lore: w.Lore,
		W: w.W, H: w.H, Tiles: w.Tiles,
		Res: w.Res, ResOrd: w.ResOrd,
		Bld: w.Bld, BldOrd: w.BldOrd, BGrid: w.BGrid,
		Actors: acts, ActOrd: w.ActOrd,
		Inventory:  w.Inventory,
		Tick:       w.Tick,
		NightStart: w.NightStart, NightEnd: w.NightEnd,
		ResCaps: w.ResCaps,
		Seed:    w.Seed, NextID: w.nextID, Claims: claims,
	}
	return json.Marshal(st)
}

// UnmarshalState 从存档重建世界：校验地形完整性、重建随机源与地图空间索引。
func UnmarshalState(b []byte) (*World, error) {
	var st WorldState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("世界状态解析失败: %w", err)
	}
	if st.W <= 0 || st.H <= 0 || len(st.Tiles) != st.W*st.H {
		return nil, fmt.Errorf("世界地图损坏（%dx%d, %d tiles）", st.W, st.H, len(st.Tiles))
	}
	w := &World{
		Name: st.Name, Lore: st.Lore,
		W: st.W, H: st.H, Tiles: st.Tiles,
		Res: st.Res, ResOrd: st.ResOrd,
		Bld: st.Bld, BldOrd: st.BldOrd, BGrid: st.BGrid,
		Actors: st.Actors, ActOrd: st.ActOrd,
		Inventory:  st.Inventory,
		Tick:       st.Tick,
		Paused:     false, // 暂停是会话态，不随存档
		NightStart: st.NightStart, NightEnd: st.NightEnd,
		ResCaps: st.ResCaps,
		Seed:    st.Seed,
		nextID:  st.NextID,
		rng:     rand.New(rand.NewSource(st.Seed)),
		claims:  st.Claims,
	}
	if w.Res == nil {
		w.Res = map[string]*Resource{}
	}
	if w.Bld == nil {
		w.Bld = map[string]*Building{}
	}
	if w.Actors == nil {
		w.Actors = map[string]*Actor{}
	}
	if w.Inventory == nil {
		w.Inventory = map[string]int{}
	}
	if w.claims == nil {
		w.claims = map[string]string{}
	}
	// 空间索引缺失/不完整时按建筑表重建（防御旧档/手工改档）
	if len(w.BGrid) != w.W*w.H {
		w.BGrid = make([]string, w.W*w.H)
		for _, id := range w.BldOrd {
			b := w.Bld[id]
			if b == nil {
				continue
			}
			for dy := 0; dy < b.H; dy++ {
				for dx := 0; dx < b.W; dx++ {
					x, y := b.X+dx, b.Y+dy
					if w.InBounds(x, y) {
						w.BGrid[y*w.W+x] = id
					}
				}
			}
		}
	}
	w.RecalcInvCap()
	return w, nil
}
