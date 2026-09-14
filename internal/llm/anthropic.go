package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"aitown/internal/config"
)

// anthropicClient 原生 Claude Messages API。BaseURL 形如 https://api.anthropic.com。
type anthropicClient struct {
	pc      *config.ProviderConfig
	hc      http.Client
	timeout time.Duration // 单次请求超时（设置页 llm_timeout_sec；0 = 120s）
}

type anMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anRequest struct {
	Model       string      `json:"model"`
	System      string      `json:"system,omitempty"`
	Messages    []anMessage `json:"messages"`
	MaxTokens   int         `json:"max_tokens"`
	Temperature float64     `json:"temperature,omitempty"`
}

type anResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *anthropicClient) endpoint() string {
	base := strings.TrimRight(c.pc.BaseURL, "/ ")
	if base == "" {
		base = "https://api.anthropic.com"
	}
	return base + "/v1/messages"
}

func (c *anthropicClient) Complete(ctx context.Context, req *Request) (*Response, error) {
	sys := req.System
	if req.JSONMode {
		if sys != "" {
			sys += "\n\n"
		}
		sys += "只返回一个 JSON 对象，不要包含任何其他文本或代码块标记。"
	}
	msgs := make([]anMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		role := m.Role
		if role != "user" && role != "assistant" {
			role = "user"
		}
		msgs = append(msgs, anMessage{Role: role, Content: m.Content})
	}
	// Anthropic API 要求必传 max_tokens；调用方不设时给足量兜底（不做 action 级截断）
	maxTok := req.MaxTokens
	if maxTok <= 0 {
		maxTok = 4096
	}
	temp := req.Temperature
	if temp <= 0 {
		temp = 0.7
	}
	body := anRequest{Model: c.pc.Model, System: sys, Messages: msgs, MaxTokens: maxTok, Temperature: temp}
	payload, _ := json.Marshal(body)

	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	if c.pc.APIKey != "" {
		hreq.Header.Set("x-api-key", c.pc.APIKey)
	}
	hreq.Header.Set("anthropic-version", "2023-06-01")
	hc := c.hc
	hc.Timeout = c.timeout
	if hc.Timeout <= 0 {
		hc.Timeout = 120 * time.Second
	}

	hresp, err := hc.Do(hreq)
	if err != nil {
		return nil, fmt.Errorf("请求 %s 失败: %w", c.endpoint(), err)
	}
	defer hresp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(hresp.Body, 1<<20))
	if hresp.StatusCode != http.StatusOK {
		snippet := strings.TrimSpace(string(raw))
		if len(snippet) > 300 {
			snippet = snippet[:300]
		}
		return nil, fmt.Errorf("LLM HTTP %d: %s", hresp.StatusCode, snippet)
	}
	var out anResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	if out.Error != nil && out.Error.Message != "" {
		return nil, fmt.Errorf("LLM 错误: %s", out.Error.Message)
	}
	var sb strings.Builder
	for _, blk := range out.Content {
		if blk.Type == "text" {
			sb.WriteString(blk.Text)
		}
	}
	if sb.Len() == 0 {
		return nil, errNoText
	}
	return &Response{
		Text:      sb.String(),
		TokensIn:  out.Usage.InputTokens,
		TokensOut: out.Usage.OutputTokens,
	}, nil
}
