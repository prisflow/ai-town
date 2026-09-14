package workflow

import (
	"context"
	"fmt"
	"strings"

	"aitown/internal/xlog"
)

// Run 一次模板执行的运行时状态。数据只属于启动它的 agent goroutine。
type Run struct {
	tpl     *Template
	ActorID string
	Vars    map[string]string
	Queue   []StepSpec

	failReason string
	Finished   bool
	OK         bool
}

// ID 模板 ID。
func (r *Run) ID() string { return r.tpl.ID }

// Title 模板标题（界面动作文本）。
func (r *Run) Title() string { return r.tpl.Title }

// FailReason 失败原因。
func (r *Run) FailReason() string { return r.failReason }

// Start 创建一次运行（不执行）。params 覆盖模板默认参数。
func (e *Engine) Start(actorID, tplID string, params map[string]string) (*Run, error) {
	tpl := e.templates[tplID]
	if tpl == nil {
		return nil, fmt.Errorf("模板 %q 不存在", tplID)
	}
	vars := map[string]string{}
	for k, v := range tpl.Params {
		vars[k] = v
	}
	for k, v := range params {
		vars[k] = v
	}
	return &Run{
		tpl:     tpl,
		ActorID: actorID,
		Vars:    vars,
		Queue:   append([]StepSpec{}, tpl.Steps...),
	}, nil
}

// Execute 顺序执行整个模板：每个步骤阻塞到完成。
// ctx 取消（世界重置/关停）会让当前步骤立即返回错误。
func (e *Engine) Execute(ctx context.Context, r *Run, h Host) error {
	for len(r.Queue) > 0 {
		if err := ctx.Err(); err != nil {
			r.fail("已取消: " + err.Error())
			return err
		}
		spec := r.Queue[0]
		r.Queue = r.Queue[1:]
		st, err := buildStep(spec)
		if err != nil {
			r.fail("步骤配置错误: " + err.Error())
			return err
		}
		// 步骤级 DEBUG 打点：实际执行的步骤序列（对照显示文本排查"言行不一"）
		xlog.Debug("workflow步骤", "tpl", r.tpl.ID, "actor", r.ActorID,
			"step", fmt.Sprint(spec["type"]), "remain", len(r.Queue))
		if err := st.Run(ctx, r, h); err != nil {
			r.fail(err.Error())
			return err
		}
	}
	r.Finished = true
	r.OK = true
	return nil
}

func (r *Run) fail(reason string) {
	r.Finished = true
	r.OK = false
	r.failReason = reason
}

// resolve 解析 $变量 引用。
func (r *Run) resolve(s string) string {
	if strings.HasPrefix(s, "$") {
		if v, ok := r.Vars[s[1:]]; ok {
			return v
		}
	}
	return s
}

// prependSteps 把子分支插到队首（condition / ai_choice 的控制流）。
func (r *Run) prependSteps(branch []StepSpec) {
	r.Queue = append(append([]StepSpec{}, branch...), r.Queue...)
}
