// Package agent 提供 QQ 交互 AI 成员能力，本文件实现 OpenAI 兼容的
// chat/completions HTTP 客户端（非流式，无全局状态）。
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ChatMessage 一条对话消息。
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest chat/completions 请求体。
// MaxTokens/Temperature 仅在非零时序列化（omitempty）。
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
}

// ChatResponse chat/completions 响应体，仅解析用到的字段。
type ChatResponse struct {
	Choices []struct {
		Index   int         `json:"index"`
		Message ChatMessage `json:"message"`
	} `json:"choices"`
}

// Content 返回 choices[0].message.content。
func (r *ChatResponse) Content() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.Content
}

// Client OpenAI 兼容 chat/completions 客户端。无全局状态，可并发使用。
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// NewClient 构造客户端。baseURL 为 OpenAI 兼容服务地址（含 /v1），
// timeout 为单次请求超时。
func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
	}
}

// Chat 发起一次非流式补全请求。
// HTTP 非 200、请求超时、响应非法 JSON、choices 为空均显式报错。
func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("agent: 编码请求失败: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("agent: 构造请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("agent: 请求 LLM 失败: %w", err)
	}
	defer httpResp.Body.Close()

	body, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return nil, fmt.Errorf("agent: 读取响应失败: %w", err)
	}
	if httpResp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("agent: LLM 返回 HTTP %d: %s", httpResp.StatusCode, truncateForLog(body))
	}

	var resp ChatResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("agent: 解析响应 JSON 失败: %w (body=%s)", err, truncateForLog(body))
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("agent: 响应中没有 choices (body=%s)", truncateForLog(body))
	}
	return &resp, nil
}

// truncateForLog 截断响应体，避免错误日志过大。
func truncateForLog(body []byte) string {
	const limit = 512
	s := string(body)
	if len(s) > limit {
		return s[:limit] + "...(truncated)"
	}
	return s
}
