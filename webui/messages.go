package webui

import (
	"encoding/json"
	"strconv"

	"Q-Mi/storage"
)

type messageSegment struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	Target string `json:"target,omitempty"`
}

func messageScalar(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return ""
}

func addMessageMetadata(row *messageJSON, record *storage.MessageRecord) {
	row.Mentions, row.Segments = []string{}, []messageSegment{}
	var segments []struct {
		Type string                     `json:"type"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(record.Segments, &segments) != nil || segments == nil {
		return
	}
	for _, seg := range segments {
		if seg.Type == "" || seg.Data == nil {
			return
		}
		switch seg.Type {
		case "text":
			row.Segments = append(row.Segments, messageSegment{Type: "text", Text: messageScalar(seg.Data["text"])})
		case "at":
			target := messageScalar(seg.Data["qq"])
			if target == "" {
				return
			}
			row.Mentions = append(row.Mentions, target)
			row.Segments = append(row.Segments, messageSegment{Type: "at", Target: target})
		case "reply":
			target := messageScalar(seg.Data["id"])
			if target == "" {
				return
			}
			row.Segments = append(row.Segments, messageSegment{Type: "reply", Target: target})
			row.Reply = map[string]any{"message_id": target, "status": "unknown"}
			id, err := strconv.ParseInt(target, 10, 64)
			if store := storeProvider(); store != nil && err == nil {
				quoted, err := store.GetMessage(id)
				if err == nil && quoted != nil && quoted.ChatType == record.ChatType && quoted.ChatID == record.ChatID && !quoted.Recalled {
					row.Reply["status"], row.Reply["user_id"], row.Reply["sender_name"] = "known", strconv.FormatInt(quoted.UserID, 10), quoted.SenderName
				}
			}
		default:
			row.Segments = append(row.Segments, messageSegment{Type: seg.Type})
		}
	}
	row.AddressingKnown = true
}
