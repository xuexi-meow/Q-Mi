package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// MessageRecord messages 表的一行
type MessageRecord struct {
	MessageID  int64
	Direction  string // "recv" / "sent"
	ChatType   string // "private" / "group"
	ChatID     int64  // 群号;私聊 = 对方 QQ
	UserID     int64  // 发送者;发出 = 机器人自身
	RawMessage string
	Segments   []byte // 原始消息段 JSON
	Time       float64
	Recalled   bool
}

const messageCols = "message_id, direction, chat_type, chat_id, user_id, raw_message, segments, time, recalled"

func (s *Store) PutMessage(r *MessageRecord) error {
	recalled := 0
	if r.Recalled {
		recalled = 1
	}
	_, err := s.db.Exec(`
INSERT INTO messages (message_id, direction, chat_type, chat_id, user_id, raw_message, segments, time, recalled)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(message_id) DO UPDATE SET
    direction=excluded.direction, chat_type=excluded.chat_type, chat_id=excluded.chat_id,
    user_id=excluded.user_id, raw_message=excluded.raw_message, segments=excluded.segments,
    time=excluded.time, recalled=excluded.recalled`,
		r.MessageID, r.Direction, r.ChatType, r.ChatID, r.UserID, r.RawMessage, string(r.Segments), r.Time, recalled)
	if err != nil {
		return fmt.Errorf("写入消息失败: %w", err)
	}
	return nil
}

func (s *Store) GetMessage(id int64) (*MessageRecord, error) {
	row := s.db.QueryRow("SELECT "+messageCols+" FROM messages WHERE message_id = ?", id)
	r, err := scanMessage(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("查询消息 %d 失败: %w", id, err)
	}
	return r, nil
}

// scanner 抽象 *sql.Row 与 *sql.Rows,共用一个 scan 逻辑
type scanner interface{ Scan(dest ...any) error }

func scanMessage(row scanner) (*MessageRecord, error) {
	var r MessageRecord
	var segments []byte
	var recalled int
	if err := row.Scan(&r.MessageID, &r.Direction, &r.ChatType, &r.ChatID, &r.UserID,
		&r.RawMessage, &segments, &r.Time, &recalled); err != nil {
		return nil, err
	}
	r.Segments = segments
	r.Recalled = recalled != 0
	return &r, nil
}

// RecentMessages 某 chat 最近 N 条,按时间倒序,已撤回默认排除
func (s *Store) RecentMessages(chatType string, chatID int64, limit int) ([]*MessageRecord, error) {
	return s.QueryMessages(MessageFilter{ChatType: chatType, ChatID: chatID, Limit: limit})
}

// MessageFilter 查询过滤器,零值字段表示不过滤
type MessageFilter struct {
	ChatType        string
	ChatID, UserID  int64
	Since, Until    time.Time
	IncludeRecalled bool
	Limit           int
}

func (s *Store) QueryMessages(f MessageFilter) ([]*MessageRecord, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	where := []string{"1=1"}
	var args []any
	if f.ChatType != "" {
		where = append(where, "chat_type = ?")
		args = append(args, f.ChatType)
	}
	if f.ChatID != 0 {
		where = append(where, "chat_id = ?")
		args = append(args, f.ChatID)
	}
	if f.UserID != 0 {
		where = append(where, "user_id = ?")
		args = append(args, f.UserID)
	}
	if !f.Since.IsZero() {
		where = append(where, "time >= ?")
		args = append(args, float64(f.Since.UnixMilli())/1000)
	}
	if !f.Until.IsZero() {
		where = append(where, "time < ?")
		args = append(args, float64(f.Until.UnixMilli())/1000)
	}
	if !f.IncludeRecalled {
		where = append(where, "recalled = 0")
	}
	args = append(args, f.Limit)

	rows, err := s.db.Query("SELECT "+messageCols+" FROM messages WHERE "+
		strings.Join(where, " AND ")+" ORDER BY time DESC LIMIT ?", args...)
	if err != nil {
		return nil, fmt.Errorf("查询消息失败: %w", err)
	}
	defer rows.Close()

	var out []*MessageRecord
	for rows.Next() {
		r, err := scanMessage(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描消息行失败: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkRecalled 撤回只打标记不删行,重复标记幂等
func (s *Store) MarkRecalled(id int64) error {
	_, err := s.db.Exec("UPDATE messages SET recalled = 1 WHERE message_id = ?", id)
	if err != nil {
		return fmt.Errorf("标记撤回失败: %w", err)
	}
	return nil
}
