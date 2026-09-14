package worldgen

import (
	"testing"

	"aitown/internal/sim"
)

func TestBuildWorldDeterministic(t *testing.T) {
	spec := DefaultSpec()
	w1 := BuildWorld(spec, 42, 0)
	w2 := BuildWorld(spec, 42, 0)
	if string(w1.Tiles) != string(w2.Tiles) {
		t.Fatal("同种子地图应完全一致")
	}
	if len(w1.ResOrd) != len(w2.ResOrd) {
		t.Fatal("同种子资源数量应一致")
	}
	for i, id := range w1.ResOrd {
		a, b := w1.Res[id], w2.Res[w2.ResOrd[i]]
		if a.X != b.X || a.Y != b.Y || a.Kind != b.Kind {
			t.Fatal("同种子资源位置应一致")
		}
	}
}

func TestBuildWorldSane(t *testing.T) {
	w := BuildWorld(DefaultSpec(), 7, 0)
	// 领主堡存在且居中
	keep := w.Keep()
	if keep == nil || !keep.Complete {
		t.Fatal("领主堡应已建成")
	}
	// 资源充足
	kinds := map[sim.ResourceKind]int{}
	for _, r := range w.Res {
		kinds[r.Kind]++
	}
	if kinds[sim.ResTree] < 15 || kinds[sim.ResBerry] < 6 || kinds[sim.ResRock] < 4 {
		t.Fatalf("资源过少: %v", kinds)
	}
	// 村民数量与规格一致且位置可通行
	if len(w.ActOrd) != len(DefaultSpec().Villagers) {
		t.Fatalf("居民数量 %d != %d", len(w.ActOrd), len(DefaultSpec().Villagers))
	}
	for _, id := range w.ActOrd {
		a := w.Actors[id]
		if !w.Passable(int(a.X), int(a.Y)) {
			t.Fatalf("居民 %s 出生在不可通行格 %v,%v", id, a.X, a.Y)
		}
	}
	// 从领主堡门口到森林必须可达（保证工作流不被地形卡死）
	doorX, doorY := keep.Door()
	// 找一棵树
	var tree *sim.Resource
	for _, r := range w.Res {
		if r.Kind == sim.ResTree {
			tree = r
			break
		}
	}
	if tree != nil {
		if _, ok := w.FindPath(doorX, doorY, tree.X, tree.Y); !ok {
			t.Fatal("领主堡到森林应可达")
		}
	}
}

func TestNormalize(t *testing.T) {
	s := Normalize(&Spec{
		Name: "", Lore: "",
		Villagers: []Villager{
			{Name: "张三", Role: "巫师", Personality: ""},
		},
	})
	if s.Name == "" || s.Lore == "" {
		t.Fatal("空字段应回退默认")
	}
	if len(s.Villagers) < 4 {
		t.Fatalf("村民应补足: %d", len(s.Villagers))
	}
	if s.Villagers[0].Role != "villager" {
		t.Fatal("非法职业应回退 villager")
	}
}
