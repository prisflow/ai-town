package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Hub SSE 订阅管理：把 game 层的消息序列化后扇出给所有浏览器连接。
// 慢消费者直接丢弃消息（前端靠 250ms 快照流自愈），不阻塞游戏循环。
type Hub struct {
	mu   sync.Mutex
	subs map[int]chan []byte
	next int
}

func NewHub() *Hub {
	return &Hub{subs: map[int]chan []byte{}}
}

// Publish 序列化并广播。可在任意 goroutine 调用（game 锁内调用，无竞争写入顺序保证）。
func (h *Hub) Publish(v any) {
	h.mu.Lock()
	empty := len(h.subs) == 0
	h.mu.Unlock()
	if empty {
		return // 没有订阅者：跳过序列化（快照每 250ms 一次，空转白耗）
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	msg := make([]byte, 0, len(b)+8)
	msg = append(msg, "data: "...)
	msg = append(msg, b...)
	msg = append(msg, '\n', '\n')
	h.mu.Lock()
	for _, ch := range h.subs {
		select {
		case ch <- msg:
		default: // 满：丢弃
		}
	}
	h.mu.Unlock()
}

// Subscribe 注册一个订阅者。
func (h *Hub) Subscribe() (int, chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.next++
	id := h.next
	ch := make(chan []byte, 1024)
	h.subs[id] = ch
	return id, ch
}

// Unsubscribe 注销。
func (h *Hub) Unsubscribe(id int) {
	h.mu.Lock()
	delete(h.subs, id)
	h.mu.Unlock()
}

// ServeSSE 处理 /api/events。
func (h *Hub) ServeSSE(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	id, ch := h.Subscribe()
	defer h.Unsubscribe(id)

	// 让 EventSource 尽快进入就绪并设置重连间隔
	w.Write([]byte("retry: 3000\n\n"))
	fl.Flush()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := w.Write([]byte(": ping\n\n")); err != nil {
				return
			}
			fl.Flush()
		case msg := <-ch:
			if _, err := w.Write(msg); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
