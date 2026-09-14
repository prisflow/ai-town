package game

// official.go 官员制度：领主任命/罢免官员、放逐村民。
// 官员拥有完整 LLM 大脑（决策/社交/提议/调度），平民纯机械劳作（零 token）。
// 人口因此可无限扩充而 token 只随官员数线性增长。

import (
	"fmt"
)

// officialsLocked 当前官员数。需持锁。
func (e *Engine) officialsLocked() int {
	n := 0
	for _, c := range e.cits {
		if c.Tier == TierOfficial {
			n++
		}
	}
	return n
}

// AppointOfficial 领主任命/罢免官员（server 调用，自行加锁）。
// cap = 官员人数上限（来自设置页 OfficialCap，<=0 视为不设限）；罢免无限制。
func (e *Engine) AppointOfficial(id string, appoint bool, cap int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.cits[id]
	if c == nil {
		return fmt.Errorf("居民不存在")
	}
	if appoint {
		if c.Tier == TierOfficial {
			return fmt.Errorf("%s 已经是官员了", c.Name)
		}
		if cap > 0 && e.officialsLocked() >= cap {
			return fmt.Errorf("官员人数已达上限（%d），请先罢免一位", cap)
		}
		c.Tier = TierOfficial
		e.publishEventLocked("edict", c.Name+" 被任命为官员，从此参与领地的谋划。")
	} else {
		if c.Tier == TierCommoner {
			return fmt.Errorf("%s 本就是平民", c.Name)
		}
		c.Tier = TierCommoner
		e.publishEventLocked("edict", c.Name+" 被罢免官职，回归寻常劳作。")
	}
	return nil
}

// Exile 领主放逐：村民收拾行李离开领地（server 调用，自行加锁）。
// 注意：停止执行流必须在锁外进行——cancelWorkflow 内部会拿 e.mu，
// Go 互斥锁不可重入，持锁调用会永久死锁并冻结整个引擎（曾被玩家触发证实）。
func (e *Engine) Exile(id string) error {
	e.mu.Lock()
	if e.world == nil {
		e.mu.Unlock()
		return fmt.Errorf("世界不存在")
	}
	if len(e.cits) <= 1 {
		e.mu.Unlock()
		return fmt.Errorf("领地不能没有居民")
	}
	if _, ok := e.cits[id]; !ok {
		e.mu.Unlock()
		return fmt.Errorf("居民不存在")
	}
	ag := e.agents[id]
	e.mu.Unlock()

	// 锁外停止执行流：打断在途 workflow 并取消上下文
	if ag != nil {
		ag.stop()
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	// 解锁窗口内可能已被并发移除/领地只剩一人：防御性复查
	c, ok := e.cits[id]
	if !ok || e.world == nil {
		return nil
	}
	if len(e.cits) <= 1 {
		return nil
	}
	// 正在对话：拆掉对话并通知仍在场的一方（避免对话引擎引用已放逐者崩溃）
	e.teardownConvosWith(id)
	e.world.RemoveActor(id)
	delete(e.cits, id)
	delete(e.agents, id)
	for i, v := range e.ord {
		if v == id {
			e.ord = append(e.ord[:i], e.ord[i+1:]...)
			break
		}
	}
	tier := "平民"
	if c.Tier == TierOfficial {
		tier = "官员"
	}
	e.publishEventLocked("edict", fmt.Sprintf("%s（%s）被领主放逐，收拾行李离开了领地。", c.Name, tier))
	e.publish(&WorldMsg{T: "world", World: e.worldJSONLocked()})
	e.world.ReleaseClaims(id)
	return nil
}

// teardownConvosWith 拆掉包含指定村民的全部对话并通知另一方。需持锁。
func (e *Engine) teardownConvosWith(id string) {
	for _, cv := range e.convos {
		if cv.aID != id && cv.bID != id {
			continue
		}
		other := cv.aID
		if cv.aID == id {
			other = cv.bID
		}
		if oc := e.cits[other]; oc != nil {
			e.deliver(other, Event{Kind: EvChatDone, From: id, Outcome: "对方离开了领地"})
		}
		delete(e.convos, cv.ID)
	}
}

// stop 停止村民执行流（放逐/关停用）：打断在途 workflow 并取消上下文。
func (a *agent) stop() {
	a.cancelWorkflow("被放逐了")
	a.cancel()
}
