package game

import (
	"strings"
	"testing"
)

func TestBuildPlannerPrompt(t *testing.T) {
	e, _ := newTestEngine(t)
	if err := e.CreateWorld("测试"); err != nil {
		t.Fatal(err)
	}
	pumpUntil(e, func() bool { return e.HasWorld() }, 100)
	a := e.agents[e.ord[0]]
	in := brainInput{
		Day: 3, Hour: 14, Season: "秋", Pop: 6,
		Domain: map[string]int{"wood": 5, "food": 9, "stone": 2},
		Mills:  1, Farms: 2, Houses: 3, Granaries: 2,
		InProgress:  []string{"伐木 · 小满"},
		Workers:     4,
		WorkersBusy: 1,
		Vocab:       brainIntents,
		WorkLog:     []WorkEntry{{Type: "gather_wood", Title: "伐木", Outcome: "done", Day: 2}},
		Memories:    []string{"上次野狗偷粮"},
	}
	p := buildPlannerPrompt(a, in)
	for _, want := range []string{
		a.cit.Name, "领地", "上次野狗偷粮", "可选行动", "gather_wood", "socialize",
		"粮仓2 民居3", "【进行中】", "伐木 · 小满", "【劳力】平民4人（干活中1 · 空闲3）",
	} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt 缺少 %q\n---\n%s", want, p)
		}
	}
}

func TestBuiltinTemplatesExist(t *testing.T) {
	e, _ := newTestEngine(t)
	if err := e.CreateWorld("测试"); err != nil {
		t.Fatal(err)
	}
	pumpUntil(e, func() bool { return e.HasWorld() }, 100)

	if e.wf.Template("build_house") == nil {
		t.Fatal("build_house 应有 workflow 模板")
	}
	if e.wf.Template("nonexistent") != nil {
		t.Fatal("未知模板应返回 nil")
	}
	if e.wf.Template("gather_wood") == nil {
		t.Fatal("gather_wood 应存在模板")
	}
}
