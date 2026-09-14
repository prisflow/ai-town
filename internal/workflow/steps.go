package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// 劳作类型常量（与 game/sim 侧的字符串一致；workflow 包保持无内部依赖）。
const (
	WorkHarvest = "harvest"
	WorkTend    = "tend"
	WorkBuild   = "build"
)

// step 单个步骤实例：阻塞执行到完成，失败返回 error。
type step interface {
	Run(ctx context.Context, r *Run, h Host) error
}

// buildStep 步骤工厂：按 type 反序列化配置并构造步骤实例。
func buildStep(spec StepSpec) (step, error) {
	typ, _ := spec["type"].(string)
	switch typ {
	case "goto":
		var s gotoStep
		return bind(&s, spec)
	case "goto_nearest":
		var s gotoNearestStep
		return bind(&s, spec)
	case "harvest":
		var s workStep
		if _, err := bind(&s, spec); err != nil {
			return nil, err
		}
		s.Kind = WorkHarvest
		return &s, nil
	case "tend":
		var s workStep
		if _, err := bind(&s, spec); err != nil {
			return nil, err
		}
		s.Kind = WorkTend
		if s.Units <= 0 {
			s.Units = 6
		}
		return &s, nil
	case "build":
		var s workStep
		if _, err := bind(&s, spec); err != nil {
			return nil, err
		}
		s.Kind = WorkBuild
		return &s, nil
	case "work": // 通用劳作：kind 显式给出（grind/bake 等磨坊工作）
		var s workStep
		return bind(&s, spec)
	case "deposit":
		var s depositStep
		return bind(&s, spec)
	case "withdraw":
		var s withdrawStep
		return bind(&s, spec)
	case "eat":
		return &eatStep{}, nil
	case "wait":
		var s waitStep
		return bind(&s, spec)
	case "say":
		var s sayStep
		return bind(&s, spec)
	case "wander":
		var s wanderStep
		return bind(&s, spec)
	case "talk":
		var s talkStep
		return bind(&s, spec)
	case "sleep":
		return &sleepStep{}, nil
	case "condition":
		var s condStep
		return bind(&s, spec)
	case "ai_choice":
		var s aiChoiceStep
		return bind(&s, spec)
	default:
		return nil, fmt.Errorf("未知步骤类型 %q", typ)
	}
}

// bind 把 map 转成强类型配置。
func bind(v any, spec StepSpec) (step, error) {
	b, err := json.Marshal(map[string]any(spec))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return nil, fmt.Errorf("%T: %w", v, err)
	}
	return v.(step), nil
}

// ---------- 移动 ----------

type gotoStep struct {
	Target string `json:"target"`
}

func (s *gotoStep) Run(ctx context.Context, r *Run, h Host) error {
	t := r.resolve(s.Target)
	if t == "" {
		return fmt.Errorf("goto 目标为空")
	}
	return h.PathToEntity(ctx, r.ActorID, t)
}

type gotoNearestStep struct {
	Kind     string `json:"kind"`
	Set      string `json:"set"`
	MaxRange int    `json:"max_range"`
}

func (s *gotoNearestStep) Run(ctx context.Context, r *Run, h Host) error {
	maxR := s.MaxRange
	if maxR <= 0 {
		maxR = 40
	}
	id, err := h.PathToNearest(ctx, r.ActorID, s.Kind, maxR)
	if err != nil {
		return err
	}
	if s.Set != "" {
		r.Vars[s.Set] = id
	}
	return nil
}

type wanderStep struct {
	Radius int `json:"radius"`
}

func (s *wanderStep) Run(ctx context.Context, r *Run, h Host) error {
	radius := s.Radius
	if radius <= 0 {
		radius = 5
	}
	rng := h.Rand()
	ax, ay := h.ActorPos(r.ActorID)
	for i := 0; i < 10; i++ {
		dx := rng.Intn(radius*2+1) - radius
		dy := rng.Intn(radius*2+1) - radius
		if dx == 0 && dy == 0 {
			continue
		}
		tx, ty := int(ax)+dx, int(ay)+dy
		if h.CanWalk(tx, ty) {
			if err := h.PathToXY(ctx, r.ActorID, tx, ty); err == nil {
				return nil
			}
		}
	}
	return nil // 随便走走失败不视为错误
}

// ---------- 劳作 ----------

type workStep struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
	Units  int    `json:"units"` // tend 的单位数
}

func (s *workStep) Run(ctx context.Context, r *Run, h Host) error {
	t := r.resolve(s.Target)
	if t == "" {
		return fmt.Errorf("劳作目标为空")
	}
	return h.BeginWork(ctx, r.ActorID, s.Kind, t, s.Units)
}

// ---------- 库存 ----------

type depositStep struct {
	All bool `json:"all"`
}

func (s *depositStep) Run(ctx context.Context, r *Run, h Host) error {
	deposit, ok := h.DepositAll(ctx, r.ActorID)
	if !ok {
		return fmt.Errorf("不在仓库旁，无法交付")
	}
	if len(deposit) > 0 {
		h.Log("job", fmt.Sprintf("交付物资 %v", deposit))
	}
	return nil
}

type withdrawStep struct {
	Resource string `json:"resource"`
	Amount   int    `json:"amount"`
}

func (s *withdrawStep) Run(ctx context.Context, r *Run, h Host) error {
	if !h.Withdraw(ctx, r.ActorID, s.Resource, s.Amount) {
		return fmt.Errorf("领地%s不足 %d", s.Resource, s.Amount)
	}
	return nil
}

type eatStep struct{}

func (s *eatStep) Run(ctx context.Context, r *Run, h Host) error {
	h.Eat(ctx, r.ActorID)
	return nil
}

// ---------- 等待/表达 ----------

type waitStep struct {
	Seconds float64 `json:"seconds"`
}

func (s *waitStep) Run(ctx context.Context, r *Run, h Host) error {
	if s.Seconds <= 0 {
		return nil
	}
	timer := time.NewTimer(time.Duration(s.Seconds * float64(time.Second)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type sayStep struct {
	Text   string  `json:"text"`
	AI     bool    `json:"ai"`
	Prompt string  `json:"prompt"`
	Secs   float64 `json:"secs"`
}

func (s *sayStep) Run(ctx context.Context, r *Run, h Host) error {
	text := r.resolve(s.Text)
	if s.AI {
		// LLM 生成失败/超时回退到占位台词，工作流不卡死
		if ai, err := h.SayAI(ctx, r.ActorID, r.resolve(s.Prompt)); err == nil && ai != "" {
			text = ai
		}
	}
	secs := s.Secs
	if secs <= 0 {
		secs = 4
	}
	h.Say(ctx, r.ActorID, text, secs)
	return nil
}

// ---------- 社交/休息 ----------

type talkStep struct {
	Partner string `json:"partner"` // 对方实体 ID；空 = 自动挑附近空闲居民
	Topic   string `json:"topic"`
	Rounds  int    `json:"rounds"`
}

func (s *talkStep) Run(ctx context.Context, r *Run, h Host) error {
	rounds := s.Rounds
	if rounds <= 0 {
		rounds = 3
	}
	return h.StartConversation(ctx, r.ActorID, r.resolve(s.Partner), r.resolve(s.Topic), rounds)
}

type sleepStep struct{}

func (s *sleepStep) Run(ctx context.Context, r *Run, h Host) error {
	return h.SleepUntilMorning(ctx, r.ActorID)
}

// ---------- 控制流 ----------

type condStep struct {
	Check CheckSpec  `json:"check"`
	Then  []StepSpec `json:"then"`
	Else  []StepSpec `json:"else"`
}

func (s *condStep) Run(ctx context.Context, r *Run, h Host) error {
	if h.Check(r.ActorID, s.Check) {
		r.prependSteps(s.Then)
	} else {
		r.prependSteps(s.Else)
	}
	return nil
}

type aiChoiceOption struct {
	Label string     `json:"label"`
	Steps []StepSpec `json:"steps"`
}

type aiChoiceStep struct {
	Prompt  string           `json:"prompt"`
	Options []aiChoiceOption `json:"options"`
}

func (s *aiChoiceStep) Run(ctx context.Context, r *Run, h Host) error {
	if len(s.Options) == 0 {
		return nil
	}
	labels := make([]string, len(s.Options))
	for i, o := range s.Options {
		labels[i] = o.Label
	}
	idx, _, err := h.Choose(ctx, r.ActorID, r.resolve(s.Prompt), labels)
	if err != nil || idx < 0 || idx >= len(s.Options) {
		idx = 0 // LLM 不可用/越界走兜底选项，工作流不卡死
	}
	r.prependSteps(s.Options[idx].Steps)
	return nil
}
