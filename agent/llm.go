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
	// ReasoningContent 思考模式的思维链：只要请求带 tools，后续所有请求必须
	// 原样回传，否则 DeepSeek 返回 400。只存在于进程内工作上下文，
	// 不写消息库、不进压缩摘要。
	ReasoningContent string `json:"reasoning_content,omitempty"`
	kind             contextKind
}

// ChatRequest chat/completions 请求体。
// MaxTokens/Temperature/Tools 仅在非零/非空时序列化（omitempty）。
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
	Tools       []ToolDef     `json:"tools,omitempty"`
	ToolChoice  string        `json:"tool_choice,omitempty"`
}

// responseMessage 响应 choices[].message：兼容 reasoning_content（DeepSeek）
// 和 reasoning（SenseNova）。两种字段的回传语义不同：DeepSeek 在带 tools 时
// 要求原样回传 reasoning_content；SenseNova 的 reasoning 只用于展示。
// Content 兼容字符串与多模态数组两种响应形态，文本经 TextContent 提取。
type responseMessage struct {
	Role             string     `json:"role"`
	Content          any        `json:"content"`
	ToolCalls        []ToolCall `json:"tool_calls"`
	ReasoningContent string     `json:"reasoning_content"`
	Reasoning        string     `json:"reasoning"`
}

// Usage 用量统计（OpenAI usage 字段 + DeepSeek 缓存命中扩展）：
// hit/miss 用于观察 prompt cache 命中率，输出 token 是长任务成本大头。
type Usage struct {
	PromptTokens          int `json:"prompt_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	TotalTokens           int `json:"total_tokens"`
}

// ChatResponse chat/completions 响应体，仅解析用到的字段。
type ChatResponse struct {
	Choices []struct {
		Index        int             `json:"index"`
		Message      responseMessage `json:"message"`
		FinishReason string          `json:"finish_reason"`
	} `json:"choices"`
	Usage Usage `json:"usage"`
}

func (r *ChatResponse) FinishReason() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].FinishReason
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

// ReasoningContent 用于展示/日志：优先 reasoning_content，回退 reasoning。
func (r *ChatResponse) ReasoningContent() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	msg := r.Choices[0].Message
	if msg.ReasoningContent != "" {
		return msg.ReasoningContent
	}
	return msg.Reasoning
}

// ReplayReasoning 只返回需要随请求回传的 reasoning_content。
// SenseNova 的 reasoning 字段语义不同（仅展示），不能当作 reasoning_content
// 回传，否则会被对方拒绝或污染上下文。
func (r *ChatResponse) ReplayReasoning() string {
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
		return nil, &HTTPError{Status: httpResp.StatusCode, Body: truncateForLog(body)}
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

// HTTPError 模型接口返回非 200 时的错误（保留状态码供失败切换判断：
// 429/5xx 可换候选，4xx 参数/模型错误换候选也没用）。
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("agent: LLM 返回 HTTP %d: %s", e.Status, e.Body)
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
