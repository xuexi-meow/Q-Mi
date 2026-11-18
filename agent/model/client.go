package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const (
	DefaultRequestTimeout = 120 * time.Second
	streamIdleTimeout     = 30 * time.Second
	maxSSEFrameBytes      = 1 << 20
)

var ErrStreamIdleTimeout = errors.New("agent: LLM 流连续 30 秒无数据")

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func NewClient(baseURL, apiKey string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = DefaultRequestTimeout
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
	}
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	return c.ChatStream(ctx, req, nil)
}

type streamRequest struct {
	ChatRequest
	Stream bool `json:"stream"`
}

func (c *Client) ChatStream(ctx context.Context, req ChatRequest, onEvent func(StreamEvent)) (*ChatResponse, error) {
	payload, err := json.Marshal(streamRequest{ChatRequest: req, Stream: true})
	if err != nil {
		return nil, fmt.Errorf("agent: 编码请求失败: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("agent: 构造请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("agent: 请求 LLM 失败: %w", err)
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < http.StatusOK || httpResp.StatusCode >= http.StatusMultipleChoices {
		body, readErr := io.ReadAll(io.LimitReader(httpResp.Body, 513))
		if readErr != nil {
			return nil, fmt.Errorf("agent: 读取错误响应失败: %w", readErr)
		}
		return nil, &HTTPError{Status: httpResp.StatusCode, Body: truncateForLog(body)}
	}

	mediaType, _, _ := mime.ParseMediaType(httpResp.Header.Get("Content-Type"))
	if mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") {
		response, err := readJSONResponse(httpResp.Body)
		if err != nil {
			return nil, err
		}
		emitFullResponse(response, onEvent)
		return response, nil
	}
	if mediaType != "text/event-stream" {
		reader := bufio.NewReader(httpResp.Body)
		first, peekErr := firstNonWhitespace(reader)
		if peekErr == nil && (first == '{' || first == '[') {
			response, err := readJSONResponse(reader)
			if err != nil {
				return nil, err
			}
			emitFullResponse(response, onEvent)
			return response, nil
		}
	}
	if mediaType != "text/event-stream" {
		return nil, fmt.Errorf("agent: 不支持的流响应类型 %q", mediaType)
	}

	body := &idleTimeoutReader{
		body:    httpResp.Body,
		timeout: streamIdleTimeout,
	}
	response, err := readChatStream(body, onEvent)
	if err != nil {
		if errors.Is(err, ErrStreamIdleTimeout) {
			return nil, err
		}
		return nil, fmt.Errorf("agent: 读取模型流失败: %w", err)
	}
	return response, nil
}

func readJSONResponse(body io.Reader) (*ChatResponse, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("agent: 读取响应失败: %w", err)
	}
	var response ChatResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("agent: 解析响应 JSON 失败: %w (body=%s)", err, truncateForLog(data))
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("agent: 响应中没有 choices (body=%s)", truncateForLog(data))
	}
	return &response, nil
}

func firstNonWhitespace(reader *bufio.Reader) (byte, error) {
	for {
		value, err := reader.Peek(1)
		if err != nil {
			return 0, err
		}
		if value[0] != ' ' && value[0] != '\t' && value[0] != '\r' && value[0] != '\n' {
			return value[0], nil
		}
		_, _ = reader.ReadByte()
	}
}

type streamChunk struct {
	Choices []struct {
		Index        int    `json:"index"`
		FinishReason string `json:"finish_reason"`
		Delta        struct {
			Content          any    `json:"content"`
			ReasoningContent string `json:"reasoning_content"`
			Reasoning        string `json:"reasoning"`
			ToolCalls        []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *Usage `json:"usage"`
}

type assembledChoice struct {
	index        int
	message      ResponseMessage
	finishReason string
	toolCalls    map[int]*ToolCall
}

func readChatStream(body io.Reader, onEvent func(StreamEvent)) (*ChatResponse, error) {
	reader := bufio.NewReaderSize(body, 16*1024)
	choices := make(map[int]*assembledChoice)
	done := false
	var usage Usage

	for {
		data, err := readSSEFrame(reader)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if string(data) == "[DONE]" {
			done = true
			break
		}

		var chunk streamChunk
		if err := json.Unmarshal(data, &chunk); err != nil {
			return nil, fmt.Errorf("SSE JSON 解码失败: %w", err)
		}
		if chunk.Usage != nil && onEvent != nil {
			onEvent(StreamEvent{Usage: chunk.Usage})
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		for _, choiceDelta := range chunk.Choices {
			choice := choices[choiceDelta.Index]
			if choice == nil {
				choice = &assembledChoice{index: choiceDelta.Index, toolCalls: make(map[int]*ToolCall)}
				choices[choiceDelta.Index] = choice
			}

			event := StreamEvent{ChoiceIndex: choiceDelta.Index}
			if text, ok := choiceDelta.Delta.Content.(string); ok {
				event.ContentDelta = text
				choice.message.Content = stringValue(choice.message.Content) + text
			}
			reasoning := choiceDelta.Delta.ReasoningContent
			if reasoning == "" {
				reasoning = choiceDelta.Delta.Reasoning
			}
			event.ReasoningDelta = reasoning
			choice.message.ReasoningContent += reasoning

			for _, toolDelta := range choiceDelta.Delta.ToolCalls {
				call := choice.toolCalls[toolDelta.Index]
				if call == nil {
					call = &ToolCall{}
					choice.toolCalls[toolDelta.Index] = call
				}
				if toolDelta.ID != "" {
					call.ID = toolDelta.ID
				}
				if toolDelta.Type != "" {
					call.Type = toolDelta.Type
				}
				if toolDelta.Function.Name != "" {
					call.Function.Name += toolDelta.Function.Name
				}
				call.Function.Arguments += toolDelta.Function.Arguments
				event.ToolCallDeltas = append(event.ToolCallDeltas, ToolCallDelta{
					Index: toolDelta.Index, ID: toolDelta.ID, Type: toolDelta.Type,
					Name: toolDelta.Function.Name, Arguments: toolDelta.Function.Arguments,
				})
			}
			if choiceDelta.FinishReason != "" {
				choice.finishReason = choiceDelta.FinishReason
				finishReason := choiceDelta.FinishReason
				event.FinishReason = &finishReason
			}
			if onEvent != nil && (event.ContentDelta != "" || event.ReasoningDelta != "" ||
				len(event.ToolCallDeltas) > 0 || event.FinishReason != nil) {
				onEvent(event)
			}
		}
	}

	if len(choices) == 0 {
		return nil, errors.New("SSE 响应中没有 choices")
	}
	for _, choice := range choices {
		if choice.finishReason == "" {
			if done {
				return nil, errors.New("SSE [DONE] 前没有 choice finish_reason")
			}
			return nil, errors.New("SSE 在 choice 完成前结束")
		}
	}

	indexes := make([]int, 0, len(choices))
	for index := range choices {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	response := &ChatResponse{Usage: usage}
	for _, index := range indexes {
		choice := choices[index]
		toolIndexes := make([]int, 0, len(choice.toolCalls))
		for toolIndex := range choice.toolCalls {
			toolIndexes = append(toolIndexes, toolIndex)
		}
		sort.Ints(toolIndexes)
		for _, toolIndex := range toolIndexes {
			choice.message.ToolCalls = append(choice.message.ToolCalls, *choice.toolCalls[toolIndex])
		}
		response.Choices = append(response.Choices, struct {
			Index        int             `json:"index"`
			Message      ResponseMessage `json:"message"`
			FinishReason string          `json:"finish_reason"`
		}{Index: choice.index, Message: choice.message, FinishReason: choice.finishReason})
	}
	return response, nil
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func emitFullResponse(response *ChatResponse, onEvent func(StreamEvent)) {
	if onEvent == nil || response == nil {
		return
	}
	for _, choice := range response.Choices {
		event := StreamEvent{
			ChoiceIndex: choice.Index, ContentDelta: stringValue(choice.Message.Content),
			ReasoningDelta: choice.Message.ReasoningContent,
		}
		if event.ReasoningDelta == "" {
			event.ReasoningDelta = choice.Message.Reasoning
		}
		for index, call := range choice.Message.ToolCalls {
			event.ToolCallDeltas = append(event.ToolCallDeltas, ToolCallDelta{
				Index: index, ID: call.ID, Type: call.Type,
				Name: call.Function.Name, Arguments: call.Function.Arguments,
			})
		}
		finishReason := choice.FinishReason
		event.FinishReason = &finishReason
		onEvent(event)
	}
	if response.Usage != (Usage{}) {
		onEvent(StreamEvent{Usage: &response.Usage})
	}
}

func readSSEFrame(reader *bufio.Reader) ([]byte, error) {
	frameBytes := 0
	var dataLines [][]byte
	for {
		line, err := readSSELine(reader)
		if err != nil {
			if errors.Is(err, io.EOF) && frameBytes == 0 && len(dataLines) == 0 {
				return nil, io.EOF
			}
			if errors.Is(err, io.EOF) {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
		frameBytes += len(line) + 1
		if frameBytes > maxSSEFrameBytes {
			return nil, errors.New("SSE 帧超过 1 MiB")
		}
		if len(line) == 0 {
			if len(dataLines) == 0 {
				frameBytes = 0
				continue
			}
			return bytes.Join(dataLines, []byte("\n")), nil
		}
		if line[0] == ':' {
			continue
		}
		field, value, hasColon := bytes.Cut(line, []byte(":"))
		if !hasColon {
			value = nil
		} else if len(value) > 0 && value[0] == ' ' {
			value = value[1:]
		}
		if bytes.Equal(field, []byte("data")) {
			dataLines = append(dataLines, append([]byte(nil), value...))
		}
	}
}

func readSSELine(reader *bufio.Reader) ([]byte, error) {
	line := make([]byte, 0, 256)
	for {
		part, err := reader.ReadSlice('\n')
		if len(line)+len(part) > maxSSEFrameBytes {
			return nil, errors.New("SSE 帧超过 1 MiB")
		}
		line = append(line, part...)
		if err == nil {
			line = bytes.TrimSuffix(line, []byte("\n"))
			line = bytes.TrimSuffix(line, []byte("\r"))
			return line, nil
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			if errors.Is(err, io.EOF) && len(line) > 0 {
				return nil, io.ErrUnexpectedEOF
			}
			return nil, err
		}
	}
}

type idleTimeoutReader struct {
	body    io.ReadCloser
	timeout time.Duration
}

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	var timedOut atomic.Bool
	finished := make(chan struct{})
	timer := time.AfterFunc(r.timeout, func() {
		timedOut.Store(true)
		_ = r.body.Close()
		close(finished)
	})
	n, err := r.body.Read(p)
	if !timer.Stop() {
		<-finished
	}
	if timedOut.Load() {
		return n, ErrStreamIdleTimeout
	}
	return n, err
}

type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("agent: LLM 返回 HTTP %d: %s", e.Status, e.Body)
}

func truncateForLog(body []byte) string {
	const limit = 512
	text := string(body)
	if len(text) > limit {
		return text[:limit] + "...(truncated)"
	}
	return text
}
