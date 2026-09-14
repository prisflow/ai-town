package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ProviderType openai 表示任意 OpenAI 兼容接口（DeepSeek/Moonshot/Qwen/Ollama/OpenRouter 等）。
const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
	ProviderMock      = "mock" // 内置演示模式，无需任何 Key
)

// ProviderConfig 描述一个 LLM 提供商的接入方式。
type ProviderConfig struct {
	Type    string `json:"type"`     // openai | anthropic | mock
	BaseURL string `json:"base_url"` // openai 形如 https://api.deepseek.com/v1；anthropic 形如 https://api.anthropic.com
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

// LimitsConfig LLM 全局限额，保护玩家的钱包。
type LimitsConfig struct {
	MaxConcurrent int `json:"max_concurrent"` // 同时在途的 LLM 请求数
	RPM           int `json:"rpm"`            // 每分钟最大请求数
	OfficialCap   int `json:"official_cap"`   // 官员人数上限（官员有完整 LLM 大脑，数量决定 token 消耗）
	ConvCap       int `json:"conv_cap"`       // 同时进行的对话场数上限（每场对话持续消耗 dialogue token）
	// LLMTimeoutSec 单次 LLM 请求超时（秒）：本地慢模型（Ollama 大模型）可调大。
	LLMTimeoutSec int `json:"llm_timeout_sec"`
	// Temperatures 各角色温度覆盖（角色名→值，0/缺省 = 用内置默认）。
	Temperatures map[string]float64 `json:"temperatures,omitempty"`
}

// PacingConfig 世界节奏与玩法参数（设置页"节奏"分组）。
type PacingConfig struct {
	// ThinkCooldownSec 官员两次思考的最小间隔（秒）：省 token 的总闸，调大省钱、调小吃戏。
	ThinkCooldownSec int `json:"think_cooldown_sec"`
	// ChatCooldownSec 一次对话后的社交冷却（秒）。
	ChatCooldownSec int `json:"chat_cooldown_sec"`
	// ChatRounds 每场对话的轮数（1-4）。
	ChatRounds int `json:"chat_rounds"`
	// EventChance 每游戏小时触发世界事件的概率（%），0 = 关闭事件。
	EventChance int `json:"event_chance"`
	// StartupOfficials 新世界预任命官员数（0 = 全平民；对话需要至少 2 名官员）。
	StartupOfficials int `json:"startup_officials"`
	// NightStart 入夜时刻（24 小时制，如 21.5）。
	NightStart float64 `json:"night_start"`
	// NightEnd 天亮时刻（如 6）。
	NightEnd float64 `json:"night_end"`
	// DayBreak 日界/跳夜目标时刻（如 7）。
	DayBreak float64 `json:"day_break"`
}

// DefaultPacing 内置默认节奏（与历史常量一致）。
func DefaultPacing() PacingConfig {
	return PacingConfig{
		ThinkCooldownSec: 5,
		ChatCooldownSec:  150,
		ChatRounds:       3,
		EventChance:      10,
		StartupOfficials: 2,
		NightStart:       21.5,
		NightEnd:         6,
		DayBreak:         7,
	}
}

// Sanitize 钳位到合法区间（零值字段回落到默认）。
func (p *PacingConfig) Sanitize() {
	d := DefaultPacing()
	clamp := func(v, lo, hi int) int {
		if v < lo {
			return lo
		}
		if v > hi {
			return hi
		}
		return v
	}
	if p.ThinkCooldownSec <= 0 {
		p.ThinkCooldownSec = d.ThinkCooldownSec
	}
	p.ThinkCooldownSec = clamp(p.ThinkCooldownSec, 1, 600)
	if p.ChatCooldownSec <= 0 {
		p.ChatCooldownSec = d.ChatCooldownSec
	}
	p.ChatCooldownSec = clamp(p.ChatCooldownSec, 10, 3600)
	if p.ChatRounds <= 0 {
		p.ChatRounds = d.ChatRounds
	}
	p.ChatRounds = clamp(p.ChatRounds, 1, 4)
	p.EventChance = clamp(p.EventChance, 0, 100)
	p.StartupOfficials = clamp(p.StartupOfficials, 0, 8)
	if p.NightStart <= 0 || p.NightStart > 24 {
		p.NightStart = d.NightStart
	}
	if p.NightEnd < 0 || p.NightEnd >= 24 || p.NightEnd >= p.NightStart {
		p.NightEnd = d.NightEnd
		if p.NightStart <= p.NightEnd {
			p.NightStart = d.NightStart
		}
	}
	if p.DayBreak <= 0 || p.DayBreak >= 24 {
		p.DayBreak = d.DayBreak
	}
}

// Config 顶层配置。Roles 把内部角色路由到某个 provider：
// worldgen(世界初始化) / planner(指令规划) / dialogue(对话) / choice(决策) / narrate(旁白)。
type Config struct {
	Providers map[string]*ProviderConfig `json:"providers"`
	Roles     map[string]string          `json:"roles"`
	Limits    LimitsConfig               `json:"limits"`
	Pacing    PacingConfig               `json:"pacing"` // 世界节奏与玩法参数（设置页"节奏"分组）
	Debug     bool                       `json:"debug"`  // 开启 DEBUG 日志（完整 prompt/原始响应），设置页开关
}

// RoleNames 全部内部角色，供设置界面枚举。
var RoleNames = []string{"worldgen", "planner", "brain", "dialogue", "choice", "narrate"}

// Defaults 返回 mock 演示模式配置：零 Key 即可跑通全流程。
func Defaults() *Config {
	roles := map[string]string{}
	for _, r := range RoleNames {
		roles[r] = "default"
	}
	return &Config{
		Providers: map[string]*ProviderConfig{
			"default": {Type: ProviderMock},
		},
		Roles:  roles,
		Limits: LimitsConfig{MaxConcurrent: 3, RPM: 60, LLMTimeoutSec: 120, OfficialCap: 2, ConvCap: 2},
		Pacing: DefaultPacing(),
	}
}

// Load 从 path 读取配置；文件不存在时返回默认配置（不报错）。
func Load(path string) (*Config, error) {
	c := Defaults()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			c.sanitize() // 缺省配置也要过一遍钳位（如 capacity 默认值）
			return c, nil
		}
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, fmt.Errorf("解析配置 %s: %w", path, err)
	}
	c.sanitize()
	return c, nil
}

// Save 将配置写盘（父目录自动创建）。
func (c *Config) Save(path string) error {
	c.sanitize()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// Masked 返回深拷贝，api_key 只保留尾 4 位用于界面回显。
func (c *Config) Masked() *Config {
	out := Defaults()
	for name, p := range c.Providers {
		p2 := *p
		if len(p2.APIKey) > 4 {
			p2.APIKey = "****" + p2.APIKey[len(p2.APIKey)-4:]
		}
		out.Providers[name] = &p2
	}
	for k, v := range c.Roles {
		out.Roles[k] = v
	}
	out.Limits = c.Limits
	out.Pacing = c.Pacing
	out.Debug = c.Debug
	return out
}

// ProviderForRole 返回角色对应的提供商；路由缺失时回退 default。
func (c *Config) ProviderForRole(role string) (*ProviderConfig, error) {
	name := c.Roles[role]
	if name == "" {
		name = "default"
	}
	p := c.Providers[name]
	if p == nil {
		return nil, fmt.Errorf("角色 %q 路由到不存在的提供商 %q", role, name)
	}
	return p, nil
}

func (c *Config) sanitize() {
	if c.Providers == nil {
		c.Providers = map[string]*ProviderConfig{"default": {Type: ProviderMock}}
	}
	if c.Roles == nil {
		c.Roles = map[string]string{}
	}
	for _, r := range RoleNames {
		if c.Roles[r] == "" {
			c.Roles[r] = "default"
		}
	}
	// 修剪已移除的历史角色键（如 reflect）：避免旧 config.json 继续回显到设置页
	valid := make(map[string]bool, len(RoleNames))
	for _, r := range RoleNames {
		valid[r] = true
	}
	for r := range c.Roles {
		if !valid[r] {
			delete(c.Roles, r)
		}
	}
	if c.Limits.MaxConcurrent <= 0 {
		c.Limits.MaxConcurrent = 3
	}
	if c.Limits.RPM <= 0 {
		c.Limits.RPM = 60
	}
	if c.Limits.OfficialCap <= 0 {
		c.Limits.OfficialCap = 2 // 默认 2 名官员：对话需要至少两人
	}
	if c.Limits.ConvCap <= 0 {
		c.Limits.ConvCap = 2 // 默认同时 2 场对话（LLM 风暴另有网关限流兜底）
	}
	if c.Limits.LLMTimeoutSec <= 0 {
		c.Limits.LLMTimeoutSec = 120
	}
	if c.Limits.LLMTimeoutSec < 10 {
		c.Limits.LLMTimeoutSec = 10
	}
	if c.Limits.LLMTimeoutSec > 600 {
		c.Limits.LLMTimeoutSec = 600
	}
	if c.Limits.Temperatures == nil {
		c.Limits.Temperatures = map[string]float64{}
	}
	for r, t := range c.Limits.Temperatures {
		if t <= 0 || t > 2 {
			delete(c.Limits.Temperatures, r) // 非法值回落内置默认
		}
	}
	c.Pacing.Sanitize()
}
