package runtime

import (
	"errors"
	"fmt"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
)

type ContextChange struct {
	Action         string  `json:"action"`
	Index          int     `json:"index"`
	Text           *string `json:"text,omitempty"`
	ClearReasoning bool    `json:"clear_reasoning,omitempty"`
}

func coreMessage(message agentcontext.Message) bool {
	return message.Wire.Role == "system" || message.Wire.Role == "developer" || message.Kind == agentcontext.KindState
}

// EditableText 只提取可以无损替换的正文，图片和协议字段不进入编辑表单。
func EditableText(message agentcontext.Message) (string, bool) {
	if coreMessage(message) {
		return "", false
	}
	switch content := message.Wire.Content.(type) {
	case nil:
		return "", true
	case string:
		return content, true
	case []model.ContentPart:
		text, count := "", 0
		for _, part := range content {
			if part.Type == "text" {
				text = part.Text
				count++
			}
		}
		return text, count <= 1
	default:
		return "", false
	}
}

func toolBatch(messages []agentcontext.Message, index int) (int, int, error) {
	start := index
	if messages[index].Wire.Role == "tool" {
		for start > 0 && messages[start].Wire.Role == "tool" {
			start--
		}
	}
	calls := messages[start].Wire.ToolCalls
	if len(calls) == 0 {
		if messages[index].Wire.Role == "tool" {
			return 0, 0, errors.New("工具回执缺少对应调用")
		}
		return index, index + 1, nil
	}
	ids := make(map[string]bool, len(calls))
	for _, call := range calls {
		if call.ID == "" || ids[call.ID] {
			return 0, 0, errors.New("工具调用 ID 无效")
		}
		ids[call.ID] = true
	}
	end := start + 1
	for end < len(messages) && messages[end].Wire.Role == "tool" {
		id := messages[end].Wire.ToolCallID
		if !ids[id] {
			return 0, 0, errors.New("工具调用与回执不匹配")
		}
		delete(ids, id)
		end++
	}
	if len(ids) != 0 {
		return 0, 0, errors.New("工具调用尚无完整回执，不能删除")
	}
	return start, end, nil
}

// Change 创建一份候选副本；调用方可以先预览，再在版本校验后原子替换。
func (c *Conversation) Change(change ContextChange) (Conversation, []int, error) {
	next := *c
	next.Messages = append([]agentcontext.Message(nil), c.Messages...)
	remove := make(map[int]bool)
	latestState := -1
	for i, message := range c.Messages {
		if message.Kind == agentcontext.KindState {
			latestState = i
		}
	}
	switch change.Action {
	case "edit", "delete":
		if change.Index < 0 || change.Index >= len(c.Messages) {
			return next, nil, agentcontext.ErrInspectionIndex
		}
		message := c.Messages[change.Index]
		if coreMessage(message) {
			return next, nil, errors.New("提示词和状态由配置、笔记及情绪入口维护")
		}
		if change.Action == "delete" {
			start, end, err := toolBatch(c.Messages, change.Index)
			if err != nil {
				return next, nil, err
			}
			for i := start; i < end; i++ {
				remove[i] = true
			}
			break
		}
		if change.Text == nil && !change.ClearReasoning {
			return next, nil, errors.New("没有需要保存的修改")
		}
		if change.Text != nil {
			if _, ok := EditableText(message); !ok {
				return next, nil, errors.New("该正文结构无法无损编辑")
			}
			switch content := message.Wire.Content.(type) {
			case []model.ContentPart:
				parts := append([]model.ContentPart(nil), content...)
				found := false
				for i := range parts {
					if parts[i].Type == "text" {
						parts[i].Text = *change.Text
						found = true
					}
				}
				if !found {
					parts = append([]model.ContentPart{{Type: "text", Text: *change.Text}}, parts...)
				}
				message.Wire.Content = parts
			default:
				message.Wire.Content = *change.Text
			}
		}
		if change.ClearReasoning {
			message.Wire.ReasoningContent = ""
		}
		next.Messages[change.Index] = message
		return next, []int{change.Index}, nil
	case "clean", "reset":
		for i, message := range c.Messages {
			if message.Wire.Role == "system" || message.Wire.Role == "developer" {
				continue
			}
			if message.Kind == agentcontext.KindState {
				remove[i] = i != latestState
				continue
			}
			if change.Action == "reset" {
				remove[i] = true
				continue
			}
			if i >= c.UnreadStart || !CompactableWork(message) {
				continue
			}
			start, end, err := toolBatch(c.Messages, i)
			if err != nil || end > c.UnreadStart {
				continue
			}
			for j := start; j < end; j++ {
				if CompactableWork(c.Messages[j]) {
					remove[j] = true
				}
			}
		}
	default:
		return next, nil, fmt.Errorf("不支持的上下文操作 %q", change.Action)
	}
	var affected []int
	next.Messages = nil
	next.UnreadStart = 0
	for i, message := range c.Messages {
		if remove[i] {
			affected = append(affected, i)
			continue
		}
		next.Messages = append(next.Messages, message)
		if i < c.UnreadStart {
			next.UnreadStart++
		}
	}
	if change.Action == "reset" {
		next.Wakes = 0
		next.UnreadStart = len(next.Messages)
	}
	return next, affected, nil
}
