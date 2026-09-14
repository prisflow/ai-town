package game

import (
	"context"
	"strings"
	"time"

	"aitown/internal/sim"
)

// host_life.go —— "对话与睡觉"：一场对话的发起与等待、夜里睡到天亮。

// StartConversation 找一位村民开聊（workflow 的 talk 步骤）。阻塞到对话结束。
//
// 流程：引擎登记对话 → 对方"隔太远"就先走过去再试一次 → 标记双方对话态 →
// 等 EvChatDone（放宽到 90s）→ 记社交冷却、好感 +1。
func (h *agentHost) StartConversation(ctx context.Context, actorID, partnerID, topic string, rounds int) error {
	partner, err := h.e.startConversationBetween(actorID, partnerID, topic, rounds)
	if err != nil {
		// 官员对话池很小（往往只有两人）：隔太远时走到对方身边再试一次。
		// pickChatPartner 只选静止官员，走到位即贴身，不会发生追逐战。
		if partner != "" && strings.Contains(err.Error(), "隔太远") {
			if walkErr := h.PathToEntity(ctx, actorID, partner); walkErr == nil {
				err = h.withWorld(func() error {
					_, err := h.e.startConversationBetween(actorID, partner, topic, rounds)
					return err
				})
			}
		}
	}
	if err != nil {
		return err
	}
	// 标记对话态：对话期间发起者在别人的 prompt 里也显示"不可交谈"；
	// defer 保证正常结束/超时/打断三条路径都恢复
	h.a.talkingWith = partner
	h.a.setView(func(v *BrainView) { v.State = "talking" })
	defer func() {
		h.a.talkingWith = ""
		h.a.setView(func(v *BrainView) { v.State = "" })
	}()
	// 对话是多轮 LLM 轮转，等待放宽到 90s（30s 会误掐正常对话）
	ev, err := h.a.waitConvDone(ctx, chatWaitTimeout, func(e Event) bool { return e.Kind == EvChatDone })
	if err != nil {
		return err
	}
	h.a.chatCooldownUntil = time.Now().Add(time.Duration(h.a.e.Pacing().ChatCooldownSec) * time.Second) // 聊完消停一阵（设置页可调）
	h.a.cit.Rel[ev.From]++
	return nil
}

// SleepUntilMorning 睡到天亮：轮询世界时刻，天亮即返回。
// 若村民已走到自家房子旁，睡觉期间置 Hidden（前端隐藏精灵，视觉上"进屋睡觉"），醒来恢复。
func (h *agentHost) SleepUntilMorning(_ context.Context, actorID string) error {
	a := h.a
	a.sleeping = true
	a.setView(func(v *BrainView) { v.State = "sleeping" })
	hidden := false
	defer func() {
		a.e.mu.Lock()
		if a.e.world != nil && hidden {
			if act := a.e.world.Actors[actorID]; act != nil {
				act.Hidden = false
			}
		}
		a.e.mu.Unlock()
		a.sleeping = false
		a.setView(func(v *BrainView) { v.State = "" })
	}()
	for {
		a.e.mu.Lock()
		if a.e.world == nil {
			a.e.mu.Unlock()
			return nil
		}
		night := a.e.world.IsNight()
		if !night {
			if act := a.e.world.Actors[actorID]; act != nil {
				act.Busy = sim.BusyNone
			}
			a.e.mu.Unlock()
			return nil // 天亮了：起床
		}
		if act := a.e.world.Actors[actorID]; act != nil {
			act.Busy = sim.BusySleep
			// 在自家房子里睡：隐藏精灵（叠在同一格的多人也各自"进屋"）
			if !hidden {
				if home := a.e.world.Bld[a.cit.HomeID]; home != nil && a.e.world.AdjacentToBuilding(act, home) {
					act.Hidden = true
					hidden = true
				}
			}
		}
		a.e.mu.Unlock()
		select {
		case <-a.ctx.Done():
			return a.ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}
