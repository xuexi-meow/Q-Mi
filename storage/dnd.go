package storage

import (
	"fmt"
	"strings"
)

type ChatDND struct {
	ChatType string `json:"chat_type"`
	ChatID   int64  `json:"chat_id"`
}

func (s *Store) SetChatDND(chatType string, chatID int64, enabled bool) error {
	if (chatType != "private" && chatType != "group") || chatID <= 0 {
		return fmt.Errorf("chat_type 必须为 private/group，chat_id 必须为正数")
	}
	if enabled {
		_, err := s.db.Exec("INSERT INTO chat_dnd(chat_type, chat_id) VALUES (?, ?) ON CONFLICT DO NOTHING", chatType, chatID)
		return err
	}
	_, err := s.db.Exec("DELETE FROM chat_dnd WHERE chat_type = ? AND chat_id = ?", chatType, chatID)
	return err
}

func (s *Store) IsChatDND(chatType string, chatID int64) (bool, error) {
	var muted bool
	err := s.db.QueryRow("SELECT EXISTS(SELECT 1 FROM chat_dnd WHERE chat_type = ? AND chat_id = ?)", chatType, chatID).Scan(&muted)
	return muted, err
}

func (s *Store) ListChatDND() ([]ChatDND, error) {
	rows, err := s.db.Query("SELECT chat_type, chat_id FROM chat_dnd ORDER BY chat_type, chat_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chats := make([]ChatDND, 0)
	for rows.Next() {
		var c ChatDND
		if err := rows.Scan(&c.ChatType, &c.ChatID); err != nil {
			return nil, err
		}
		chats = append(chats, c)
	}
	return chats, rows.Err()
}

// AttentionFilter applies only to recent context, never to archive/search.
// Caller supplies the current account and administrator configuration.
type AttentionFilter struct {
	SelfID   int64
	AdminIDs []int64
}

func attentionWhere(f *AttentionFilter) (string, []any) {
	var args []any
	clauses := []string{
		"NOT EXISTS (SELECT 1 FROM chat_dnd d WHERE d.chat_type = messages.chat_type AND d.chat_id = messages.chat_id)",
		"direction = 'sent'",
	}
	if len(f.AdminIDs) > 0 {
		placeholders := make([]string, len(f.AdminIDs))
		for i, id := range f.AdminIDs {
			placeholders[i] = "?"
			args = append(args, id)
		}
		clauses = append(clauses, "user_id IN ("+strings.Join(placeholders, ",")+")")
	}
	// Old segment data can be malformed or valid JSON of the wrong shape.
	// Only actual at segments grant the mention exception.
	target := "json_extract(seg.value, '$.data.qq') = 'all'"
	if f.SelfID > 0 {
		target += " OR CAST(json_extract(seg.value, '$.data.qq') AS TEXT) = ?"
		args = append(args, fmt.Sprint(f.SelfID))
	}
	clauses = append(clauses, `EXISTS (
		SELECT 1 FROM json_each(
			CASE WHEN json_valid(segments) THEN
				CASE WHEN json_type(segments) = 'array' THEN segments ELSE '[]' END
			ELSE '[]' END
		) seg
		WHERE CASE WHEN seg.type = 'object' THEN
			json_extract(seg.value, '$.type') = 'at' AND (`+target+`)
		ELSE 0 END
	)`)
	return "(" + strings.Join(clauses, " OR ") + ")", args
}
