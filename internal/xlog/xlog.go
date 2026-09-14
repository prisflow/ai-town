package xlog

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---------- 全局状态 ----------

var (
	mu      sync.Mutex
	level   slog.LevelVar // 运行时可调（设置页 debug 开关）
	ring    []string
	ringI   int
	ringCap = 300
	sup     = map[string]suppress{} // 节流窗口与抑制计数
	file    *asyncFile              // 异步文件写者（Sync 等待其排空）
)

type suppress struct {
	until time.Time
	n     int
}

// ---------- 初始化 ----------

// Setup 初始化 slog：文本格式写 stderr（同步）与轮转文件（异步），debug=true 开 DEBUG。
// 文件落盘在后台单写者 goroutine 完成——日志调用方（含持世界锁的世界线程）不被磁盘 I/O 拖住；
// 缓冲满时丢弃并计数（诊断面板的 ring 不受影响，仍完整）。
func Setup(dataDir string, debug bool) error {
	dir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	rw, err := newRotate(filepath.Join(dir, "aitown.log"), 5<<20)
	if err != nil {
		return err
	}
	af := newAsyncFile(2048)
	file = af
	go func() {
		for b := range af.ch {
			if n := af.drop.Swap(0); n > 0 {
				fmt.Fprintf(os.Stderr, "[xlog] 文件日志缓冲溢出，丢弃 %d 条（诊断面板仍完整）\n", n)
			}
			_, _ = rw.Write(b)
		}
	}()
	SetDebug(debug)
	h := slog.NewTextHandler(fanout{os.Stderr, af}, &slog.HandlerOptions{
		Level: &level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Value = slog.StringValue(a.Value.Time().Format("15:04:05"))
			}
			return a
		},
	})
	slog.SetDefault(slog.New(&tee{base: h}))
	return nil
}

// fanout 容错多路输出：逐目标写入，单个失败不影响其他目标。
// 不能用 io.MultiWriter——它在第一个 writer 报错时就短路返回，而 GUI 子系统
// （wails 打包的 -H windowsgui 应用）里 os.Stderr 句柄无效，会导致文件日志整个丢失。
type fanout []io.Writer

func (f fanout) Write(p []byte) (int, error) {
	for _, w := range f {
		_, _ = w.Write(p)
	}
	return len(p), nil
}

// asyncFile 异步落盘适配器：Write 只入队（非阻塞），由 Setup 的后台 goroutine 单写者落盘。
type asyncFile struct {
	ch   chan []byte
	drop atomic.Int64
}

func newAsyncFile(size int) *asyncFile { return &asyncFile{ch: make(chan []byte, size)} }

func (a *asyncFile) Write(p []byte) (int, error) {
	buf := append([]byte(nil), p...)
	select {
	case a.ch <- buf:
	default:
		a.drop.Add(1) // 缓冲满：丢弃（ring 已有完整记录）
	}
	return len(p), nil
}

// Sync 等待异步落盘队列排空（进程退出前调用，尽力把日志写完）。
// 最多等约 1 秒；文件日志非关键路径，超时静默返回。
func Sync() {
	af := file
	if af == nil {
		return
	}
	for i := 0; i < 100; i++ {
		if len(af.ch) == 0 {
			time.Sleep(20 * time.Millisecond) // 给单写者时间落最后一条
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// SetDebug 运行时切换 DEBUG 级（设置页开关保存后调用）。
func SetDebug(on bool) {
	if on {
		level.Set(slog.LevelDebug)
	} else {
		level.Set(slog.LevelInfo)
	}
}

// DebugEnabled 当前是否开着 DEBUG。
func DebugEnabled() bool { return level.Level() <= slog.LevelDebug }

// ---------- 便捷入口 ----------

// Debug 记 DEBUG 级日志。
func Debug(msg string, args ...any) { slog.Debug(msg, args...) }

// Info 记 INFO 级日志。
func Info(msg string, args ...any) { slog.Info(msg, args...) }

// Warn 记 WARN 级日志。
func Warn(msg string, args ...any) { slog.Warn(msg, args...) }

// Error 记 ERROR 级日志。
func Error(msg string, args ...any) { slog.Error(msg, args...) }

// WarnEvery 同 key 节流的 WARN：窗口内首条必记，其余抑制并在下一条里报数。
func WarnEvery(key string, window time.Duration, msg string, args ...any) {
	if n := every(key, window); n >= 0 {
		if n > 0 {
			args = append(args, "suppressed", n)
		}
		slog.Warn(msg, args...)
	}
}

// ErrorEvery 同 key 节流的 ERROR。
func ErrorEvery(key string, window time.Duration, msg string, args ...any) {
	if n := every(key, window); n >= 0 {
		if n > 0 {
			args = append(args, "suppressed", n)
		}
		slog.Error(msg, args...)
	}
}

// every 返回本条放行前窗口内被抑制的条数（0 = 首条）。
func every(key string, window time.Duration) int {
	mu.Lock()
	defer mu.Unlock()
	now := time.Now()
	s, ok := sup[key]
	if ok && now.Before(s.until) {
		s.n++
		sup[key] = s
		return -1
	}
	sup[key] = suppress{until: now.Add(window)}
	n := s.n
	s.n = 0
	return n
}

// Trunc 截断长文本供日志展示（附省略号标记）。
func Trunc(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// ---------- 环形缓冲 tail ----------

// tee 包装基础 Handler：每条日志压入环形缓冲，供 /api/diag 直接读取。
type tee struct {
	base slog.Handler
}

func (t *tee) Handle(ctx context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Time.Format("15:04:05"))
	b.WriteByte(' ')
	b.WriteString(r.Level.String())
	if tr := TraceFrom(ctx); tr != "" {
		b.WriteString(" #" + tr)
	}
	b.WriteByte(' ')
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&b, " %s=%v", a.Key, a.Value)
		return true
	})

	mu.Lock()
	if len(ring) < ringCap {
		ring = append(ring, b.String())
	} else {
		ring[ringI] = b.String()
		ringI = (ringI + 1) % ringCap
	}
	mu.Unlock()
	return t.base.Handle(ctx, r)
}

func (t *tee) WithAttrs(attrs []slog.Attr) slog.Handler       { return &tee{base: t.base.WithAttrs(attrs)} }
func (t *tee) WithGroup(name string) slog.Handler             { return &tee{base: t.base.WithGroup(name)} }
func (t *tee) Enabled(_ context.Context, lvl slog.Level) bool { return lvl >= level.Level() }

// Tail 返回最近 n 条日志（时间正序）。n<=0 表示全部。
func Tail(n int) []string {
	mu.Lock()
	defer mu.Unlock()
	if len(ring) == 0 {
		return nil
	}
	out := make([]string, 0, len(ring))
	if len(ring) < ringCap {
		out = append(out, ring...)
	} else {
		out = append(out, ring[ringI:]...)
		out = append(out, ring[:ringI]...)
	}
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// ---------- 轮转文件 ----------

// rotate 达到 max 字节轮转：aitown.log → aitown.log.1（仅保留一份）。
type rotate struct {
	mu   sync.Mutex
	path string
	max  int64
	f    *os.File
	size int64
}

func newRotate(path string, max int64) (*rotate, error) {
	r := &rotate{path: path, max: max}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotate) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

func (r *rotate) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size+int64(len(p)) > r.max {
		r.f.Close()
		_ = os.Rename(r.path, r.path+".1")
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

// ---------- trace 上下文 ----------

type traceKey struct{}

// WithTrace 把 trace ID 放入 ctx（一次村民思考链路的关联 ID）。
func WithTrace(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, traceKey{}, id)
}

// TraceFrom 取 ctx 中的 trace ID（无则空串）。
func TraceFrom(ctx context.Context) string {
	if v, ok := ctx.Value(traceKey{}).(string); ok {
		return v
	}
	return ""
}
