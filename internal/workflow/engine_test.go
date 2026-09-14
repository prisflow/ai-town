package workflow

import (
	"context"
	"math/rand"
	"strings"
	"testing"
)

// fakeHost 阻塞式 Host 的确定性假实现（所有方法立即返回）。
type fakeHost struct {
	logs   []string
	said   []string
	wood   float64
	workOn string
	choice int
}

func (f *fakeHost) Log(cat, text string) { f.logs = append(f.logs, cat+":"+text) }
func (f *fakeHost) Say(_ context.Context, actorID, text string, s float64) {
	f.said = append(f.said, text)
}
func (f *fakeHost) PathToEntity(_ context.Context, actorID, entity string) error { return nil }
func (f *fakeHost) PathToNearest(_ context.Context, actorID, kind string, maxRange int) (string, error) {
	return "res1", nil
}
func (f *fakeHost) PathToXY(_ context.Context, actorID string, x, y int) error { return nil }
func (f *fakeHost) CanWalk(x, y int) bool                                      { return true }
func (f *fakeHost) ActorPos(actorID string) (float64, float64)                 { return 5, 5 }
func (f *fakeHost) BeginWork(_ context.Context, actorID, kind, target string, units int) error {
	f.workOn = target
	return nil
}
func (f *fakeHost) DepositAll(_ context.Context, actorID string) (map[string]int, bool) {
	return map[string]int{"wood": 8}, true
}
func (f *fakeHost) Withdraw(_ context.Context, actorID, res string, n int) bool {
	return f.wood >= float64(n)
}
func (f *fakeHost) Eat(_ context.Context, actorID string) bool { return true }
func (f *fakeHost) StartConversation(_ context.Context, actorID, partnerID, topic string, rounds int) error {
	return nil
}
func (f *fakeHost) SleepUntilMorning(_ context.Context, actorID string) error { return nil }
func (f *fakeHost) Choose(_ context.Context, actorID, prompt string, options []string) (int, string, error) {
	return f.choice, "测试", nil
}
func (f *fakeHost) SayAI(_ context.Context, actorID, prompt string) (string, error) {
	return "(AI 台词)", nil
}
func (f *fakeHost) Check(actorID string, c CheckSpec) bool {
	var val float64
	if c.Key == "domain.wood" {
		val = f.wood
	}
	switch c.Op {
	case ">=":
		return val >= c.Value
	case "<":
		return val < c.Value
	}
	return false
}
func (f *fakeHost) Rand() *rand.Rand { return rand.New(rand.NewSource(1)) }

func TestTemplateChain(t *testing.T) {
	h := &fakeHost{wood: 3, choice: 1}
	e := NewEngine()
	e.Register(&Template{ID: "t1", Title: "T1", Steps: []StepSpec{
		{"type": "goto", "target": "keep"},
		{"type": "harvest", "target": "$tree"},
		{"type": "condition", "check": map[string]any{"key": "domain.wood", "op": ">=", "value": 5.0},
			"then": []StepSpec{{"type": "say", "text": "rich"}},
			"else": []StepSpec{{"type": "say", "text": "poor"}}},
		{"type": "ai_choice", "prompt": "选一个", "options": []any{
			map[string]any{"label": "a", "steps": []StepSpec{{"type": "wait", "seconds": 0.01}}},
			map[string]any{"label": "b", "steps": []StepSpec{{"type": "say", "text": "picked-b"}}},
		}},
		{"type": "say", "text": "done"},
	}})

	r, err := e.Start("a1", "t1", map[string]string{"tree": "res1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Execute(context.Background(), r, h); err != nil {
		t.Fatalf("Execute 应成功: %v", err)
	}
	if !r.Finished || !r.OK {
		t.Fatalf("应当成功完成: %v %v %s", r.Finished, r.OK, r.FailReason())
	}
	joined := strings.Join(h.said, "|")
	if !strings.Contains(joined, "poor") { // wood=3 < 5 走 else
		t.Fatalf("条件分支错误: %s", joined)
	}
	if !strings.Contains(joined, "picked-b") { // choice=1 选 b
		t.Fatalf("AI 选择分支错误: %s", joined)
	}
	if !strings.Contains(joined, "done") {
		t.Fatalf("未走到最后一步: %s", joined)
	}
	if h.workOn != "res1" {
		t.Fatalf("劳作目标错误: %s", h.workOn)
	}
}

func TestExecuteFail(t *testing.T) {
	h := &fakeHost{wood: 1}
	e := NewEngine()
	e.Register(&Template{ID: "t2", Title: "T2", Steps: []StepSpec{
		{"type": "withdraw", "resource": "wood", "amount": 10},
	}})
	r, _ := e.Start("a1", "t2", nil)
	if err := e.Execute(context.Background(), r, h); err == nil {
		t.Fatal("材料不足应当报错")
	}
	if !r.Finished || r.OK {
		t.Fatal("运行应标记为失败")
	}
	if !strings.Contains(r.FailReason(), "不足") {
		t.Fatalf("失败原因错误: %s", r.FailReason())
	}
}

func TestRolesAndRequires(t *testing.T) {
	tpl := &Template{ID: "t3", Title: "T3", Roles: []string{"woodcutter"}, Requires: map[string]int{"wood": 5}}
	if tpl.AllowsRole("farmer") {
		t.Fatal("farmer 不应允许")
	}
	if !tpl.AllowsRole("woodcutter") {
		t.Fatal("woodcutter 应允许")
	}
}

func TestLoadBuiltin(t *testing.T) {
	e := NewEngine()
	if err := e.LoadBuiltin(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"gather_wood", "gather_food", "gather_stone", "build_house", "build_granary", "build_farm", "farm_tend", "eat_meal", "idle_wander", "socialize", "sleep"} {
		if e.Template(id) == nil {
			t.Fatalf("缺少内置模板 %s", id)
		}
	}
}
