package server

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aitown/internal/config"
	"aitown/internal/game"
	"aitown/internal/llm"
)

// TestSSEDeliversTypedFrames 端到端验证 SSE 帧都带类型字段（t），防止构造时漏设 T。
func TestSSEDeliversTypedFrames(t *testing.T) {
	cfg := config.Defaults()
	gw := llm.NewGateway(cfg)
	hub := NewHub()
	eng := game.NewEngine(gw, hub)
	eng.Start()
	srv := New(eng, gw, cfg, "", nil, hub)

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// 订阅 SSE
	req, _ := http.NewRequest("GET", ts.URL+"/api/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	frames := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "data: ") {
				frames <- line
			}
		}
	}()

	// 建世界 → 等就绪 → 下指令
	post := func(path string, body string) {
		r, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
	}
	post("/api/world", `{"prompt":"测试"}`)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		st := eng.State()
		if st.HasWorld {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	post("/api/edict", `{"text":"多储备一些木材"}`)

	// 收集 3 秒帧，校验每帧都有类型且出现关键类型
	seen := map[string]bool{}
	timeout := time.After(3 * time.Second)
	for {
		select {
		case <-timeout:
			if !seen["edict"] || !seen["event"] {
				t.Fatalf("应收到 edict/event 帧，实际: %v", seen)
			}
			return
		case line := <-frames:
			var m map[string]any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m); err != nil {
				t.Fatalf("帧不是合法 JSON: %s", line)
			}
			typ, _ := m["t"].(string)
			if typ == "" {
				t.Fatalf("帧缺少类型字段: %s", line)
			}
			seen[typ] = true
		}
	}
}
