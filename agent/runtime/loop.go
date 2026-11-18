package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
	"Q-Mi/agent/tool"
)

type PreparedTurn struct {
	Turn     *agentcontext.Turn
	Request  model.ChatRequest
	Tools    []tool.Tool
	Executor *Executor
	NewInput bool
}

// LoopDriver connects request preparation, model routing, and inspection to
// the activity loop. It never grants the loop access to concrete tools.
type LoopDriver interface {
	Prepare(context.Context, *agentcontext.Turn, int) (PreparedTurn, error)
	Request(context.Context, PreparedTurn, int) (*model.ChatResponse, error)
	Observe(LoopEvent)
}

// ContextRecoveryDriver 在模型明确拒绝长度时尝试回收，不能重放任何工具。
type ContextRecoveryDriver interface {
	RecoverContext(context.Context, PreparedTurn, error) (bool, error)
}

type LoopEvent struct {
	Phase    string
	Round    int
	Prepared PreparedTurn
	Response *model.ChatResponse
	Results  []string
	Err      error
	Publish  bool
	Debug    bool
}

type Loop struct {
	Conversation *Conversation
	Driver       LoopDriver
	Events       *EventLog
	MaxRounds    int
}

func MeaningfulTurn(turn *agentcontext.Turn) bool {
	for _, msg := range turn.NewMessages {
		if msg.Wake != agentcontext.WakeBackground || msg.SenderName != "轮询" {
			return true
		}
	}
	return false
}

func appendWork(messages []agentcontext.Message, message model.ChatMessage) []agentcontext.Message {
	return append(messages, agentcontext.FromWire(message, agentcontext.KindWork))
}

func (l *Loop) Run(ctx context.Context, turn *agentcontext.Turn) error {
	maxRounds := l.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 10
	}
	counted := MeaningfulTurn(turn)
	protocolFailures := 0
	contextRecovered := false
	for round := 1; round <= maxRounds; round++ {
		l.Driver.Observe(LoopEvent{Phase: "preparing", Round: round})
		prepared, err := l.Driver.Prepare(ctx, turn, round)
		if err != nil {
			l.Events.Record(turn.ChatType, turn.ChatID, KindError, err.Error())
			return err
		}
		turn = prepared.Turn
		if prepared.NewInput {
			counted = MeaningfulTurn(turn)
		}
		response, err := l.Driver.Request(ctx, prepared, round)
		if err != nil && !contextRecovered {
			if recovery, ok := l.Driver.(ContextRecoveryDriver); ok {
				recovered, recoveryErr := recovery.RecoverContext(ctx, prepared, err)
				if recoveryErr != nil {
					err = recoveryErr
				}
				if recovered {
					contextRecovered = true
					prepared, err = l.Driver.Prepare(ctx, turn, round)
					if err == nil {
						turn = prepared.Turn
						if prepared.NewInput {
							counted = MeaningfulTurn(turn)
						}
						response, err = l.Driver.Request(ctx, prepared, round)
					}
				}
			}
		}
		if err != nil {
			l.Driver.Observe(LoopEvent{Round: round, Prepared: prepared, Err: err, Debug: true})
			l.Events.Record(turn.ChatType, turn.ChatID, KindError, err.Error())
			slog.Warn("LLM 请求失败，本轮思考结束",
				"chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round, "err", err)
			return err
		}
		l.Conversation.UnreadStart = len(prepared.Request.Messages)
		if reasoning := response.ReasoningContent(); reasoning != "" {
			slog.Info("思考", "chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round, "reasoning", reasoning)
			l.Events.Record(turn.ChatType, turn.ChatID, KindReasoning, reasoning)
		}
		if content := response.TextContent(); content != "" {
			slog.Info("输出", "chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round, "content", content)
			l.Events.Record(turn.ChatType, turn.ChatID, KindOutput, content)
		}
		if detail := responseProtocolError(response); detail != "" {
			protocolFailures++
			l.Driver.Observe(LoopEvent{Round: round, Prepared: prepared, Response: response, Debug: true})
			l.Conversation.Messages = appendWork(l.Conversation.Messages, model.ChatMessage{
				Role: "assistant", Content: response.TextContent(), ReasoningContent: response.ReplayReasoning(),
			})
			feedback, _ := marshalResult(map[string]any{
				"error": "tool_call_required", "sent": false, "detail": detail,
				"instruction": "没有消息发出。必须使用完整的 API tool_calls、唯一非空 id 和 function 类型，arguments 为 JSON 对象；不要在 content 中模拟调用。发言用 send_message，暂停用 finish({})。分析、草稿不得放入发送正文。",
			})
			l.Conversation.Messages = appendWork(l.Conversation.Messages, model.ChatMessage{Role: "user", Content: feedback})
			l.Driver.Observe(LoopEvent{Round: round, Prepared: prepared, Publish: true})
			l.Events.Record(turn.ChatType, turn.ChatID, KindError, "tool_call_required: 普通输出未发送")
			if protocolFailures >= 3 {
				return errors.New("连续三次工具调用协议错误")
			}
			continue
		}
		protocolFailures = 0
		calls := response.ToolCalls()
		if !counted {
			for _, call := range calls {
				if call.Function.Name != "finish" {
					l.Conversation.Wakes++
					counted = true
					break
				}
			}
		}
		l.Conversation.Messages = appendWork(l.Conversation.Messages, model.ChatMessage{
			Role: "assistant", Content: response.TextContent(), ToolCalls: calls,
			ReasoningContent: response.ReplayReasoning(),
		})
		l.Driver.Observe(LoopEvent{Phase: "tools", Round: round})
		results, finished := l.executeBatch(prepared, calls, round)
		for i, result := range results {
			l.Conversation.Messages = appendWork(l.Conversation.Messages,
				model.ChatMessage{Role: "tool", Content: result, ToolCallID: calls[i].ID})
		}
		if images := turn.PendingImages(); len(images) > 0 {
			parts := make([]model.ContentPart, 0, len(images)+1)
			parts = append(parts, model.ContentPart{Type: "text", Text: "以下图片来自上一轮工具结果（查询自动附带或你主动查看）"})
			for _, url := range images {
				parts = append(parts, model.ContentPart{Type: "image_url", ImageURL: &struct {
					URL string `json:"url"`
				}{URL: url}})
			}
			l.Conversation.Messages = appendWork(l.Conversation.Messages, model.ChatMessage{Role: "user", Content: parts})
			turn.ClearPendingImages()
		}
		l.Driver.Observe(LoopEvent{Round: round, Prepared: prepared, Response: response, Results: results, Debug: true, Publish: true})
		if finished {
			l.Events.Record(turn.ChatType, turn.ChatID, KindFinish, "结束思考")
			slog.Info("结束思考", "chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round)
			return nil
		}
	}
	l.Events.Record(turn.ChatType, turn.ChatID, KindError, "轮数上限")
	slog.Warn("思考轮数达到上限，强制结束本轮",
		"chat_type", turn.ChatType, "chat_id", turn.ChatID, "max_tool_rounds", maxRounds)
	return errors.New("轮数上限")
}

func (l *Loop) executeBatch(prepared PreparedTurn, calls []model.ToolCall, round int) ([]string, bool) {
	results := make([]string, len(calls))
	finished := false
	for _, call := range calls {
		if call.Function.Name == "sleep" && len(calls) != 1 {
			for i := range results {
				results[i] = ToolErrorJSON(errors.New("sleep 必须单独调用，本批工具均未执行；先完成其他行动，再单独休息"))
			}
			return results, false
		}
	}
	sleepSucceeded := false
	var workers sync.WaitGroup
	turn := prepared.Turn
	for i, call := range calls {
		if call.Function.Name == "finish" {
			result := "{}"
			if _, err := tool.ValidateArguments(tool.Tool{Name: "finish", Parameters: tool.ObjectParams(nil, nil)}, call.Function.Arguments); err != nil {
				result = ToolErrorJSON(err)
			} else {
				finished = true
			}
			results[i] = result
			continue
		}
		workers.Add(1)
		go func(i int, call model.ToolCall) {
			defer workers.Done()
			slog.Info("工具调用", "chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round,
				"tool", call.Function.Name, "args", call.Function.Arguments)
			l.Events.Record(turn.ChatType, turn.ChatID, KindToolCall, call.Function.Name+" "+call.Function.Arguments)
			result, err := prepared.Executor.Execute(prepared.Tools, call)
			if err != nil {
				slog.Info("工具执行失败", "chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round,
					"tool", call.Function.Name, "err", err)
				l.Events.Record(turn.ChatType, turn.ChatID, KindToolResult, fmt.Sprintf("%s: %s", call.Function.Name, err))
				result = ToolErrorJSON(err)
			} else if call.Function.Name == "sleep" {
				sleepSucceeded = true
			}
			results[i] = truncateToolResult(result)
		}(i, call)
	}
	workers.Wait()
	return results, finished || sleepSucceeded
}

func responseProtocolError(response *model.ChatResponse) string {
	if reason := response.FinishReason(); reason == "length" || reason == "content_filter" {
		return "response incomplete: finish_reason=" + reason
	}
	calls := response.ToolCalls()
	if len(calls) == 0 {
		return "missing tool_calls"
	}
	ids := make(map[string]bool, len(calls))
	for _, call := range calls {
		if call.ID == "" || ids[call.ID] || call.Type != "function" || call.Function.Name == "" {
			return "tool_calls contains missing/duplicate id, invalid type or empty function name"
		}
		ids[call.ID] = true
	}
	return ""
}

func truncateToolResult(result string) string {
	const maxRunes = 20000
	if len([]rune(result)) <= maxRunes {
		return result
	}
	encoded, _ := marshalResult(map[string]any{
		"truncated": true, "original_runes": len([]rune(result)),
		"preview": limitRunes(result, maxRunes/8),
		"note":    "仅为结果预览，不是完整数据；按相关会话、消息 ID 或更小范围重新查询。",
	})
	return encoded
}
