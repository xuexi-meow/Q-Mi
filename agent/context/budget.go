package context

import (
	"encoding/json"
	"fmt"
	"strings"

	"Q-Mi/agent/model"
)

// Text is estimated at three ASCII bytes or half a non-ASCII rune per token.
// Image cost is an allowance, not the model's vision tokenizer.
func TextTokens(text string) int {
	units := 0
	for _, r := range text {
		if r < 128 {
			units++
		} else {
			units += 6
		}
	}
	return (units + 2) / 3
}

func LimitTextTokens(text string, budget int) string {
	if TextTokens(text) <= budget {
		return text
	}
	if budget <= 16 {
		return ""
	}
	left := (budget - 16) * 3
	for i, r := range text {
		cost := 1
		if r >= 128 {
			cost = 6
		}
		if left < cost {
			return text[:i] + "...(截断)"
		}
		left -= cost
	}
	return text
}

func ContentTextAndImages(content any) (string, int) {
	switch value := content.(type) {
	case string:
		return value, 0
	case []model.ContentPart:
		var text strings.Builder
		images := 0
		for _, part := range value {
			if part.Type == "text" {
				text.WriteString(part.Text)
			} else if part.Type == "image_url" {
				images++
			}
		}
		return text.String(), images
	case []any:
		var parts []model.ContentPart
		data, _ := json.Marshal(value)
		_ = json.Unmarshal(data, &parts)
		return ContentTextAndImages(parts)
	default:
		data, _ := json.Marshal(content)
		return string(data), 0
	}
}

func EstimatedInputTokens(messages []model.ChatMessage, defs []model.ToolDef) int {
	total := 8
	for _, message := range messages {
		text, images := ContentTextAndImages(message.Content)
		total += 8 + TextTokens(text) + images*2048
		// thinking 模式要求 reasoning_content 随请求回传，按实际占用计入预算。
		total += TextTokens(message.ReasoningContent)
		if len(message.ToolCalls) > 0 {
			data, _ := json.Marshal(message.ToolCalls)
			total += TextTokens(string(data))
		}
	}
	if len(defs) > 0 {
		data, _ := json.Marshal(defs)
		total += 16 + TextTokens(string(data))
	}
	return total
}

func SafeCut(messages []Message, cut int) int {
	for cut > 0 && cut < len(messages) && messages[cut].Wire.Role == "tool" {
		cut--
	}
	return cut
}

func SummaryTranscript(messages []model.ChatMessage, budget int) string {
	var transcript strings.Builder
	perMessage := budget/(len(messages)+1) - 24
	if perMessage > 2048 {
		perMessage = 2048
	}
	if perMessage < 16 {
		perMessage = 16
	}
	for _, message := range messages {
		text, images := ContentTextAndImages(message.Content)
		if len(message.ToolCalls) > 0 {
			data, _ := json.Marshal(message.ToolCalls)
			text = string(data) + "\n" + text
		}
		fmt.Fprintf(&transcript, "\n%s %s: %s", message.Role, message.ToolCallID, LimitTextTokens(text, perMessage))
		if images > 0 {
			fmt.Fprintf(&transcript, " [省略%d张图片，需按消息来源重新查看]", images)
		}
	}
	return LimitTextTokens(transcript.String(), budget)
}
