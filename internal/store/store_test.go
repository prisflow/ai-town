package store

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSaveLoadRoundTrip 单槽读写的完整往返：保存 → 读取 → 元信息一致。
func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "saves.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if got, err := db.Load(); err != nil || got != nil {
		t.Fatalf("空库应返回 (nil, nil)，实际 %v %v", got, err)
	}
	if _, _, _, ok, err := db.Info(); err != nil || ok {
		t.Fatalf("空库 Info 应为 ok=false: %v %v", ok, err)
	}

	payload := []byte(`{"version":1,"note":"测试存档"}`)
	if err := db.Save(payload, "迷雾河谷", 5); err != nil {
		t.Fatal(err)
	}
	got, err := db.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("读回的 payload 不一致: %s", got)
	}
	name, day, savedAt, ok, err := db.Info()
	if err != nil || !ok || name != "迷雾河谷" || day != 5 {
		t.Fatalf("元信息错误: %s %d %v %v", name, day, ok, err)
	}
	if _, err := time.Parse(time.RFC3339, savedAt); err != nil {
		t.Fatalf("保存时间格式错误: %q", savedAt)
	}

	// 覆盖保存：单槽应只保留最新一份
	if err := db.Save([]byte(`{"version":1,"note":"第二次"}`), "迷雾河谷", 6); err != nil {
		t.Fatal(err)
	}
	got, err = db.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("第二次")) {
		t.Fatalf("覆盖后应是新 payload: %s", got)
	}
}

// TestReopenKeepsData 关库重开后数据仍在（WAL 落盘）。
func TestReopenKeepsData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "saves.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Save([]byte(`{"k":"v"}`), "测试", 1); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("存档文件应存在: %v", err)
	}
	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	if _, err := db2.Load(); err != nil {
		t.Fatalf("重开后应能读到存档: %v", err)
	}
}
