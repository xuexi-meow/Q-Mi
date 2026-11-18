package agent

import (
	"Q-Mi/agent/context"
	"Q-Mi/storage"
	"encoding/json"
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

// messageView keeps the legacy agent entry point while context owns rendering.
func messageView(store *storage.Store, selfID int64, message TurnMessage, origin string) map[string]any {
	return context.MessageView(store, selfID, message, origin)
}

func recordTurnMessage(record *storage.MessageRecord) TurnMessage {
	return context.RecordTurnMessage(record)
}
