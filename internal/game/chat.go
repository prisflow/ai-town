package game

import (
	"fmt"
	"math"
	"strings"

	"aitown/internal/llm"
)

// Conversation 一场两人对话。世界线程编排轮转（LLM 异步回调经 mail 回流），
// 参与的两个村民 goroutine 阻塞等待对话结束（EvChatDone）。
// 同时场数上限可配置（设置页 conv_cap，默认 2；LLM 风暴另有网关限流兜底）。
type Conversation struct {
	ID      string   // 会话 ID（initiator|partner），回调回流的路由键
	aID     string   // 发起者
	bID     string   // 应答者
	topic   string   // 话题（默认取最近领主指令）
	turn    int      // 已说台词数
	maxTurn int      // 总台词数 = rounds*2
	history []string // 已说台词（"名字：台词"），供下一轮 prompt 上下文
	aSpeaks bool     // 下一个说话的是 a
}

// startConversationBetween 在两个村民之间开始一场对话（世界线程调用，需持锁环境）。
// aID = 发起者，bID = 应答者（空 = 自动挑一位空闲官员）。
// 返回实际选定的应答者 ID（自动选伴时供发起者"走近"重试用）。
func (e *Engine) startConversationBetween(initID, partnerID, topic string, rounds int) (string, error) {
	if e.convos == nil {
		e.convos = map[string]*Conversation{}
	}
	maxConvos := e.convCap
	if maxConvos <= 0 {
		maxConvos = 2
	}
	if len(e.convos) >= maxConvos {
		return "", fmt.Errorf("聊天的位子满了，待会儿再说")
	}
	if ag := e.agents[initID]; ag != nil && ag.talkingWith != "" {
		return "", fmt.Errorf("你已经在聊天了")
	}
	if partnerID == "" {
		partnerID = e.pickChatPartner(initID)
		if partnerID == "" {
			return "", fmt.Errorf("附近没有能聊天的居民")
		}
	}
	if _, dup := e.convos[initID+"|"+partnerID]; dup {
		return "", fmt.Errorf("你们已经聊上了")
	}
	// 原地触发：两人必须凑到一起（欧氏距离 ≤3，dx²+dy² ≤ 9）才能开聊，不隔空喊话
	initA0, partnerA0 := e.world.Actors[initID], e.world.Actors[partnerID]
	if initA0 == nil || partnerA0 == nil {
		return partnerID, fmt.Errorf("实体不存在")
	}
	if dx, dy := initA0.X-partnerA0.X, initA0.Y-partnerA0.Y; dx*dx+dy*dy > 9 {
		return partnerID, fmt.Errorf("还没走到一起，隔太远聊不了")
	}
	init, initA := e.cits[initID], e.world.Actors[initID]
	partner, partnerA := e.cits[partnerID], e.world.Actors[partnerID]
	if init == nil || initA == nil || partner == nil || partnerA == nil {
		return partnerID, fmt.Errorf("实体不存在")
	}
	if strings.TrimSpace(topic) == "" {
		if n := len(e.edicts); n > 0 {
			topic = e.edicts[n-1]
		} else {
			topic = "领地的生活"
		}
	}
	if rounds <= 0 {
		rounds = e.Pacing().ChatRounds // 轮数来自设置页"节奏"
	}
	if rounds < 1 {
		rounds = 1
	}
	if rounds > 4 {
		rounds = 4
	}
	cv := &Conversation{ID: initID + "|" + partnerID, aID: initID, bID: partnerID,
		topic: topic, maxTurn: rounds * 2, aSpeaks: true}
	e.convos[cv.ID] = cv
	// Busy 所有权归一：对话系统不直接写 Actor.Busy（与 workflow 的 BusyWork/BusyMove
	// 交叉写入曾导致劳作状态被覆盖、村民永久挂死）。双方物理态由各自的
	// onMail(EvInvite/EvChatDone) 在自己的 goroutine 内设置，串行无竞态。
	e.publishEventLocked("chat", fmt.Sprintf("%s 和 %s 聊了起来（话题：%s）", init.Name, partner.Name, topic))
	// 邀请入箱：对方若在干活会被打断；其 goroutine 收到后标记进入对话（心跳不再思考）
	e.deliver(partnerID, Event{Kind: EvInvite, From: initID, Outcome: init.Name + " 拉着你聊：" + topic})
	e.convoTurn(cv)
	return partnerID, nil
}

// officialCountLocked 在册官员人数。需持锁。
func (e *Engine) officialCountLocked() int {
	n := 0
	for _, c := range e.cits {
		if c.Tier == TierOfficial {
			n++
		}
	}
	return n
}

// officialCount 自行加锁版官员人数。
func (e *Engine) officialCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.officialCountLocked()
}

// chatGateLocked 聊天资格的统一判定（唯一权威）：
// 领地里至少两名官员，且目标是另一位官员。返回 "" = 可聊，否则为拒绝原因。
// 词表可见性（decideFor）/ 搭档挑选（pickChatPartner）/ 意图落地（intentToRun）
// 三处共用同一规则，保证"LLM 看得见的"与"实际办得成的"一致。需持锁。
func (e *Engine) chatGateLocked(selfID, targetID string) string {
	if e.officialCountLocked() < 2 {
		return "领地里只有你一个官员，没人可聊"
	}
	if targetID == "" || targetID == selfID {
		return "找不到要聊的人"
	}
	if c := e.cits[targetID]; c == nil || c.Tier != TierOfficial {
		return "平民不参与闲谈（只有官员之间才有对话）"
	}
	return ""
}

// pickChatPartner 为发起者挑一位交谈对象：**仅限官员**（平民不参与对话）、
// 未睡/未聊；不要求静止（在忙的也能聊——邀请即打断，见 EvInvite 语义）。距离最近优先。
// 资格规则见 chatGateLocked。需持锁环境。
func (e *Engine) pickChatPartner(initID string) string {
	init, ok := e.world.Actors[initID]
	if !ok {
		return ""
	}
	best, bestD := "", math.MaxFloat64
	for _, id := range e.ord {
		if id == initID {
			continue
		}
		ag := e.agents[id]
		if ag == nil || ag.sleeping || ag.talkingWith != "" {
			continue
		}
		// 官员之间才社交：平民不参与对话。
		// 对方可被邀请打断（EvInvite 语义），在忙自己事的也能聊——不要求静止。
		if c := e.cits[id]; c == nil || c.Tier != TierOfficial {
			continue
		}
		p, ok := e.world.Actors[id]
		if !ok {
			continue
		}
		dx, dy := p.X-init.X, p.Y-init.Y
		if d := dx*dx + dy*dy; d < bestD {
			best, bestD = id, d
		}
	}
	return best
}

// convoTurn 发起下一句台词的 LLM 生成（异步回调经 mail 回流）。需持锁环境。
func (e *Engine) convoTurn(cv *Conversation) {
	if cv == nil {
		return
	}
	speakerID, listenerID := cv.aID, cv.bID
	if !cv.aSpeaks {
		speakerID, listenerID = cv.bID, cv.aID
	}
	speaker := e.cits[speakerID]
	listener := e.cits[listenerID]
	// 参与方已被放逐（防御：正常路径由 Exile 的 teardownConvosWith 拆除）：
	// 拆掉对话并释放仍在场的一方，避免空指针崩溃
	if speaker == nil || listener == nil {
		other := speakerID
		if speaker != nil {
			other = listenerID
		}
		if oc := e.cits[other]; oc != nil {
			e.deliver(other, Event{Kind: EvChatDone, From: other, Outcome: "对方离开了领地"})
		}
		delete(e.convos, cv.ID)
		return
	}

	rel := speaker.Rel[listenerID]
	sys := `你在一个 2D 像素领地游戏里为村民生成对话台词。村民之间可以友好也可以有摩擦——性格冲突、误会、调侃甚至小争执都是正常的。
要求：中文口语，符合各自性格与关系，每句不超过30字。关系好的可能互相打趣，关系差的可能冷嘲热讽。
只输出 JSON：{"text":"台词","mood":"positive|neutral|negative"}`
	var u strings.Builder
	fmt.Fprintf(&u, "世界：%s。话题：%s。\n", e.world.Name, cv.topic)
	fmt.Fprintf(&u, "你：%s（%s），性格：%s，心情：%s。对方对你的好感：%d。\n", speaker.Name, roleLabel(speaker.Role), speaker.Personality, moodLabel(speaker.Mood), rel)
	fmt.Fprintf(&u, "聊天对象：%s（%s），性格：%s。\n", listener.Name, roleLabel(listener.Role), listener.Personality)
	if len(cv.history) > 0 {
		u.WriteString("之前的对话：\n")
		for _, l := range cv.history {
			u.WriteString(l + "\n")
		}
	}
	u.WriteString("请生成你（" + speaker.Name + "）的下一句台词。")
	e.gw.CompleteAsync(&llm.Request{
		Role:     llm.RoleDialogue,
		System:   sys,
		Messages: []llm.Message{{Role: "user", Content: u.String()}},
		JSONMode: true, Temperature: e.gw.Temperature(llm.RoleDialogue, 0.95),
	}, func(resp *llm.Response, err error) {
		text := "嗯，是啊。"
		mood := ""
		if err == nil {
			type outT struct {
				Text string `json:"text"`
				Mood string `json:"mood"`
			}
			out, perr := llm.ParseData[outT](resp.Text)
			if perr == nil && out.Text != "" {
				text = out.Text
				mood = out.Mood
				if out.Mood == "negative" {
					text = text + "（语气不太好）"
				}
			}
		}
		// 闭包只捕获 cv（ID 路由回流）；世界重置后 convos 表重建，过期回调自动失效
		e.mail <- func() { e.applyTurn(cv.ID, text, mood) }
	})
}

// applyTurn 台词落地：广播、推进或收尾。由世界线程（mail 回流）执行。
func (e *Engine) applyTurn(id, text, mood string) {
	cv := e.convos[id]
	if cv == nil {
		return
	}
	speakerID, listenerID := cv.aID, cv.bID
	if !cv.aSpeaks {
		speakerID, listenerID = cv.bID, cv.aID
	}
	sp, li := e.cits[speakerID], e.cits[listenerID]
	// 说话情绪影响本人的心情（最小闭环：positive +1 / negative -1）
	switch mood {
	case "positive":
		sp.AdjustMood(1)
	case "negative":
		sp.AdjustMood(-1)
	}
	e.publish(&ChatMsg{T: "chat", From: speakerID, FromName: sp.Name, To: listenerID, ToName: li.Name,
		Text: text, Day: e.world.Day(), Hour: e.world.Hour()})
	// 台词冒泡：说话者头顶渲染（4 秒），与侧栏聊天记录同步
	e.publish(&BubbleMsg{T: "bubble", Actor: speakerID, Text: text, Secs: 4})
	line := sp.Name + "：" + text
	cv.history = append(cv.history, line)
	cv.turn++
	cv.aSpeaks = !cv.aSpeaks
	// 说话者的记忆便签投给本人（由其 goroutine 自行落账）
	e.deliver(speakerID, Event{Kind: EvMemo, Outcome: "对" + li.Name + "说：" + text})

	if cv.turn >= cv.maxTurn {
		day, hour := e.world.Day(), e.world.Hour()
		// 关系变化：基于对话轮次中的正/负面情绪比例
		delta := e.relDelta(cv)
		sp.AdjustRel(li.ID, delta)
		li.AdjustRel(sp.ID, delta)
		// 记忆摘要
		sp.AddMemoryAt(day, hour, "chat", "和"+li.Name+"聊了"+cv.topic, 2)
		li.AddMemoryAt(day, hour, "chat", "和"+sp.Name+"聊了"+cv.topic, 2)
		// 关系阈值跨越检测
		e.checkRelThreshold(sp, li)
		e.checkRelThreshold(li, sp)
		// Busy 清理由双方 onMail(EvChatDone) 在各自 goroutine 内执行（所有权归一）
		// 通知双方 goroutine：对话结束（发起者由此解除 waitConvDone；应答者清除对话标记）
		summary := "和" + e.cits[cv.bID].Name + "聊了" + cv.topic
		summary2 := "和" + e.cits[cv.aID].Name + "聊了" + cv.topic
		e.deliver(cv.aID, Event{Kind: EvChatDone, From: cv.bID, Outcome: summary})
		e.deliver(cv.bID, Event{Kind: EvChatDone, From: cv.aID, Outcome: summary2})
		delete(e.convos, cv.ID)
		return
	}
	e.convoTurn(cv)
}

// relDelta 根据对话中正/负面台词比例计算好感变化（-1 到 +2）。
func (e *Engine) relDelta(cv *Conversation) int {
	pos, neg := 0, 0
	for _, h := range cv.history {
		if strings.Contains(h, "生气") || strings.Contains(h, "讨厌") || strings.Contains(h, "不行") {
			neg++
		} else if strings.Contains(h, "谢谢") || strings.Contains(h, "开心") || strings.Contains(h, "好啊") {
			pos++
		}
	}
	d := pos - neg
	if d > 2 {
		d = 2
	}
	if d < -1 {
		d = -1
	}
	return d
}

// checkRelThreshold 检测关系跨越阈值并发布事件。
func (e *Engine) checkRelThreshold(c, other *Citizen) {
	score := c.Rel[other.ID]
	lvl := RelLevel(score)
	memKey := fmt.Sprintf("rel_%s", other.ID)
	// 避免重复触发（用记忆去重）
	for _, m := range c.Mem {
		if strings.Contains(m.Text, memKey) {
			return
		}
	}
	switch lvl {
	case "friend":
		c.AddMemory(e.world, "event", fmt.Sprintf("和%s成了朋友", other.Name), 3)
		e.publishEventLocked("social", fmt.Sprintf("%s 和 %s 成为了朋友", c.Name, other.Name))
	case "best_friend":
		c.AddMemory(e.world, "event", fmt.Sprintf("和%s成了挚友", other.Name), 4)
		e.publishEventLocked("social", fmt.Sprintf("%s 和 %s 成为了挚友", c.Name, other.Name))
	case "rival":
		c.AddMemory(e.world, "event", fmt.Sprintf("和%s关系恶化", other.Name), 3)
		e.publishEventLocked("social", fmt.Sprintf("%s 和 %s 关系恶化了", c.Name, other.Name))
	}
}
