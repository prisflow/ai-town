package game

import (
	"context"
	"errors"
	"time"

	"aitown/internal/workflow"
	"aitown/internal/xlog"
)

// execWorkflow 同步执行一次 workflow：阻塞当前村民 goroutine
// 直到完成/失败/被打断（新通知或对话邀请）。生命周期全量落日志（INFO，失败 WARN）。
//
// 收尾三件事（无论成败）：
//   - 释放资源软认领、记录工作履历（planner 上下文"你最近做过什么"）
//   - 有工作单则结算 jobFinished（done / interrupted 搁置 / failed 累计）
//   - 失败/打断各留一条记忆，让下一步的思考知道"刚才发生了什么"
func (a *agent) execWorkflow(run *workflow.Run, jobID, trace string) {
	a.run = run
	a.setView(func(v *BrainView) { v.Action = run.Title() })
	xlog.Info("workflow开始", "villager", a.cit.Name, "trace", trace,
		"tpl", run.ID(), "title", run.Title(), "job", jobID)
	wfCtx, wfCancel := context.WithCancel(xlog.WithTrace(a.ctx, trace)) // wfCtx 带 trace：workflow 内的等待/失败日志可关联到思考链路
	a.wfCancel = wfCancel
	err := a.e.wf.Execute(wfCtx, run, &agentHost{e: a.e, a: a})
	wfCancel()
	a.wfCancel = nil
	interrupted := a.wfInterrupted
	a.wfInterrupted = ""
	a.run = nil
	a.setView(func(v *BrainView) { v.Action = "" })
	// 干完活歇口气：冷却期内心跳不再触发思考（失败/打断的退避会在 think 里另行覆盖；
	// 冷却秒数来自设置页"节奏"）
	if a.nextThink.Before(time.Now()) {
		a.nextThink = time.Now().Add(time.Duration(a.e.Pacing().ThinkCooldownSec) * time.Second)
	}

	a.e.mu.Lock()
	day, hour := 0, 0.0
	if a.e.world != nil {
		day, hour = a.e.world.Day(), a.e.world.Hour()
		a.e.world.ReleaseClaims(a.cit.ID) // 释放资源软认领（完成/失败/打断都要放）
	}
	a.e.mu.Unlock()
	// 工作履历：无论成败都记录（planner 上下文"你最近做过什么"）
	outcome := "done"
	if err != nil {
		outcome = "failed"
	}
	a.cit.AddWorkLog(int(day), run.ID(), run.Title(), outcome)

	if jobID != "" {
		outcome := "failed"
		switch {
		case err == nil:
			outcome = "done"
		case interrupted != "" || errors.Is(err, context.Canceled):
			outcome = "interrupted" // 搁置回 pending，稍后可再领
		}
		a.e.jobFinished(jobID, outcome, run.FailReason())
	}
	if err == nil {
		a.failStreak = 0 // 真正干成一件活才清失败计数（意图失败与 LLM 失败共用）
		xlog.Info("workflow完成", "villager", a.cit.Name, "trace", trace, "tpl", run.ID())
		return
	}
	if errors.Is(err, context.Canceled) || interrupted != "" {
		if interrupted == "" {
			interrupted = "被叫走了"
		}
		xlog.Info("workflow被打断", "villager", a.cit.Name, "trace", trace, "tpl", run.ID(), "reason", interrupted)
		a.e.publishEvent("job", a.cit.Name+" 放下了手头的工作："+interrupted)
		a.rememberAt("event", "因为"+interrupted+"放下了"+run.Title(), 2, day, hour)
		return
	}
	xlog.Warn("workflow失败", "villager", a.cit.Name, "trace", trace, "tpl", run.ID(), "reason", run.FailReason())
	a.e.publishEvent("job", a.cit.Name+" 的"+run.Title()+"未能完成："+run.FailReason())
	a.rememberAt("event", run.Title()+"未能完成："+run.FailReason(), 2, day, hour)
}
