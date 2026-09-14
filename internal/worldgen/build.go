package worldgen

import (
	"math"
	"math/rand"

	"aitown/internal/sim"
)

// DefaultSize 默认地图边长（格）。
const DefaultSize = 48

type spot struct{ x, y int }

// BuildWorld 按规格 + 种子确定性地搭建世界：
// 地形（湖/沙岸/道路）、资源簇（森林/浆果/岩石）、核心建筑群与村民实体。
// 同一 spec + seed 输出完全一致，可测试复现。
func BuildWorld(spec *Spec, seed int64, size int) *sim.World {
	if size <= 0 {
		size = DefaultSize
	}
	w := sim.NewWorld(size, size, seed)
	rng := w.RNG()
	cx, cy := size/2, size/2

	// ---- 地形：左下湖 + 沙岸 ----
	lakeX, lakeY := 8+rng.Intn(4), size-10-rng.Intn(4)
	lakeR := 4 + rng.Intn(2)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			d := math.Hypot(float64(x-lakeX), float64(y-lakeY))
			switch {
			case d < float64(lakeR):
				w.SetTile(x, y, sim.Water)
			case d < float64(lakeR)+1.4:
				w.SetTile(x, y, sim.Sand)
			}
		}
	}
	// 十字主路（遇水即断）
	for x := 1; x < size-1; x++ {
		if w.TileAt(x, cy) == sim.Grass {
			w.SetTile(x, cy, sim.Path)
		}
	}
	for y := 1; y < size-1; y++ {
		if w.TileAt(cx, y) == sim.Grass {
			w.SetTile(cx, y, sim.Path)
		}
	}

	// ---- 建筑 ----
	keep := &sim.Building{ID: w.GenID("b"), Kind: sim.BKeep, Name: "领主堡",
		X: cx - 1, Y: cy - 1, W: 3, H: 3, Progress: 100, Complete: true}
	w.AddBuilding(keep)

	bOcc := map[[2]int]bool{}
	markOcc := func(x, y, fw, fh int) {
		for dy := -1; dy <= fh; dy++ { // 连门口一行一起占位，避免贴脸盖楼
			for dx := -1; dx <= fw; dx++ {
				bOcc[[2]int{x + dx, y + dy}] = true
			}
		}
	}
	markOcc(keep.X, keep.Y, keep.W, keep.H)

	place := func(kind sim.BuildingKind, name string, s spot) *sim.Building {
		fw, fh := kind.Footprint()
		b := &sim.Building{ID: w.GenID("b"), Kind: kind, Name: name,
			X: s.x, Y: s.y, W: fw, H: fh, Progress: 100, Complete: true}
		w.AddBuilding(b)
		markOcc(b.X, b.Y, fw, fh)
		return b
	}

	houses := 0
	houseWant := 3 + rng.Intn(2)
	for _, s := range findSpots(rng, w, cx, cy, 4, 8, houseWant+3, 2, 2, bOcc) {
		if houses >= houseWant {
			break
		}
		place(sim.BHouse, "民居", s)
		houses++
	}
	if ss := findSpots(rng, w, cx, cy, 3, 7, 1, 2, 2, bOcc); len(ss) > 0 {
		place(sim.BGranary, "粮仓", ss[0])
	}
	farms := 0
	for _, s := range findSpots(rng, w, cx, cy, 4, 9, 4, 3, 2, bOcc) {
		if farms >= 2 {
			break
		}
		b := place(sim.BFarm, "农田", s)
		for y := b.Y; y < b.Y+b.H; y++ {
			for x := b.X; x < b.X+b.W; x++ {
				if w.TileAt(x, y) == sim.Grass {
					w.SetTile(x, y, sim.Farm)
				}
			}
		}
		farms++
	}

	// ---- 资源 ----
	forestCX, forestCY := size-9-rng.Intn(3), 8+rng.Intn(3)
	trees := 0
	for i := 0; i < 260 && trees < 46; i++ {
		x := forestCX + rng.Intn(13) - 6
		y := forestCY + rng.Intn(13) - 6
		if math.Hypot(float64(x-forestCX), float64(y-forestCY)) > 6.5 {
			continue
		}
		if canPlaceResource(w, x, y) {
			addRes(w, rng, sim.ResTree, x, y, 5+rng.Intn(3))
			trees++
		}
	}
	for i := 0; i < 40 && trees < 60; i++ {
		x, y := rng.Intn(size), rng.Intn(size)
		if canPlaceResource(w, x, y) && math.Hypot(float64(x-cx), float64(y-cy)) > 5 {
			addRes(w, rng, sim.ResTree, x, y, 4+rng.Intn(3))
			trees++
		}
	}
	berries := 0
	for i := 0; i < 80 && berries < 14; i++ {
		ang := rng.Float64() * 6.283
		r := 6 + rng.Intn(6)
		x := cx + int(math.Cos(ang)*float64(r))
		y := cy + int(math.Sin(ang)*float64(r))
		if canPlaceResource(w, x, y) {
			addRes(w, rng, sim.ResBerry, x, y, 4+rng.Intn(2))
			berries++
		}
	}
	rocks := 0
	for i := 0; i < 80 && rocks < 10; i++ {
		x := size - 8 - rng.Intn(6)
		y := size - 8 - rng.Intn(6)
		if canPlaceResource(w, x, y) {
			addRes(w, rng, sim.ResRock, x, y, 5+rng.Intn(3))
			rocks++
		}
	}

	// ---- 村民实体 ----
	w.Name = spec.Name
	w.Lore = spec.Lore
	for i := range spec.Villagers {
		spot, ok := w.NearestFreeAround(cx-2+i%4, cy+2+(i/4)%2)
		if !ok {
			spot, ok = w.NearestFreeAround(cx, cy+3)
			if !ok {
				spot = sim.Pt{X: cx, Y: cy}
			}
		}
		a := &sim.Actor{
			ID:       w.GenID("a"),
			X:        float64(spot.X) + 0.5,
			Y:        float64(spot.Y) + 0.5,
			Speed:    2.2 + rng.Float64()*0.4,
			Carrying: map[string]int{},
		}
		w.AddActor(a)
	}
	w.RecalcInvCap() // 初始粮仓计入库存上限
	return w
}

func addRes(w *sim.World, rng *rand.Rand, kind sim.ResourceKind, x, y, amount int) {
	w.AddResource(&sim.Resource{
		ID: w.GenID("r"), Kind: kind, X: x, Y: y,
		Amount: amount, Max: amount,
	})
}

// canPlaceResource 资源点合法性：可通行瓦片、非路非田、且不至于堵死（周围仍有 2 个通行格）。
func canPlaceResource(w *sim.World, x, y int) bool {
	if !w.InBounds(x, y) || !w.Passable(x, y) {
		return false
	}
	t := w.TileAt(x, y)
	if t == sim.Path || t == sim.Farm {
		return false
	}
	free := 0
	for _, d := range [4][2]int{{0, 1}, {0, -1}, {1, 0}, {-1, 0}} {
		if w.Passable(x+d[0], y+d[1]) {
			free++
		}
	}
	return free >= 2
}

// findSpots 环距 [minR,maxR] 内寻找 n 个 fw x fh 建筑点位（确定性：按环遍历 + 起始角随种子）。
func findSpots(rng *rand.Rand, w *sim.World, cx, cy, minR, maxR, n, fw, fh int, occ map[[2]int]bool) []spot {
	var out []spot
	startAng := rng.Float64() * 6.283
	for r := minR; r <= maxR && len(out) < n; r++ {
		steps := 8 + r*4
		for i := 0; i < steps && len(out) < n; i++ {
			ang := startAng + float64(i)/float64(steps)*6.283
			x := cx + int(math.Cos(ang)*float64(r))
			y := cy + int(math.Sin(ang)*float64(r)) - fh/2
			if !canPlaceBuilding(w, occ, x, y, fw, fh) {
				continue
			}
			out = append(out, spot{x, y})
			for dy := -1; dy <= fh; dy++ {
				for dx := -1; dx <= fw; dx++ {
					occ[[2]int{x + dx, y + dy}] = true
				}
			}
		}
	}
	return out
}

func canPlaceBuilding(w *sim.World, occ map[[2]int]bool, x, y, fw, fh int) bool {
	for dy := 0; dy < fh; dy++ {
		for dx := 0; dx < fw; dx++ {
			tx, ty := x+dx, y+dy
			if !w.InBounds(tx, ty) || !w.Passable(tx, ty) {
				return false
			}
			t := w.TileAt(tx, ty)
			if t == sim.Path || t == sim.Farm || t == sim.Sand {
				return false
			}
			if occ[[2]int{tx, ty}] {
				return false
			}
		}
	}
	// 门口必须可达（footprint 下缘中点下方一格）
	return w.Passable(x+fw/2, y+fh)
}
