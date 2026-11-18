package model

import "fmt"

func NeedForMessages(messages []ChatMessage) Need {
	need := Need{Tools: true}
	for _, message := range messages {
		parts, ok := message.Content.([]ContentPart)
		if !ok {
			continue
		}
		for _, part := range parts {
			if part.Type == "image_url" {
				need.Vision = true
				return need
			}
		}
	}
	return need
}

// StripImageParts 仅修改请求副本，保留工作上下文中的原图。
func StripImageParts(messages []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, len(messages))
	copy(out, messages)
	for i, message := range out {
		parts, ok := message.Content.([]ContentPart)
		if !ok {
			continue
		}
		kept := make([]ContentPart, 0, len(parts))
		images := 0
		for _, part := range parts {
			if part.Type == "image_url" {
				images++
				continue
			}
			kept = append(kept, part)
		}
		if images == 0 {
			continue
		}
		kept = append(kept, ContentPart{Type: "text", Text: fmt.Sprintf("（%d 张图片已省略）", images)})
		out[i].Content = kept
	}
	return out
}
