package model

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

type oneByteReader struct {
	reader io.Reader
}

func (r oneByteReader) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.reader.Read(p)
}

func TestChatStreamEmitsAndAggregatesDeltas(t *testing.T) {
	var request map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		frames := []string{
			`data: {"choices":[{"index":0,"delta":{"content":"你好","reasoning_content":"先确认"}}]}` + "\n\n",
			`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{\"q\":"}}]}}]}` + "\n\n",
			`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"猫\"}"}}]},"finish_reason":"tool_calls"}]}` + "\n\n",
			"data: [DONE]\n\n",
		}
		for _, frame := range frames {
			if _, err := fmt.Fprint(w, frame); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "key", time.Second)
	var events []StreamEvent
	response, err := client.ChatStream(context.Background(), ChatRequest{
		Model: "test-model", Messages: []ChatMessage{{Role: "user", Content: "查一下"}},
	}, func(event StreamEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if request["stream"] != true {
		t.Fatalf("request stream = %#v, want true", request["stream"])
	}
	if response.TextContent() != "你好" || response.ReasoningContent() != "先确认" {
		t.Fatalf("aggregated response = content %q, reasoning %q", response.TextContent(), response.ReasoningContent())
	}
	calls := response.ToolCalls()
	if len(calls) != 1 || calls[0].ID != "call-1" || calls[0].Type != "function" ||
		calls[0].Function.Name != "lookup" || calls[0].Function.Arguments != `{"q":"猫"}` {
		t.Fatalf("aggregated tool calls = %#v", calls)
	}
	if len(events) != 3 ||
		events[0].ContentDelta != "你好" || events[0].ReasoningDelta != "先确认" ||
		len(events[1].ToolCallDeltas) != 1 || events[1].ToolCallDeltas[0].Arguments != `{"q":` ||
		events[2].FinishReason == nil || *events[2].FinishReason != "tool_calls" {
		t.Fatalf("stream events = %#v", events)
	}

	choice := response.Choices[0]
	if choice.FinishReason != "tool_calls" {
		t.Errorf("finish reason = %q, want tool_calls", choice.FinishReason)
	}
	if got := []int{events[0].ChoiceIndex, events[1].ChoiceIndex, events[2].ChoiceIndex}; !reflect.DeepEqual(got, []int{0, 0, 0}) {
		t.Errorf("choice indexes = %v, want [0 0 0]", got)
	}
}

func TestReadSSEFrameHandlesCRLFMultilineAndSplitUTF8(t *testing.T) {
	input := ": heartbeat\r\ndata: {\r\ndata: \"text\":\"猫 🐈\"}\r\n\r\n"
	frame, err := readSSEFrame(bufio.NewReader(oneByteReader{reader: bytes.NewBufferString(input)}))
	if err != nil {
		t.Fatalf("readSSEFrame: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal(frame, &decoded); err != nil {
		t.Fatalf("decode joined data lines %q: %v", frame, err)
	}
	if decoded["text"] != "猫 🐈" {
		t.Fatalf("decoded text = %q", decoded["text"])
	}
}

func TestReadChatStreamRejectsChoiceWithoutFinishReason(t *testing.T) {
	input := `data: {"choices":[{"index":0,"delta":{"content":"partial"}}]}` + "\n\n"
	response, err := readChatStream(bytes.NewBufferString(input), nil)
	if err == nil || response != nil || !strings.Contains(err.Error(), "choice 完成前结束") {
		t.Fatalf("readChatStream = (%#v, %v), want incomplete choice error", response, err)
	}
}

func TestReadSSEFrameRejectsFrameOverOneMiB(t *testing.T) {
	input := "data: " + strings.Repeat("x", maxSSEFrameBytes) + "\n\n"
	_, err := readSSEFrame(bufio.NewReader(strings.NewReader(input)))
	if err == nil || !strings.Contains(err.Error(), "超过 1 MiB") {
		t.Fatalf("readSSEFrame error = %v, want oversized frame error", err)
	}
}

func TestChatStreamAggregatesUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w,
			`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`+"\n\n"+
				`data: {"choices":[],"usage":{"prompt_tokens":8,"completion_tokens":3,"total_tokens":11}}`+"\n\n"+
				"data: [DONE]\n\n")
	}))
	defer server.Close()

	response, err := NewClient(server.URL, "key", time.Second).ChatStream(context.Background(), ChatRequest{}, nil)
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if response.Usage != (Usage{PromptTokens: 8, CompletionTokens: 3, TotalTokens: 11}) {
		t.Fatalf("usage = %+v", response.Usage)
	}
}

func TestChatStreamFallsBackToJSONWithoutContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"json fallback"}}]}`)
	}))
	defer server.Close()

	var events []StreamEvent
	response, err := NewClient(server.URL, "key", time.Second).ChatStream(context.Background(), ChatRequest{}, func(event StreamEvent) {
		events = append(events, event)
	})
	if err != nil {
		t.Fatalf("ChatStream: %v", err)
	}
	if response.TextContent() != "json fallback" || len(events) != 1 || events[0].ContentDelta != "json fallback" {
		t.Fatalf("response=%q events=%#v", response.TextContent(), events)
	}
}

func TestChatStreamBoundsHTTPErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprint(w, strings.Repeat("x", 1024))
	}))
	defer server.Close()

	_, err := NewClient(server.URL, "key", time.Second).ChatStream(context.Background(), ChatRequest{}, nil)
	var httpErr *HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("error = %v, want HTTPError", err)
	}
	if len(httpErr.Body) > 530 || !strings.HasSuffix(httpErr.Body, "...(truncated)") {
		t.Fatalf("HTTP error body length=%d, suffix=%q", len(httpErr.Body), httpErr.Body[max(0, len(httpErr.Body)-20):])
	}
}

func TestIdleTimeoutReaderInterruptsBlockedRead(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	body := &pipeReadCloser{reader: reader}
	stream := &idleTimeoutReader{body: body, timeout: 10 * time.Millisecond}
	_, err := stream.Read(make([]byte, 1))
	if !errors.Is(err, ErrStreamIdleTimeout) {
		t.Fatalf("Read error = %v, want idle timeout", err)
	}
}

type pipeReadCloser struct {
	reader *io.PipeReader
}

func (r *pipeReadCloser) Read(p []byte) (int, error) {
	return r.reader.Read(p)
}

func (r *pipeReadCloser) Close() error {
	return r.reader.Close()
}
