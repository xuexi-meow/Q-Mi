package agent

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"Q-Mi/storage"
)

const (
	maxAutoAttachImages     = 5
	maxQueryMessageRunes    = 1000
	maxQuerySenderNameRunes = 128
	maxMessageResultRunes   = 12000
)

type messageQueryResult struct {
	Messages  []map[string]any `json:"messages"`
	Returned  int              `json:"returned"`
	Omitted   int              `json:"omitted"`
	Truncated bool             `json:"truncated"`
}

// queryMessageRows bounds complete JSON before downloading images. Omitted
// counts only records removed from the selected window, not all stored matches.
func queryMessageRows(store *storage.Store, images *imageStore, turn *Turn, f storage.MessageFilter, keyword ...string) (string, error) {
	if store == nil {
		return "", fmt.Errorf("记忆库未启用，无法查询消息")
	}
	var (
		records []*storage.MessageRecord
		err     error
	)
	if len(keyword) > 0 {
		records, err = store.SearchMessages(keyword[0], f)
	} else {
		records, err = store.QueryMessages(f)
	}
	if err != nil {
		return "", fmt.Errorf("查询消息失败: %w", err)
	}
	result := messageQueryResult{
		Messages: make([]map[string]any, 0, len(records)),
		Omitted:  len(records), Truncated: len(records) > 0,
	}
	partial := false
	for _, r := range records { // Select newest first; reverse only after budgeting.
		var selfID int64
		if turn != nil {
			selfID = turn.SelfID
		}
		row := messageView(store, selfID, recordTurnMessage(r), "archive")
		row["time"] = time.Unix(int64(r.Time), 0).Format(timeLayout)
		rowPartial := false
		text, _ := row["text"].(string)
		if length := utf8.RuneCountInString(text); length > maxQueryMessageRunes {
			row["text"] = string([]rune(text)[:maxQueryMessageRunes])
			row["text_truncated"], row["text_length"] = true, length
			rowPartial = true
		}
		if utf8.RuneCountInString(r.SenderName) > maxQuerySenderNameRunes {
			row["sender_name"] = string([]rune(r.SenderName)[:maxQuerySenderNameRunes])
			row["sender_name_truncated"] = true
			rowPartial = true
		}
		if refs := extractImageRefs(r.Segments); len(refs) > 0 {
			row["images"] = len(refs)
			if turn != nil && images != nil {
				// Reserve the field now; changing false to true only shortens JSON.
				row["images_attached"] = false
			}
		}
		candidate := messageQueryResult{
			Messages: append(result.Messages, row),
			Returned: result.Returned + 1,
			Omitted:  len(records) - result.Returned - 1,
		}
		candidate.Truncated = partial || rowPartial || candidate.Omitted > 0
		encoded, err := marshalCompact(candidate)
		if err != nil {
			return "", err
		}
		if utf8.RuneCountInString(encoded) > maxMessageResultRunes {
			break
		}
		result = candidate
		partial = partial || rowPartial
	}
	for left, right := 0, len(result.Messages)-1; left < right; left, right = left+1, right-1 {
		result.Messages[left], result.Messages[right] = result.Messages[right], result.Messages[left]
	}
	if turn != nil && images != nil {
		attached := 0
		for i, r := range records[:result.Returned] {
			if attached >= maxAutoAttachImages {
				break
			}
			row := result.Messages[result.Returned-1-i]
			for _, ref := range extractImageRefs(r.Segments) {
				if attached >= maxAutoAttachImages {
					break
				}
				du := images.dataURLFor(r.MessageID, ref.Idx, ref.URL)
				if !strings.HasPrefix(du, "data:") {
					continue
				}
				turn.AddPendingImage(du)
				row["images_attached"] = true
				attached++
			}
		}
	}
	return marshalCompact(result)
}
