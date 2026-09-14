package game

import (
	"fmt"
	"math"
	"strings"

	"aitown/internal/sim"
	"aitown/internal/workflow"
)

// Job 工作单：领主指令经规划器分解后的最小可执行单元。
// Type 即 workflow 模板 ID，Params 注入模板变量（如建造工地 $site）。
type Job struct {
	// ID 工作单 ID（j1、j2…）。
	ID string
	// Type 任务类型（= workflow 模板 ID）。
	Type string
	// Title 显示标题（取自模板）。
	Title string
	// Params 注入模板的变量（如建造工地的 site）。
	Params map[string]string
	// Priority 优先级 1-5，5 最紧急。
	Priority int
	// Note 规划器附加的简短说明。
	Note string
	// IssuedBy 发布者："lord" = 领主/总管指令；居民 ID = 官员发布；"" = 领地系统事件。
	IssuedBy string
	// Status 生命周期：pending → claimed → done；失败/打断搁置回 pending，Fails≥3 才 failed。
	Status string
	// ClaimedBy 认领居民的 ID；空 = 无人认领。
	ClaimedBy string
	// Fails 累计失败次数（搁置重领的循环保护）。
	Fails int
}

// JobOrder 官员在一次思考里发布的一张工作单（brain 输出的 orders 数组元素）。
// 无硬约束：发多少、是否重复、优先级高低全由官员自己决定。
type JobOrder struct {
	Type     string `json:"type"`
	Priority int    `json:"priority"`
	Note     string `json:"note"`
}

// addJob 登记工作单；建造类自动选址落工地蓝图。需持有锁。
// issuedBy 记录发布者（"lord" / 居民 ID / ""=领地），供前端展示"谁发布的工作"。
func (e *Engine) addJob(jobType string, params map[string]string, pri int, note, issuedBy string) *Job {
	tpl := e.wf.Template(jobType)
	if tpl == nil {
		e.publishEventLocked("system", "未知的工作类型 "+jobType+"，已忽略")
		return nil
	}
	p := map[string]string{}
	for k, v := range tpl.Params {
		p[k] = v
	}
	for k, v := range params {
		p[k] = v
	}
	// 建造类：先圈地出蓝图
	switch jobType {
	case "build_house":
		if b := e.allocateSite(sim.BHouse); b != nil {
			p["site"] = b.ID
		} else {
			return nil
		}
	case "build_granary":
		if b := e.allocateSite(sim.BGranary); b != nil {
			p["site"] = b.ID
		} else {
			return nil
		}
	case "build_farm":
		if b := e.allocateSite(sim.BFarm); b != nil {
			p["site"] = b.ID
		} else {
			return nil
		}
	case "build_mill":
		if b := e.allocateSite(sim.BMill); b != nil {
			p["site"] = b.ID
		} else {
			return nil
		}
	}
	if pri < 1 || pri > 5 {
		pri = 3
	}
	j := &Job{
		ID:       fmt.Sprintf("j%d", e.jobSeq+1),
		Type:     jobType,
		Title:    tpl.Title,
		Params:   p,
		Priority: pri,
		Note:     note,
		IssuedBy: issuedBy,
		Status:   "pending",
	}
	e.jobSeq++
	e.jobs = append(e.jobs, j)
	suffix := ""
	if note != "" {
		suffix = "（" + note + "）"
	}
	e.publishEventLocked("job", "发布工作："+tpl.Title+suffix)
	return j
}

// applyOrders 落地官员在 brain 里发布的工作单（自行加锁）。每条一张、**无硬约束**——
// 不设数量上限、不去重、不拦重复：发多发少、要不要重复，全由官员自己掂量。
// 公告栏于是成了官员管理能力的"成绩单"：安排得是否得当，领主看得见、也评判得了。
// 返回成功上板的张数。未知类型会被 addJob 拒绝（结构安全，不算玩法限制）。
func (e *Engine) applyOrders(c *Citizen, orders []JobOrder) int {
	if len(orders) == 0 {
		return 0
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, o := range orders {
		t := strings.TrimSpace(o.Type)
		if t == "" {
			continue
		}
		if e.addJob(t, nil, o.Priority, o.Note, c.ID) != nil {
			n++
		}
	}
	return n
}

// requiresMet 模板的库存前置是否满足。
func (e *Engine) requiresMet(tpl *workflow.Template) bool {
	for k, v := range tpl.Requires {
		if e.world.Inventory[k] < v {
			return false
		}
	}
	return true
}

// tryClaim 村民 agent goroutine 的认领入口（自行加锁）。
func (e *Engine) tryClaim(c *Citizen) *Job {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.claimJob(c)
}

// payJobCosts 首次认领时一次性支付模板要求的材料（幂等：paid 标记）。
// 此后搁置/重领不再重复扣料，撤单时按 paid 退料。需持有锁。
func (e *Engine) payJobCosts(j *Job) {
	if j.Params["paid"] == "1" {
		return
	}
	if tpl := e.wf.Template(j.Type); tpl != nil {
		for k, v := range tpl.Requires {
			e.world.Inventory[k] -= v
		}
	}
	j.Params["paid"] = "1"
}

// jobClaimReason 工作单对某村民不可认领的原因（"" = 可认领）；dispose = 单子已失去
// 意义、调用方应直接核销（如工地已被建完）。两条认领路径（自动挑单 / LLM 指名单）
// 共用本判定，保证"提示给 LLM 的可见性"与"认领时的把关"永远一致。需持有锁。
func (e *Engine) jobClaimReason(c *Citizen, j *Job) (reason string, dispose bool) {
	if j.Status != "pending" {
		return "已被别人领走", false
	}
	tpl := e.wf.Template(j.Type)
	if tpl == nil {
		return "类型未知", false
	}
	if !tpl.AllowsRole(c.Role) {
		return "不是你的职业能干的活", false
	}
	// 产出满仓的单不认领（消耗后会自然恢复可领）——防止对着满仓仓库白干
	if e.outputFullLocked(j.Type) {
		return "产出仓已满，先不做了", false
	}
	// 建造工地必须仍是工地（没被别人建完）
	if sid := j.Params["site"]; sid != "" {
		if b := e.world.Bld[sid]; b != nil && b.Complete {
			return "工地已被建完", true
		}
	}
	// 材料门槛只对首次认领生效：料已在认领时一次性支付（paid），
	// 搁置重领不再重复扣料、也不再被缺料卡住
	if j.Params["paid"] != "1" && !e.requiresMet(tpl) {
		return "料没备齐，先干不了", false
	}
	return "", false
}

// claimJobLocked 认领指定工作单：标记 claimed + 首次付料 + 广播。需持有锁。
func (e *Engine) claimJobLocked(c *Citizen, j *Job) {
	// 首次认领：一次性支付材料（此后搁置/重领/撤单按 paid 处理）
	e.payJobCosts(j)
	j.Status = "claimed"
	j.ClaimedBy = c.ID
	e.publishEventLocked("job", fmt.Sprintf("%s 领取了工作「%s」", c.Name, j.Title))
}

// claimJob 空闲居民按优先级自动挑一张可领的工作单。需持有锁。
func (e *Engine) claimJob(c *Citizen) *Job {
	var best *Job
	for _, j := range e.jobs {
		reason, dispose := e.jobClaimReason(c, j)
		if dispose {
			j.Status = "done" // 已失去意义的单（如工地被建完）直接核销
			continue
		}
		if reason != "" {
			continue
		}
		if best == nil || j.Priority > best.Priority {
			best = j
		}
	}
	if best == nil {
		return nil
	}
	e.claimJobLocked(c, best)
	return best
}

// jobFinished 核销工作单（村民 agent goroutine 调用，自行加锁）。
// outcome: done(完成) | interrupted(被打断→搁置回 pending，稍后可再领) | failed(失败→搁置，
// 累计 3 次才真正 failed 并撤除工地蓝图——条件不齐的工程先等一等，而不是直接报废)。
func (e *Engine) jobFinished(id, outcome, reason string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, j := range e.jobs {
		if j.ID != id {
			continue
		}
		switch outcome {
		case "done":
			j.Status = "done"
			// 干成一件活：心情小涨
			if c := e.cits[j.ClaimedBy]; c != nil {
				c.AdjustMood(2)
			}
			e.publishEventLocked("job", fmt.Sprintf("「%s」完成", j.Title))
			// 发单回执：官员发单后常"看不见下文"，容易反复重发。只投完成、只投发布者
			// 本人（领取不投，控制通知噪声），让官员对自己发的单有没有被干有感知。
			if j.IssuedBy != "" && j.IssuedBy != "lord" {
				if issuer := e.cits[j.IssuedBy]; issuer != nil && issuer.Tier == TierOfficial {
					by := "完成了"
					if worker := e.cits[j.ClaimedBy]; worker != nil {
						by = "被" + worker.Name + "完成了"
					}
					e.deliver(j.IssuedBy, Event{Kind: EvReceipt, Outcome: fmt.Sprintf("你发布的「%s」%s", j.Title, by)})
				}
			}
		case "interrupted":
			j.Status = "pending"
			j.ClaimedBy = ""
			e.publishEventLocked("job", fmt.Sprintf("「%s」被搁置：%s（稍后再领）", j.Title, reason))
		default:
			j.Fails++
			if j.Fails >= 3 {
				j.Status = "failed"
				if c := e.cits[j.ClaimedBy]; c != nil {
					c.AdjustMood(-3) // 反复做不成：心情受挫
				}
				e.publishEventLocked("job", fmt.Sprintf("「%s」屡试不成，撤销（%s）", j.Title, reason))
				e.abandonSite(j)
			} else {
				j.Status = "pending"
				j.ClaimedBy = ""
				e.publishEventLocked("job", fmt.Sprintf("「%s」没成：%s（先搁一搁）", j.Title, reason))
			}
		}
		e.pruneJobsLocked()
		return
	}
}

// pruneJobsLocked 修剪工作单列表（长会话内存有界）：pending/claimed 全保留，
// 已了结的历史单只留最近 keepJobHistory 条（ID 用 jobSeq 单调计数，修剪不影响唯一性）。
// 需持有锁。
const keepJobHistory = 20

func (e *Engine) pruneJobsLocked() {
	finished := 0
	for _, j := range e.jobs {
		if j.Status == "done" || j.Status == "failed" {
			finished++
		}
	}
	if finished <= keepJobHistory {
		return
	}
	drop := finished - keepJobHistory
	kept := make([]*Job, 0, len(e.jobs)-drop)
	for _, j := range e.jobs {
		if drop > 0 && (j.Status == "done" || j.Status == "failed") {
			drop--
			continue
		}
		kept = append(kept, j)
	}
	e.jobs = kept
}

// abandonSite 撤除工作单对应的工地蓝图（未完工时），已支付的材料退回仓库。需持有锁。
func (e *Engine) abandonSite(j *Job) {
	// 退料：工地拆解，木石回仓（治愈风：不让人白忙）
	if j.Params["paid"] == "1" {
		if tpl := e.wf.Template(j.Type); tpl != nil {
			for k, v := range tpl.Requires {
				e.world.Inventory[k] += v
			}
			j.Params["paid"] = ""
			e.publishEventLocked("job", "工地拆解，材料已回仓")
		}
	}
	sid := j.Params["site"]
	if sid == "" {
		return
	}
	b := e.world.Bld[sid]
	if b == nil || b.Complete {
		return
	}
	e.world.RemoveBuilding(sid)
	e.publish(&WorldMsg{T: "world", World: e.worldJSONLocked()})
	e.publishEventLocked("job", b.Name+"的工地被平整回土地")
}

// allocateSite 为建造工程圈地：从领主堡向外螺旋寻找可容纳 footprint 的草地，
// 落下蓝图（Progress=0），同时全量推送一次世界（前端渲染工地）。
// 选址双重自检：不把村民圈在脚印里；不得破坏连通性（村民互相连通 + 城堡可达）。
func (e *Engine) allocateSite(kind sim.BuildingKind) *sim.Building {
	w := e.world
	keep := w.Keep()
	if keep == nil {
		return nil
	}
	cx, cy := keep.X+1, keep.Y+1
	fw, fh := kind.Footprint()
	before := w.ConnectivityOK()
	for r := 3; r <= 20; r++ {
		steps := 8 + r*4
		for i := 0; i < steps; i++ {
			ang := float64(i) / float64(steps) * 6.283
			x := cx + int(float64(r)*1.3*math.Cos(ang))
			y := cy + int(float64(r)*math.Sin(ang))
			if !e.canSite(x, y, fw, fh) {
				continue
			}
			b := &sim.Building{ID: w.GenID("b"), Kind: kind,
				Name: "工地·" + kind.Label(), X: x, Y: y, W: fw, H: fh}
			w.AddBuilding(b)
			if before && !w.ConnectivityOK() {
				w.RemoveBuilding(b.ID) // 会把村民/领主堡围死：换个位置
				continue
			}
			e.publish(&WorldMsg{T: "world", World: e.worldJSONLocked()})
			return b
		}
	}
	e.publishEventLocked("system", "领地周围找不到足够的空地，建造计划搁置")
	return nil
}

func (e *Engine) canSite(x, y, fw, fh int) bool {
	w := e.world
	// 不得把村民圈进/贴脸：脚印外扩 1 格内有村民则放弃（选址曾把村民封进农田）
	for _, aid := range w.ActOrd {
		act := w.Actors[aid]
		if act == nil {
			continue
		}
		ax, ay := int(act.X), int(act.Y)
		if ax >= x-1 && ax <= x+fw && ay >= y-1 && ay <= y+fh {
			return false
		}
	}
	for dy := 0; dy < fh; dy++ {
		for dx := 0; dx < fw; dx++ {
			tx, ty := x+dx, y+dy
			if !w.InBounds(tx, ty) || !w.Passable(tx, ty) {
				return false
			}
			t := w.TileAt(tx, ty)
			if t != sim.Grass && t != sim.Dirt {
				return false
			}
			if w.BuildingAt(tx, ty) != nil {
				return false
			}
			// 邻格不能被资源堵死门口
			free := 0
			for _, d := range [4][2]int{{0, 1}, {0, -1}, {1, 0}, {-1, 0}} {
				if w.Passable(tx+d[0], ty+d[1]) {
					free++
				}
			}
			if free < 2 {
				return false
			}
		}
	}
	return w.Passable(x+fw/2, y+fh)
}
