package sim

import "testing"

func TestSeasonProgression(t *testing.T) {
	w := NewWorld(10, 10, 1)
	cases := []struct {
		tick int64
		want Season
	}{
		{0, SeasonSpring},
		{TicksPerDay * 7, SeasonSummer},
		{TicksPerDay * 14, SeasonAutumn},
		{TicksPerDay * 21, SeasonWinter},
		{TicksPerDay * 28, SeasonSpring}, // 次年春
	}
	for _, c := range cases {
		w.Tick = c.tick
		if w.Season() != c.want {
			t.Fatalf("tick=%d 季节错误: got %s want %v", c.tick, w.Season().Name(), c.want)
		}
	}
}

func TestWinterHoldsRegrow(t *testing.T) {
	w := NewWorld(10, 10, 1)
	w.AddResource(&Resource{ID: "r1", Kind: ResBerry, X: 1, Y: 1, Amount: 0, Max: 4, Depleted: true, RegrowAt: 10})

	w.Tick = TicksPerDay * 21 // 冬 1 日
	w.RegenResources()
	if !w.Res["r1"].Depleted {
		t.Fatal("冬季不应再生")
	}

	w.Tick = TicksPerDay * 35 // 次年春 8 日（早已超过 RegrowAt）
	w.RegenResources()
	if w.Res["r1"].Depleted {
		t.Fatal("开春后应恢复")
	}
}

func TestTendProducesWheatAndWinterFallow(t *testing.T) {
	w := NewWorld(10, 10, 1)
	w.AddBuilding(&Building{ID: "f1", Kind: BFarm, X: 3, Y: 3, W: 3, H: 2, Progress: 100, Complete: true})
	a := &Actor{ID: "a1", X: 4.5, Y: 5.5, Carrying: map[string]int{}}
	w.AddActor(a)

	w.BeginWork(a, WorkTend, "f1", 2)
	for i := 0; i < 100; i++ {
		w.Tick++
		if done, _ := w.StepActorWork(a); done {
			break
		}
	}
	if w.Inventory["wheat"] != 2 {
		t.Fatalf("2 单位应产 2 小麦（每单位 +1）: %d", w.Inventory["wheat"])
	}

	w.Tick = TicksPerDay * 21 // 冬 1 日
	w.BeginWork(a, WorkTend, "f1", 2)
	done, outcome := w.StepActorWork(a)
	if !done || outcome != "empty" {
		t.Fatalf("冬季农田应休耕: %v %s", done, outcome)
	}
}

func TestGrindAndBake(t *testing.T) {
	w := NewWorld(10, 10, 1)
	w.AddBuilding(&Building{ID: "m1", Kind: BMill, X: 3, Y: 3, W: 2, H: 2, Progress: 100, Complete: true})
	a := &Actor{ID: "a1", X: 4.5, Y: 5.5, Carrying: map[string]int{}}
	w.AddActor(a)

	w.Inventory["wheat"] = 6
	w.BeginWork(a, WorkGrind, "m1", 3)
	for i := 0; i < 200; i++ {
		w.Tick++
		if done, _ := w.StepActorWork(a); done {
			break
		}
	}
	if w.Inventory["flour"] != 3 || w.Inventory["wheat"] != 0 {
		t.Fatalf("磨面错误: %+v", w.Inventory)
	}

	w.Inventory["flour"] = 3
	w.Inventory["food"] = 6
	w.BeginWork(a, WorkBake, "m1", 3)
	for i := 0; i < 200; i++ {
		w.Tick++
		if done, _ := w.StepActorWork(a); done {
			break
		}
	}
	if w.Inventory["bread"] != 3 || w.Inventory["flour"] != 0 {
		t.Fatalf("烘焙错误: %+v", w.Inventory)
	}
}

func TestTendWorkTakesLonger(t *testing.T) {
	w := NewWorld(8, 8, 1)
	a := &Actor{ID: "a1", X: 1.5, Y: 1.5, Speed: 3, Carrying: map[string]int{}}
	w.AddActor(a)
	w.AddBuilding(&Building{ID: "f1", Kind: BFarm, X: 0, Y: 2, W: 2, H: 2, Progress: 100, Complete: true})

	w.BeginWork(a, WorkTend, "f1", -1)
	w.Tick = 2000 // 越过世界初始时刻（1050）
	w.StepActorWork(a)
	tend := a.Work.NextUnitTick - w.Tick
	if tend != 12 {
		t.Fatalf("农活间隔应 1.2 秒 = 12: %d", tend)
	}
}

func TestRecalcInvCap(t *testing.T) {
	w := NewWorld(10, 10, 1)
	w.AddBuilding(&Building{ID: "g1", Kind: BGranary, X: 2, Y: 2, W: 2, H: 2, Progress: 100, Complete: true})
	w.AddBuilding(&Building{ID: "g2", Kind: BGranary, X: 6, Y: 2, W: 2, H: 2, Progress: 0, Complete: false})
	w.RecalcInvCap()
	if w.ResCaps["wheat"] != 150+100 {
		t.Fatalf("仅 1 座建成粮仓时小麦上限: %d", w.ResCaps["wheat"])
	}
	if w.ResCaps["wood"] != 120 {
		t.Fatalf("粮仓不应影响木材上限: %d", w.ResCaps["wood"])
	}
	w.Bld["g2"].Complete = true
	w.RecalcInvCap()
	if w.ResCaps["wheat"] != 150+200 {
		t.Fatalf("两座粮仓时小麦上限: %d", w.ResCaps["wheat"])
	}
}

func TestDepositPerResourceCap(t *testing.T) {
	w := NewWorld(10, 10, 1)
	w.AddBuilding(&Building{ID: "k1", Kind: BKeep, X: 2, Y: 2, W: 2, H: 2, Progress: 100, Complete: true})
	a := &Actor{ID: "a1", X: 3.5, Y: 4.5, Speed: 3, Carrying: map[string]int{"food": 8, "wood": 4}}
	w.AddActor(a)
	w.Inventory["food"] = w.ResCaps["food"] // 浆果仓已满

	deposit, full, ok := w.DepositAll(a)
	if !ok {
		t.Fatal("在仓库旁应可交付")
	}
	if deposit["wood"] != 4 {
		t.Fatalf("木材应正常入库: %v", deposit)
	}
	if deposit["food"] != 0 {
		t.Fatalf("满仓的浆果不应入库: %v", deposit)
	}
	if len(full) != 1 || full[0] != "food" {
		t.Fatalf("应报告浆果满仓: %v", full)
	}
	if a.CarryTotal() != 0 {
		t.Fatalf("装不下的物资应清出背包（防死循环），剩余: %d", a.CarryTotal())
	}
}

func TestFarmTendStopsAtWheatCap(t *testing.T) {
	w := NewWorld(8, 8, 1)
	a := &Actor{ID: "a1", X: 1.5, Y: 1.5, Speed: 3, Carrying: map[string]int{}}
	w.AddActor(a)
	w.AddBuilding(&Building{ID: "f1", Kind: BFarm, X: 0, Y: 2, W: 2, H: 2, Progress: 100, Complete: true})
	w.Inventory["wheat"] = w.ResCaps["wheat"] // 小麦仓已满

	w.BeginWork(a, WorkTend, "f1", -1)
	w.Tick = 2000
	done, outcome := w.StepActorWork(a)
	if !done || outcome != "full" {
		t.Fatalf("小麦满仓时应立即收工，实际 done=%v outcome=%q", done, outcome)
	}
	if w.Inventory["wheat"] != w.ResCaps["wheat"] {
		t.Fatalf("不应超上限入库: %d", w.Inventory["wheat"])
	}
}
