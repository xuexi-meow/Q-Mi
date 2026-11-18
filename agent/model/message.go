package model

// ToolFunction is one function invocation returned by a model.
type ToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolSchema struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters"`
}

type ToolDef struct {
	Type     string     `json:"type"`
	Function ToolSchema `json:"function"`
}

type ContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL *struct {
		URL string `json:"url"`
	} `json:"image_url,omitempty"`
}

type ChatMessage struct {
	Role             string     `json:"role"`
	Content          any        `json:"content"`
	ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string     `json:"tool_call_id,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
}

type ResponseFormat struct {
	Type string `json:"type"`
}

type ChatRequest struct {
	Model          string          `json:"model"`
	Messages       []ChatMessage   `json:"messages"`
	MaxTokens      int             `json:"max_tokens,omitempty"`
	Temperature    float64         `json:"temperature,omitempty"`
	Tools          []ToolDef       `json:"tools,omitempty"`
	ToolChoice     string          `json:"tool_choice,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

type ToolCallDelta struct {
	Index     int    `json:"index"`
	ID        string `json:"id,omitempty"`
	Type      string `json:"type,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type StreamEvent struct {
	ChoiceIndex    int             `json:"choice_index"`
	ContentDelta   string          `json:"content_delta,omitempty"`
	ReasoningDelta string          `json:"reasoning_delta,omitempty"`
	ToolCallDeltas []ToolCallDelta `json:"tool_call_deltas,omitempty"`
	Usage          *Usage          `json:"usage,omitempty"`
	FinishReason   *string         `json:"finish_reason,omitempty"`
}

type ResponseMessage struct {
	Role             string     `json:"role"`
	Content          any        `json:"content"`
	ToolCalls        []ToolCall `json:"tool_calls"`
	ReasoningContent string     `json:"reasoning_content"`
	Reasoning        string     `json:"reasoning"`
}

type Usage struct {
	PromptTokens          int `json:"prompt_tokens"`
	PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
	CompletionTokens      int `json:"completion_tokens"`
	TotalTokens           int `json:"total_tokens"`
}

type ChatResponse struct {
	Choices []struct {
		Index        int             `json:"index"`
		Message      ResponseMessage `json:"message"`
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

func (r *ChatResponse) TextContent() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	switch content := r.Choices[0].Message.Content.(type) {
	case string:
		return content
	case []any:
		var text string
		for _, item := range content {
			part, ok := item.(map[string]any)
			if !ok || part["type"] != "text" {
				continue
			}
			if value, ok := part["text"].(string); ok {
				text += value
			}
		}
		return text
	default:
		return ""
	}
}

func (r *ChatResponse) ToolCalls() []ToolCall {
	if r == nil || len(r.Choices) == 0 {
		return nil
	}
	return r.Choices[0].Message.ToolCalls
}

func (r *ChatResponse) ReasoningContent() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	message := r.Choices[0].Message
	if message.ReasoningContent != "" {
		return message.ReasoningContent
	}
	return message.Reasoning
}

func (r *ChatResponse) ReplayReasoning() string {
	if r == nil || len(r.Choices) == 0 {
		return ""
	}
	return r.Choices[0].Message.ReasoningContent
}
