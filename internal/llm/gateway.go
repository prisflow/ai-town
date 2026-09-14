package llm

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"aitown/internal/config"
	"aitown/internal/xlog"
)

// 内部角色名。每个角色可路由到不同的提供商/模型（例如规划用强模型，对话用便宜模型）。
const (
	RoleWorldgen = "worldgen"
	RolePlanner  = "planner"
	RoleBrain    = "brain" // 村民自主决策规划器（空闲思考时选下一步意图）
	RoleDialogue = "dialogue"
	RoleChoice   = "choice"
	RoleNarrate  = "narrate"
)

// Message 一条对话消息。Role: system/user/assistant。
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Request 一次补全请求。Role 仅用于网关路由与 mock 分支，不会发给提供商。
type Request struct {
	Role        string
	System      string
	Messages    []Message
	JSONMode    bool // 要求返回纯 JSON
	MaxTokens   int
	Temperature float64
}

// Response 补全结果与 token 用量。
type Response struct {
	Text      string
	TokensIn  int
	TokensOut int
}

// Client 是提供商客户端的最小接口。
type Client interface {
	Complete(ctx context.Context, req *Request) (*Response, error)
}

// Stats 网关累计统计（界面上的 token 消耗仪表）。
type Stats struct {
	Calls         int64  `json:"llm_calls"`
	Errors        int64  `json:"llm_errors"`
	TokensIn      int64  `json:"tokens_in"`
	TokensOut     int64  `json:"tokens_out"`
	LastError     string `json:"last_error,omitempty"`
	LastLatencyMS int64  `json:"last_latency_ms"`
}

// Gateway LLM 网关：并发信号量 + 滑动窗口 RPM 限制 + 统计。
type Gateway struct {
	mu   sync.RWMutex
	cfg  *config.Config
	sem  chan struct{}
	rpm  []time.Time
	rpmM sync.Mutex

	st  Stats
	stM sync.Mutex

	clientsM sync.Mutex
	clients  map[*config.ProviderConfig]Client // 按提供商缓存客户端（mock 客户端携带确定性状态，不能每请求重建）
}

// NewGateway 创建网关。
func NewGateway(cfg *config.Config) *Gateway {
	g := &Gateway{cfg: cfg}
	g.rebuildSem()
	return g
}

// UpdateConfig 热更新配置（设置页保存后立即生效）。
func (g *Gateway) UpdateConfig(cfg *config.Config) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.cfg = cfg
	g.rebuildSem()
	g.clientsM.Lock()
	g.clients = nil // 配置变更：客户端按新提供商重建
	g.clientsM.Unlock()
}

// Temperature 返回角色的温度覆盖（设置页"创意度"；未配置时用调用方 fallback）。
func (g *Gateway) Temperature(role string, fallback float64) float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.cfg != nil {
		if t, ok := g.cfg.Limits.Temperatures[role]; ok && t > 0 {
			return t
		}
	}
	return fallback
}

// llmTimeout 当前的单次 LLM 请求超时（设置页 llm_timeout_sec；默认 120s）。
func (g *Gateway) llmTimeout() time.Duration {
	g.mu.RLock()
	defer g.mu.RUnlock()
	sec := 0
	if g.cfg != nil {
		sec = g.cfg.Limits.LLMTimeoutSec
	}
	if sec <= 0 {
		sec = 120
	}
	return time.Duration(sec) * time.Second
}

func (g *Gateway) rebuildSem() {
	n := g.cfg.Limits.MaxConcurrent
	if n <= 0 {
		n = 3
	}
	g.sem = make(chan struct{}, n)
}

func (g *Gateway) clientFor(role string) (Client, error) {
	g.mu.RLock()
	pc, err := g.cfg.ProviderForRole(role)
	g.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	g.clientsM.Lock()
	defer g.clientsM.Unlock()
	if cl, ok := g.clients[pc]; ok {
		return cl, nil
	}
	cl, err := newClient(pc, g.llmTimeout())
	if err != nil {
		return nil, err
	}
	if g.clients == nil {
		g.clients = map[*config.ProviderConfig]Client{}
	}
	g.clients[pc] = cl
	return cl, nil
}

func newClient(pc *config.ProviderConfig, timeout time.Duration) (Client, error) {
	switch pc.Type {
	case config.ProviderOpenAI:
		return &openAIClient{pc: pc, timeout: timeout}, nil
	case config.ProviderAnthropic:
		return &anthropicClient{pc: pc, timeout: timeout}, nil
	case config.ProviderMock:
		return &mockClient{}, nil
	default:
		return nil, fmt.Errorf("未知提供商类型 %q（支持 openai/anthropic/mock）", pc.Type)
	}
}

// Complete 同步补全：应用并发与 RPM 限额（mock 直连不限流），记录统计。
// 网关是日志咽喉点：所有 LLM 成败在此统一落日志（诊断流），调用方无需重复打点。
func (g *Gateway) Complete(ctx context.Context, req *Request) (*Response, error) {
	start := time.Now()
	cl, err := g.clientFor(req.Role)
	if err != nil {
		g.recordErr(err, 0)
		xlog.Error("LLM路由失败", "role", req.Role, "trace", xlog.TraceFrom(ctx), "err", err)
		return nil, err
	}
	// mock 为本地确定性实现，无限流必要；真实提供商走信号量 + 滑动窗口背压
	if _, isMock := cl.(*mockClient); !isMock {
		select {
		case g.sem <- struct{}{}:
			defer func() { <-g.sem }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if err := g.waitRPM(ctx); err != nil {
			return nil, err
		}
	}

	resp, err := cl.Complete(ctx, req)
	lat := time.Since(start).Milliseconds()
	if err != nil {
		g.recordErr(err, lat)
		xlog.Error("LLM请求失败", "role", req.Role, "trace", xlog.TraceFrom(ctx), "latency_ms", lat, "err", err)
		return nil, err
	}
	g.stM.Lock()
	g.st.Calls++
	g.st.TokensIn += int64(resp.TokensIn)
	g.st.TokensOut += int64(resp.TokensOut)
	g.st.LastLatencyMS = lat
	g.stM.Unlock()
	xlog.Debug("LLM请求完成", "role", req.Role, "trace", xlog.TraceFrom(ctx),
		"latency_ms", lat, "tokens_in", resp.TokensIn, "tokens_out", resp.TokensOut)
	return resp, nil
}

// recordErr 记入统计（错误计数 + 最后错误文案）。
func (g *Gateway) recordErr(err error, latMS int64) {
	g.stM.Lock()
	g.st.Errors++
	g.st.LastError = err.Error()
	g.st.LastLatencyMS = latMS
	g.stM.Unlock()
}

// CompleteAsync 异步补全。cb 在后台 goroutine 执行，调用方（游戏循环）
// 负责把结果投递回自己的单线程邮箱，禁止在 cb 里直接改游戏状态。
func (g *Gateway) CompleteAsync(req *Request, cb func(*Response, error)) {
	go func() {
		resp, err := g.Complete(context.Background(), req)
		cb(resp, err)
	}()
}

// TestProvider 用一次极小请求探测连通性，返回延迟毫秒。
func (g *Gateway) TestProvider(ctx context.Context, name string) (int64, error) {
	g.mu.RLock()
	pc := g.cfg.Providers[name]
	g.mu.RUnlock()
	if pc == nil {
		return 0, fmt.Errorf("提供商 %q 不存在", name)
	}
	cl, err := newClient(pc, g.llmTimeout())
	if err != nil {
		return 0, err
	}
	start := time.Now()
	// 不设 MaxTokens：思考型模型的思维链也要配额，探测请求同样不能设上限
	_, err = cl.Complete(ctx, &Request{
		Role:     "test",
		System:   "ping",
		Messages: []Message{{Role: "user", Content: "回复：pong"}},
	})
	return time.Since(start).Milliseconds(), err
}

func (g *Gateway) waitRPM(ctx context.Context) error {
	g.mu.RLock()
	limit := g.cfg.Limits.RPM
	g.mu.RUnlock()
	if limit <= 0 {
		return nil
	}
	for {
		g.rpmM.Lock()
		now := time.Now()
		keep := g.rpm[:0]
		for _, t := range g.rpm {
			if now.Sub(t) < time.Minute {
				keep = append(keep, t)
			}
		}
		g.rpm = keep
		if len(g.rpm) < limit {
			g.rpm = append(g.rpm, now)
			g.rpmM.Unlock()
			return nil
		}
		next := g.rpm[0].Add(time.Minute)
		g.rpmM.Unlock()
		wait := time.Until(next)
		if wait < 10*time.Millisecond {
			wait = 10 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// Stats 返回统计快照。
func (g *Gateway) Stats() Stats {
	g.stM.Lock()
	defer g.stM.Unlock()
	return g.st
}

// errUnknown 提供给客户端实现复用。
var errNoText = errors.New("响应中没有文本内容")
