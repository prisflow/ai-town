package sim

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWorldStateRoundTrip 世界存档往返：地形/实体/库存/时间/种子/认领表全量守恒，
// 且归一化只作用于存档副本（内存世界不被改动）。
func TestWorldStateRoundTrip(t *testing.T) {
	w := NewWorld(16, 16, 42)
	w.Name, w.Lore = "测试谷", "传说"
	w.Tick = 3*TicksPerDay + 5*TicksPerHour

	// 建筑 + 资源 + 村民（带运行态：路径/劳作/背包/隐藏）
	keep := &Building{ID: w.GenID("b"), Kind: BKeep, Name: "领主堡", X: 6, Y: 6, W: 3, H: 3, Progress: 100, Complete: true}
	w.AddBuilding(keep)
	tree := &Resource{ID: w.GenID("r"), Kind: ResTree, X: 2, Y: 2, Amount: 3, Max: 5}
	w.AddResource(tree)
	a := &Actor{
		ID: "a1", X: 3.5, Y: 3.5, Speed: 2.5, Facing: 2,
		Busy: BusyMove, Path: []Pt{{X: 4, Y: 4}}, Work: &WorkState{Kind: WorkHarvest, TargetID: tree.ID, Units: 1},
		Carrying: map[string]int{"wood": 2}, Hidden: true,
	}
	w.AddActor(a)
	w.ClaimRes(a.ID, tree.ID)
	w.Inventory["wood"] = 55

	nextIDBefore := w.nextID
	raw, err := w.MarshalState()
	if err != nil {
		t.Fatal(err)
	}

	// 归一化只作用于副本：内存世界仍是原样
	if a.Busy != BusyMove || len(a.Path) != 1 || a.Work == nil || !a.Hidden {
		t.Fatal("MarshalState 不应改动内存世界")
	}
	if !strings.Contains(string(raw), `"Name":"测试谷"`) {
		t.Fatalf("序列化结果异常: %.120s", raw)
	}

	got, err := UnmarshalState(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != w.Name || got.Lore != w.Lore || got.W != w.W || got.Tick != w.Tick {
		t.Fatalf("基础字段不一致: %+v", got)
	}
	if len(got.Tiles) != len(w.Tiles) || len(got.Res) != 1 || len(got.Bld) != 1 || len(got.Actors) != 1 {
		t.Fatalf("实体数量不一致: tiles=%d res=%d bld=%d act=%d", len(got.Tiles), len(got.Res), len(got.Bld), len(got.Actors))
	}
	if got.Inventory["wood"] != 55 || got.Seed != 42 || got.nextID != nextIDBefore {
		t.Fatalf("库存/种子/发号器不一致: inv=%d seed=%d next=%d", got.Inventory["wood"], got.Seed, got.nextID)
	}
	if got.claims[tree.ID] != "a1" {
		t.Fatalf("认领表应保留: %v", got.claims)
	}
	ga := got.Actors["a1"]
	if ga == nil || ga.Carrying["wood"] != 2 || ga.Busy != BusyNone || ga.Path != nil || ga.Work != nil || ga.Hidden {
		t.Fatalf("运行态应清空、背包应保留: %+v", ga)
	}
	// 发号器连续性：读档后新 ID 不与旧实体冲突
	if id := got.GenID("r"); id == tree.ID {
		t.Fatalf("发号器回退导致 ID 复用: %s", id)
	}
	if got.rng == nil {
		t.Fatal("随机源应重建")
	}
}

// TestUnmarshalStateBad 坏档防御：截断/空地图应报错而不是造出半个世界。
func TestUnmarshalStateBad(t *testing.T) {
	if _, err := UnmarshalState([]byte(`{"W":10,"H":10,"Tiles":[]}`)); err == nil {
		t.Fatal("地形缺失应报错")
	}
	if _, err := UnmarshalState([]byte(`not json`)); err == nil {
		t.Fatal("非 JSON 应报错")
	}
	var st WorldState
	if err := json.Unmarshal([]byte(`{"W":2,"H":2}`), &st); err != nil {
		t.Fatal(err)
	}
	if _, err := UnmarshalState([]byte(`{"W":-1,"H":2}`)); err == nil {
		t.Fatal("非法尺寸应报错")
	}
}
