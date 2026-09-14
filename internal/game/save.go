package game

// save.go —— 存档/读档：世界 + 村民 + 工作单的完整快照（agents 运行态不存）。
//
// 【为什么这样存】
//   - 全量 JSON：当前状态规模（几十 KB ~ 几 MB）全量读写最简单可靠；
//   - claimed 单写回 pending：读档后这些活重新可领，不会卡在"幽灵认领者"手里；
//   - 运行态不序列化：读档时重建全部村民 goroutine，从空闲状态重新生活；
//   - 异步落盘：序列化在锁内、写盘在后台单写者，日界存档不阻塞世界线程；
//     退出与手动存档走同步路径（返回时已写完）。
//
// 【已知取舍】Citizen 的动态字段（记忆/关系/心情）由各自 agent goroutine 写，
// 存档读取存在弱一致窗口。当前单机单用户可接受；等"快操作意图化"（ROADMAP）后收口。

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"aitown/internal/sim"
	"aitown/internal/xlog"
)

// saveVersion 存档格式版本（LoadSave 严格校验，不匹配拒绝加载）。
const saveVersion = 1

// Saver 存档后端（main 注入 *store.DB；nil = 纯内存运行）。
type Saver interface {
	Save(payload []byte, worldName string, day int) error
	// Load 返回 (payload, nil)；无存档返回 (nil, nil)；读取失败返回 error。
	Load() ([]byte, error)
	// Info 存档概况（启动选择页展示；无存档 ok=false）。
	Info() (name string, day int, savedAt string, ok bool, err error)
}

// SaveData 一份完整存档。
type SaveData struct {
	Version         int                 `json:"version"`
	SavedAt         string              `json:"saved_at"`
	World           json.RawMessage     `json:"world"`
	Citizens        map[string]*Citizen `json:"citizens"`
	Order           []string            `json:"order"`
	Jobs            []*Job              `json:"jobs"`
	JobSeq          int                 `json:"job_seq"`
	Edicts          []string            `json:"edicts"`
	LastFoodWarnDay int                 `json:"last_food_warn_day"`
	EventLastDay    map[string]int      `json:"event_last_day"`
}

// saveJob 异步写盘任务。
type saveJob struct {
	payload []byte
	name    string
	day     int
}

// SaveMeta 存档概况（启动选择页用）：不加载完整存档也能告诉玩家"上次玩到哪"。
type SaveMeta struct {
	// Exists 是否存在可继续的存档。
	Exists bool `json:"exists"`
	// WorldName 存档里的世界名。
	WorldName string `json:"world_name"`
	// Day 存档时的游戏天数。
	Day int `json:"day"`
	// SavedAt 保存时刻（RFC3339）。
	SavedAt string `json:"saved_at"`
}

// SaveMeta 查询存档概况（无后端/无档时 Exists=false）。
func (e *Engine) SaveMeta() SaveMeta {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.saveMetaLocked()
}

// saveMetaLocked 需持锁。
func (e *Engine) saveMetaLocked() SaveMeta {
	if e.saver == nil {
		return SaveMeta{}
	}
	name, day, at, ok, err := e.saver.Info()
	if err != nil {
		xlog.Warn("读取存档信息失败", "err", err)
		return SaveMeta{}
	}
	return SaveMeta{Exists: ok, WorldName: name, Day: day, SavedAt: at}
}

// AttachSaver 注入存档后端并启动异步写盘 goroutine。
// 须在 Start 之前调用；传 nil 表示禁用持久化（测试/纯内存模式）。
func (e *Engine) AttachSaver(s Saver) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.saver = s
	if s != nil && e.saveCh == nil {
		e.saveCh = make(chan saveJob, 1)
		go e.saveLoop()
	}
}

// saveLoop 后台单写者：串行落盘，失败只记日志（下一次日界/退出会再写）。
func (e *Engine) saveLoop() {
	for jb := range e.saveCh {
		if err := e.saver.Save(jb.payload, jb.name, jb.day); err != nil {
			xlog.Warn("自动存档失败", "day", jb.day, "err", err)
		}
	}
}

// marshalSaveLocked 构建存档字节（需持锁）。不改动内存状态：claimed→pending
// 的归一化只发生在导出的副本上（内存里的活还要继续干）。
func (e *Engine) marshalSaveLocked() ([]byte, error) {
	if e.world == nil {
		return nil, errors.New("还没有世界")
	}
	world, err := e.world.MarshalState()
	if err != nil {
		return nil, err
	}
	jobs := make([]*Job, len(e.jobs))
	for i, j := range e.jobs {
		cp := *j
		if cp.Status == "claimed" {
			cp.Status = "pending"
			cp.ClaimedBy = ""
		}
		jobs[i] = &cp
	}
	edicts := append([]string(nil), e.edicts...)
	sd := SaveData{
		Version:         saveVersion,
		SavedAt:         time.Now().Format(time.RFC3339),
		World:           world,
		Citizens:        e.cits,
		Order:           e.ord,
		Jobs:            jobs,
		JobSeq:          e.jobSeq,
		Edicts:          edicts,
		LastFoodWarnDay: e.lastFoodWarnDay,
		EventLastDay:    e.eventLastDay,
	}
	return json.Marshal(sd)
}

// SaveNow 手动/退出存档（同步落盘，返回时已写完）。
func (e *Engine) SaveNow() error {
	e.mu.Lock()
	if e.saver == nil {
		e.mu.Unlock()
		return errors.New("未启用存档")
	}
	b, err := e.marshalSaveLocked()
	if err != nil {
		e.mu.Unlock()
		return err
	}
	name, day := e.world.Name, e.world.Day()
	e.mu.Unlock()
	if err := e.saver.Save(b, name, day); err != nil {
		return err
	}
	xlog.Info("手动存档完成", "world", name, "day", day, "bytes", len(b))
	return nil
}

// autoSaveIfNewDayLocked 日界自动存档：游戏天数变化时投一份异步写盘（每天一次）。
// 需持锁。首个观测日只记录不写（世界刚建/刚读档，没有新东西可存）。
func (e *Engine) autoSaveIfNewDayLocked() {
	if e.saver == nil || e.world == nil || e.saveCh == nil {
		return
	}
	day := e.world.Day()
	if day == e.lastSaveDay {
		return
	}
	if e.lastSaveDay == 0 {
		e.lastSaveDay = day
		return
	}
	e.lastSaveDay = day
	e.saveAsyncLocked()
}

// saveAsyncLocked 把当前状态投给后台单写者（需持锁；队列容量 1，忙碌时跳过本次）。
func (e *Engine) saveAsyncLocked() {
	if e.saver == nil || e.world == nil || e.saveCh == nil {
		return
	}
	b, err := e.marshalSaveLocked()
	if err != nil {
		xlog.Warn("存档序列化失败", "err", err)
		return
	}
	select {
	case e.saveCh <- saveJob{payload: b, name: e.world.Name, day: e.world.Day()}:
		xlog.Info("存档已排入写盘", "day", e.world.Day(), "bytes", len(b))
	default: // 上一份还在写：跳过本次
	}
}

// LoadSave 尝试从存档恢复世界与村民；返回是否加载成功。
// 须在 Start 之前调用（世界为空时）。失败返回 error，调用方记日志后按"无世界"继续。
func (e *Engine) LoadSave() (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.saver == nil || e.world != nil {
		return false, nil
	}
	payload, err := e.saver.Load()
	if err != nil {
		return false, err
	}
	if payload == nil {
		return false, nil
	}
	var sd SaveData
	if err := json.Unmarshal(payload, &sd); err != nil {
		return false, fmt.Errorf("存档解析失败: %w", err)
	}
	if sd.Version != saveVersion {
		return false, fmt.Errorf("存档版本不兼容（存档 %d，程序 %d）", sd.Version, saveVersion)
	}
	w, err := sim.UnmarshalState(sd.World)
	if err != nil {
		return false, err
	}
	e.world = w
	e.cits = sd.Citizens
	if e.cits == nil {
		e.cits = map[string]*Citizen{}
	}
	// 只保留同时存在 citizen 的居民（防御手工改档/半截存档）
	ord := make([]string, 0, len(sd.Order))
	for _, id := range sd.Order {
		if e.cits[id] != nil {
			ord = append(ord, id)
		}
	}
	e.ord = ord
	e.jobs = sd.Jobs
	e.jobSeq = sd.JobSeq
	e.edicts = sd.Edicts
	e.lastFoodWarnDay = sd.LastFoodWarnDay
	e.eventLastDay = sd.EventLastDay
	if e.eventLastDay == nil {
		e.eventLastDay = map[string]int{}
	}
	e.convos = map[string]*Conversation{}
	e.agents = map[string]*agent{}
	e.lastSaveDay = w.Day()
	e.generating = false
	// 重建全部村民执行流：从空闲状态重新生活
	e.spawnAgentsLocked()
	e.publish(&WorldMsg{T: "world", World: e.worldJSONLocked()})
	e.publishEventLocked("system", fmt.Sprintf("已从存档继续：第 %d 天，%d 名居民。", w.Day(), len(e.cits)))
	xlog.Info("读档完成", "world", w.Name, "day", w.Day(), "citizens", len(e.cits), "jobs", len(e.jobs))
	return true, nil
}
