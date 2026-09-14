package game

import (
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeSaver 内存存档后端（测试用）。
type fakeSaver struct {
	mu      sync.Mutex
	payload []byte
	saves   int
	name    string
	day     int
	loadErr error
}

func (f *fakeSaver) Save(b []byte, name string, day int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.payload = append([]byte(nil), b...)
	f.name, f.day = name, day
	f.saves++
	return nil
}

func (f *fakeSaver) Load() ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	return append([]byte(nil), f.payload...), nil
}

func (f *fakeSaver) Info() (string, int, string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.payload == nil {
		return "", 0, "", false, nil
	}
	return f.name, f.day, "2026-01-01T00:00:00Z", true, nil
}

// TestSaveLoadResumes 存档→读档全链路：世界/村民/工作单恢复、claimed 归一化回
// pending、执行流重建、世界继续推进。
func TestSaveLoadResumes(t *testing.T) {
	saver := &fakeSaver{}
	e, _ := newTestEngine(t)
	e.AttachSaver(saver)
	if err := e.CreateWorld("测试"); err != nil {
		t.Fatal(err)
	}
	if !pumpUntil(e, func() bool { return e.HasWorld() }, 100) {
		t.Fatal("世界应生成")
	}
	pump(e, 300) // 让村民干点活（mock），产生工作单/库存变化

	e.mu.Lock()
	day0, tick0, cits0 := e.world.Day(), e.world.Tick, len(e.cits)
	wood0 := e.world.Inventory["wood"]
	// 手工挂一张 claimed 单：验证读档后回到 pending（幽灵认领者防御）
	j := e.addJob("gather_wood", nil, 3, "", "")
	j.Status = "claimed"
	j.ClaimedBy = e.ord[0]
	e.mu.Unlock()

	if err := e.SaveNow(); err != nil {
		t.Fatal(err)
	}
	if saver.saves == 0 {
		t.Fatal("SaveNow 应写入存档")
	}

	e2, _ := newTestEngine(t)
	e2.AttachSaver(saver)
	loaded, err := e2.LoadSave()
	if err != nil || !loaded {
		t.Fatalf("读档应成功: loaded=%v err=%v", loaded, err)
	}
	if !e2.HasWorld() {
		t.Fatal("读档后世界应就绪")
	}
	e2.mu.Lock()
	if e2.world.Day() != day0 || e2.world.Tick != tick0 || len(e2.cits) != cits0 {
		t.Fatalf("读档状态不一致: day=%d/%d tick=%d/%d cits=%d/%d",
			e2.world.Day(), day0, e2.world.Tick, tick0, len(e2.cits), cits0)
	}
	if e2.world.Inventory["wood"] != wood0 {
		t.Fatalf("库存应随档恢复: %d/%d", e2.world.Inventory["wood"], wood0)
	}
	if len(e2.agents) != len(e2.cits) {
		t.Fatalf("执行流应重建: %d/%d", len(e2.agents), len(e2.cits))
	}
	found := false
	for _, jj := range e2.jobs {
		if jj.ID == j.ID {
			found = true
			if jj.Status != "pending" || jj.ClaimedBy != "" {
				t.Fatalf("claimed 单应归一化为 pending: %s %s", jj.Status, jj.ClaimedBy)
			}
		}
	}
	if !found {
		t.Fatal("工作单应随档恢复")
	}
	e2.mu.Unlock()

	// 继续推进：世界时间前进（执行流在跑）
	pump(e2, 50)
	e2.mu.Lock()
	if e2.world.Tick <= tick0 {
		t.Fatalf("读档后世界应继续推进: %d -> %d", tick0, e2.world.Tick)
	}
	e2.mu.Unlock()
}

// TestAutoSaveOnNewDay 日界自动存档：跨天后异步写盘（等待后台单写者完成）。
func TestAutoSaveOnNewDay(t *testing.T) {
	saver := &fakeSaver{}
	e, _ := newTestEngine(t)
	e.AttachSaver(saver)
	if err := e.CreateDefaultWorld(); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	if e.lastSaveDay != e.world.Day() {
		t.Fatalf("新世界应把自动存档基点初始化为当天: lastSaveDay=%d day=%d", e.lastSaveDay, e.world.Day())
	}
	e.mu.Unlock()

	pump(e, 4000)  // testTimeFactor=20 → 约 11 游戏天，必然跨日界
	deadline := 50 // 后台写盘最多等 50×10ms
	for i := 0; i < deadline; i++ {
		saver.mu.Lock()
		n := saver.saves
		saver.mu.Unlock()
		if n > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("跨天后应触发自动存档")
}

// TestLoadSaveBad 坏档/错版本/IO 错误防御：宁可报错，不造半个世界。
func TestLoadSaveBad(t *testing.T) {
	// 坏 JSON
	e, _ := newTestEngine(t)
	e.AttachSaver(&fakeSaver{payload: []byte("not json")})
	if loaded, err := e.LoadSave(); err == nil || loaded {
		t.Fatalf("坏档应报错且不加载: %v %v", loaded, err)
	}
	if e.HasWorld() {
		t.Fatal("坏档不应产生世界")
	}

	// 版本不匹配
	e2, _ := newTestEngine(t)
	b, _ := json.Marshal(SaveData{Version: 999, World: json.RawMessage(`{}`)})
	e2.AttachSaver(&fakeSaver{payload: b})
	if _, err := e2.LoadSave(); err == nil {
		t.Fatal("版本不匹配应报错")
	}

	// 读取 IO 错误
	e3, _ := newTestEngine(t)
	e3.AttachSaver(&fakeSaver{loadErr: errTestIO})
	if _, err := e3.LoadSave(); err == nil {
		t.Fatal("读取失败应上抛")
	}

	// 未配置后端：静默返回
	e4, _ := newTestEngine(t)
	if loaded, err := e4.LoadSave(); loaded || err != nil {
		t.Fatalf("无后端应静默: %v %v", loaded, err)
	}
	if err := e4.SaveNow(); err == nil {
		t.Fatal("无后端手动存档应报错")
	}
}

var errTestIO = errors.New("模拟 IO 失败")

// TestSaveMetaAndNewWorldOverwrite 启动选择页的数据依据与"开新世界即覆盖旧档"：
// 空后端无档 → 建新世界立即排入一份写盘（不必等第一个日界）。
func TestSaveMetaAndNewWorldOverwrite(t *testing.T) {
	saver := &fakeSaver{}
	e, _ := newTestEngine(t)
	e.AttachSaver(saver)

	if meta := e.SaveMeta(); meta.Exists {
		t.Fatal("空后端不应报告有存档")
	}
	if meta := e.State().Save; meta.Exists {
		t.Fatal("State().Save 空后端不应报告有存档")
	}

	if err := e.CreateDefaultWorld(); err != nil {
		t.Fatal(err)
	}
	// 新世界立即异步落档：等后台单写者完成
	for i := 0; i < 50; i++ {
		if meta := e.SaveMeta(); meta.Exists {
			if meta.WorldName == "" || meta.Day == 0 {
				t.Fatalf("存档概况缺字段: %+v", meta)
			}
			if !e.State().Save.Exists {
				t.Fatal("State().Save 应同步反映存档存在")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("新世界应立即落一份档（开新世界覆盖旧档的依据）")
}
