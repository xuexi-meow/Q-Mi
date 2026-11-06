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

// ToolFunction 一次工具调用的函数部分；Arguments 是 JSON 字符串。
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolCall 模型响应里的一次工具调用（OpenAI 格式）。
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

// ToolSchema 发给模型的单个工具 schema。
type ToolSchema struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

// ToolDef chat/completions 请求 tools 数组的一项。
type ToolDef struct {
	Type     string     `json:"type"`
	Function ToolSchema `json:"function"`
}

// ContentPart 多模态消息内容数组的一项（OpenAI chat 兼容格式）：
// type 为 "text" 时携带 Text；为 "image_url" 时携带 ImageURL（P2.7）。
type ContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

// ChatMessage 一条对话消息。Content 为字符串，或多模态内容数组 []ContentPart
// （P2.7：消息携带图片时）。ToolCalls 用于 assistant 回填工具调用，
// ToolCallID 用于 role=tool 的结果消息。
type ChatMessage struct {
	Role       string     `json:"role"`
	Content    any        `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

// ChatRequest chat/completions 请求体。
// MaxTokens/Temperature/Tools 仅在非零/非空时序列化（omitempty）。
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	Tools       []ToolDef     `json:"tools,omitempty"`
}

// responseMessage 响应 choices[].message：比请求消息多 reasoning_content
// （DeepSeek 系思维链，仅捕获不注入上下文）。Content 兼容字符串与多模态
// 数组两种响应形态，文本经 TextContent 提取。
type responseMessage struct {
	Role             string     `json:"role"`
	Content          any        `json:"content"`
	ToolCalls        []ToolCall `json:"tool_calls"`
	ReasoningContent string     `json:"reasoning_content"`
}

// ChatResponse chat/completions 响应体，仅解析用到的字段。
type ChatResponse struct {
	Choices []struct {
		Index   int             `json:"index"`
		Message responseMessage `json:"message"`
	} `json:"choices"`
}

// TextContent 返回 choices[0].message.content 的文本：content 为字符串时
// 原样返回；为多模态数组时按序拼接所有 text 部分（其余类型跳过）；
// 其他形态视为空。
func (r *ChatResponse) TextContent() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	switch c := r.Choices[0].Message.Content.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, part := range c {
			m, ok := part.(map[string]any)
			if !ok {
				continue
			}
			if typ, _ := m["type"].(string); typ == "text" {
				if txt, ok := m["text"].(string); ok {
					b.WriteString(txt)
				}
			}
		}
		return b.String()
	default:
		return ""
	}
}

// ToolCalls 返回 choices[0].message.tool_calls。
func (r *ChatResponse) ToolCalls() []ToolCall {
	if r == nil || len(r.Choices) == 0 {
		return nil
	}
	return r.Choices[0].Message.ToolCalls
}

// ReasoningContent 返回 choices[0].message.reasoning_content（仅捕获，不注入上下文）。
func (r *ChatResponse) ReasoningContent() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.ReasoningContent
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
