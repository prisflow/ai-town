package sim

import "testing"

func TestFindPathAroundWater(t *testing.T) {
	w := NewWorld(10, 10, 1)
	// 竖一道水墙，只在 y=4 留缺口
	for y := 0; y < 10; y++ {
		w.SetTile(5, y, Water)
	}
	w.SetTile(5, 4, Grass)

	path, ok := w.FindPath(1, 1, 8, 1)
	if !ok {
		t.Fatal("应当能绕过水墙找到路径")
	}
	if len(path) == 0 || path[len(path)-1] != (Pt{8, 1}) {
		t.Fatalf("路径终点错误: %v", path)
	}
	for _, p := range path {
		if w.TileAt(p.X, p.Y) == Water {
			t.Fatalf("路径穿过水域: %v", p)
		}
	}
}

func TestFindPathUnreachable(t *testing.T) {
	w := NewWorld(10, 10, 1)
	for y := 0; y < 10; y++ {
		w.SetTile(5, y, Water)
	}
	if _, ok := w.FindPath(1, 1, 8, 1); ok {
		t.Fatal("无缺口时应当不可达")
	}
}

func TestFindPathSameTile(t *testing.T) {
	w := NewWorld(5, 5, 1)
	path, ok := w.FindPath(2, 2, 2, 2)
	if !ok || len(path) != 0 {
		t.Fatalf("同格路径应为空且成功: %v %v", path, ok)
	}
}

func TestWorkAndDeposit(t *testing.T) {
	w := NewWorld(8, 8, 1)
	// 领主堡 3x3 居中
	keep := &Building{ID: "k1", Kind: BKeep, Name: "堡", X: 3, Y: 3, W: 3, H: 3, Progress: 100, Complete: true}
	w.AddBuilding(keep)
	a := &Actor{ID: "a1", X: 1.5, Y: 1.5, Speed: 3, Carrying: map[string]int{}}
	w.AddActor(a)
	w.AddResource(&Resource{ID: "r1", Kind: ResTree, X: 1, Y: 0, Amount: 3, Max: 3})

	// 采集 3 单位
	w.BeginWork(a, WorkHarvest, "r1", -1)
	for i := 0; i < 100; i++ {
		w.Tick += 20
		if done, _ := w.StepActorWork(a); done {
			break
		}
	}
	if got := a.Carrying["wood"]; got != 3 {
		t.Fatalf("应采到 3 木，实际 %d", got)
	}
	if !w.Res["r1"].Depleted {
		t.Fatal("资源应当枯竭")
	}
	// 到门口交付
	a.X, a.Y = 4.5, 6.5
	deposit, full, ok := w.DepositAll(a)
	if !ok || deposit["wood"] != 3 || w.Inventory["wood"] < 3 {
		t.Fatalf("交付失败: %v %v %v", ok, deposit, w.Inventory)
	}
	if len(full) != 0 {
		t.Fatalf("正常交付不应有满仓资源: %v", full)
	}
}

// TestHarvestNoCarryCap 采集不受“背包容量”限制：单点 12 件应全部携回（回归：容量限制已移除）。
func TestHarvestNoCarryCap(t *testing.T) {
	w := NewWorld(8, 8, 1)
	a := &Actor{ID: "a1", X: 1.5, Y: 1.5, Speed: 3, Carrying: map[string]int{}}
	w.AddActor(a)
	w.AddResource(&Resource{ID: "r1", Kind: ResTree, X: 1, Y: 0, Amount: 12, Max: 12})

	w.BeginWork(a, WorkHarvest, "r1", -1)
	outcome := ""
	for i := 0; i < 200; i++ {
		w.Tick += 20
		done, o := w.StepActorWork(a)
		if done {
			outcome = o
			break
		}
	}
	if got := a.Carrying["wood"]; got != 12 {
		t.Fatalf("应携回 12 木（无背包上限），实际 %d", got)
	}
	if outcome != "empty" {
		t.Fatalf("应在资源枯竭时以 empty 收工，实际 %q", outcome)
	}
}
