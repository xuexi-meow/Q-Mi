package agent

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"Q-Mi/storage"
)

type contextSegment struct {
	Type string                     `json:"type"`
	Data map[string]json.RawMessage `json:"data"`
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

// messageView is shared by live inputs and archive tools. JSON escaping keeps
// user-authored nicknames/text from forging the surrounding source metadata.
func messageView(store *storage.Store, selfID int64, message TurnMessage, origin string) map[string]any {
	kind := "qq_message"
	if message.Wake != "" && message.Wake != WakeChat {
		kind = "background_event"
	}
	row := map[string]any{
		"kind": kind, "origin": origin,
		"chat_type": message.ChatType, "chat_id": message.ChatID,
		"message_id": message.MessageID, "user_id": message.SenderID,
		"sender_name": truncateRunes(message.SenderName, maxQuerySenderNameRunes),
		"is_self":     message.IsSelf || (selfID > 0 && message.SenderID == selfID),
		"timestamp":   message.Time.Format(time.RFC3339Nano),
		"text":        truncateContent(message.Text),
	}
	row["mentions"], row["mentions_self"], row["mentions_all"] = nil, false, false
	row["addressing_known"] = false
	var segments []contextSegment
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
			row["reply"] = scopedReply(store, message.ChatType, message.ChatID, id, selfID)
		case "image":
			text.WriteString("[图片]")
		case "face":
			text.WriteString("[表情:" + segmentString(segment.Data["id"]) + "]")
		case "":
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
		// Older archives may contain only media/mention segments, with the
		// remaining caption present exclusively in raw_message.
		row["text"] = truncateContent(replaceImageCQ(message.Text))
	} else {
		row["text"] = truncateContent(text.String())
	}
	return row
}

func scopedReply(store *storage.Store, chatType string, chatID int64, id string, selfID int64) map[string]any {
	reply := map[string]any{"message_id": id, "status": "unknown", "is_self": nil}
	messageID, err := strconv.ParseInt(id, 10, 64)
	if err != nil || store == nil {
		return reply
	}
	record, err := store.GetMessage(messageID)
	// Missing or cross-chat references reveal neither contents nor attribution.
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
	reply["sender_name"] = truncateRunes(record.SenderName, maxQuerySenderNameRunes)
	reply["text"] = truncateRunes(record.RawMessage, 500)
	return reply
}

func recordTurnMessage(record *storage.MessageRecord) TurnMessage {
	return TurnMessage{
		MessageID: record.MessageID, ChatType: record.ChatType, ChatID: record.ChatID,
		SenderID: record.UserID, SenderName: record.SenderName, Text: record.RawMessage,
		Segments: record.Segments, IsSelf: record.Direction == "sent",
		Time: time.UnixMilli(int64(record.Time * 1000)),
	}
}
