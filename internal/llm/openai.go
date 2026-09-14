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
	"aitown/internal/xlog"
)

// openAIClient 覆盖一切 OpenAI 兼容接口：
// OpenAI https://api.openai.com/v1
// DeepSeek https://api.deepseek.com/v1
// Moonshot https://api.moonshot.cn/v1
// Qwen(DashScope兼容) https://dashscope.aliyuncs.com/compatible-mode/v1
// 智谱 https://open.bigmodel.cn/api/paas/v4
// OpenRouter https://openrouter.ai/api/v1
// Ollama http://127.0.0.1:11434/v1
// LM Studio http://127.0.0.1:1234/v1
type openAIClient struct {
	pc      *config.ProviderConfig
	hc      http.Client
	timeout time.Duration // 单次请求超时（设置页 llm_timeout_sec；0 = 120s）
}

type oaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type oaRequest struct {
	Model          string      `json:"model"`
	Messages       []oaMessage `json:"messages"`
	Temperature    float64     `json:"temperature,omitempty"`
	MaxTokens      int         `json:"max_tokens,omitempty"`
	ResponseFormat interface{} `json:"response_format,omitempty"`
}

type oaResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (c *openAIClient) endpoint() string {
	base := strings.TrimRight(c.pc.BaseURL, "/ ")
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	return base + "/chat/completions"
}

func (c *openAIClient) Complete(ctx context.Context, req *Request) (*Response, error) {
	resp, err := c.do(ctx, req, req.JSONMode)
	// 某些兼容网关不支持 response_format，去掉后重试一次
	if err != nil && req.JSONMode && strings.Contains(err.Error(), "response_format") {
		resp, err = c.do(ctx, req, false)
	}
	return resp, err
}

func (c *openAIClient) do(ctx context.Context, req *Request, jsonMode bool) (*Response, error) {
	msgs := make([]oaMessage, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, oaMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		msgs = append(msgs, oaMessage{Role: m.Role, Content: m.Content})
	}
	temp := req.Temperature
	if temp <= 0 {
		temp = 0.7
	}
	body := oaRequest{Model: c.pc.Model, Messages: msgs, Temperature: temp}
	// max_tokens 不设 = 交给提供商默认（思考型模型的思维链配额不受限；
	// action 级小请求在这里设上限会截断思维链，导致 content 为空或 JSON 半截）
	if maxTok := req.MaxTokens; maxTok > 0 {
		body.MaxTokens = maxTok
	}
	if jsonMode {
		body.ResponseFormat = map[string]string{"type": "json_object"}
	}
	payload, _ := json.Marshal(body)

	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	if c.pc.APIKey != "" {
		hreq.Header.Set("Authorization", "Bearer "+c.pc.APIKey)
	}
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
	var out oaResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	if out.Error != nil && out.Error.Message != "" {
		return nil, fmt.Errorf("LLM 错误: %s", out.Error.Message)
	}
	if len(out.Choices) == 0 {
		return nil, errNoText
	}
	// 截断可见化：finish_reason=length 说明输出被配额切断（诊断流用）
	if out.Choices[0].FinishReason == "length" {
		xlog.Debug("LLM输出被截断", "role", req.Role, "finish_reason", "length",
			"head", xlog.Trunc(out.Choices[0].Message.Content, 120))
	}
	return &Response{
		Text:      out.Choices[0].Message.Content,
		TokensIn:  out.Usage.PromptTokens,
		TokensOut: out.Usage.CompletionTokens,
	}, nil
}
