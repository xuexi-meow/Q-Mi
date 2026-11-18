package agent

import (
	"context"
	"time"

	"Q-Mi/agent/model"
	"Q-Mi/utils/config"
)

type ToolFunction = model.ToolFunction
type ToolCall = model.ToolCall
type ToolSchema = model.ToolSchema
type ToolDef = model.ToolDef
type ContentPart = model.ContentPart
type Usage = model.Usage
type HTTPError = model.HTTPError
type ChatMessage = model.ChatMessage
type StreamEvent = model.StreamEvent
type ChatRequest = model.ChatRequest
type responseMessage = model.ResponseMessage
type ChatResponse = model.ChatResponse

type Client struct{ inner *model.Client }

func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{inner: model.NewClient(baseURL, apiKey, timeout)}
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	return c.inner.ChatStream(ctx, req, nil)
}

func chatWithModelClient(ctx context.Context, client *model.Client, req ChatRequest) (*ChatResponse, error) {
	return client.ChatStream(ctx, req, nil)
}

func chatWithModelClientStream(ctx context.Context, client *model.Client, req ChatRequest, onEvent func(StreamEvent)) (*ChatResponse, error) {
	return client.ChatStream(ctx, req, onEvent)
}

func buildPool(cfg config.AgentConfig, legacy *Client) *model.Pool {
	var client *model.Client
	if legacy != nil {
		client = legacy.inner
	}
	return model.NewPool(cfg, client)
}

func needForMessages(messages []ChatMessage) model.Need    { return model.NeedForMessages(messages) }
func stripImageParts(messages []ChatMessage) []ChatMessage { return model.StripImageParts(messages) }
