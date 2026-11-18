package context

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"Q-Mi/storage"
)

const maxContextSenderNameRunes = 128

type messageSegment struct {
	Type string                     `json:"type"`
	Data map[string]json.RawMessage `json:"data"`
}

// MessageView renders a message as structured, untrusted conversation data.
func MessageView(store *storage.Store, selfID int64, message TurnMessage, origin string) map[string]any {
	kind := "qq_message"
	if message.Wake != "" && message.Wake != WakeChat {
		kind = "background_event"
	}
	row := map[string]any{
		"kind": kind, "origin": origin,
		"chat_type": message.ChatType, "chat_id": message.ChatID,
		"message_id": message.MessageID, "user_id": message.SenderID,
		"sender_name": truncateContextRunes(message.SenderName, maxContextSenderNameRunes),
		"is_self":     message.IsSelf || (selfID > 0 && message.SenderID == selfID),
		"timestamp":   message.Time.Format(time.RFC3339Nano),
		"text":        truncateContextContent(message.Text),
		"mentions":    nil, "mentions_self": false, "mentions_all": false,
		"addressing_known": false,
	}
	var segments []messageSegment
	if len(message.Segments) == 0 || json.Unmarshal(message.Segments, &segments) != nil || segments == nil {
		return row
	}
	mentions := make([]string, 0)
	var text strings.Builder
	hasText := false
	for _, segment := range segments {
		if segment.Type == "" || segment.Data == nil {
			return row
		}
		switch segment.Type {
		case "text":
			hasText = true
			text.WriteString(segmentString(segment.Data["text"]))
		case "at":
			target := segmentString(segment.Data["qq"])
			if target == "" {
				return row
			}
			mentions = append(mentions, target)
			text.WriteString("@" + target + " ")
		case "reply":
			id := segmentString(segment.Data["id"])
			if id == "" {
				return row
			}
			row["reply"] = scopedMessageReply(store, message.ChatType, message.ChatID, id, selfID)
		case "image":
			text.WriteString("[图片]")
		case "face":
			text.WriteString("[表情:" + segmentString(segment.Data["id"]) + "]")
		default:
			text.WriteString("[" + segment.Type + "]")
		}
	}
	row["mentions"], row["mentions_self"], row["mentions_all"] = mentions, false, false
	row["addressing_known"] = true
	for _, target := range mentions {
		if selfID > 0 && target == strconv.FormatInt(selfID, 10) {
			row["mentions_self"] = true
		}
		if target == "all" {
			row["mentions_all"] = true
		}
	}
	if !hasText && message.Text != "" {
		row["text"] = truncateContextContent(replaceStoredImageCQ(message.Text))
	} else {
		row["text"] = truncateContextContent(text.String())
	}
	return row
}

func segmentString(value json.RawMessage) string {
	var text string
	if json.Unmarshal(value, &text) == nil {
		return text
	}
	var number json.Number
	if json.Unmarshal(value, &number) == nil {
		return number.String()
	}
	return ""
}

func scopedMessageReply(store *storage.Store, chatType string, chatID int64, id string, selfID int64) map[string]any {
	reply := map[string]any{"message_id": id, "status": "unknown", "is_self": nil}
	messageID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || store == nil {
		return reply
	}
	record, err := store.GetMessage(messageID)
	if err != nil || record == nil || record.ChatType != chatType || record.ChatID != chatID {
		return reply
	}
	if record.Recalled {
		reply["status"] = "recalled"
		return reply
	}
	reply["status"] = "known"
	reply["user_id"] = record.UserID
	reply["is_self"] = record.Direction == "sent" || (selfID > 0 && record.UserID == selfID)
	reply["sender_name"] = truncateContextRunes(record.SenderName, maxContextSenderNameRunes)
	reply["text"] = truncateContextRunes(record.RawMessage, 500)
	return reply
}

func RecordTurnMessage(record *storage.MessageRecord) TurnMessage {
	return TurnMessage{
		MessageID: record.MessageID, ChatType: record.ChatType, ChatID: record.ChatID,
		SenderID: record.UserID, SenderName: record.SenderName, Text: record.RawMessage,
		Segments: record.Segments, IsSelf: record.Direction == "sent",
		Time: time.UnixMilli(int64(record.Time * 1000)),
	}
}

func truncateContextRunes(text string, max int) string {
	if utf8.RuneCountInString(text) <= max {
		return text
	}
	runes := []rune(text)
	return string(runes[:max])
}

func truncateContextContent(text string) string {
	runes := []rune(text)
	if len(runes) <= builderMaxContentRunes {
		return text
	}
	return string(runes[:builderMaxContentRunes]) + "...(截断)"
}
