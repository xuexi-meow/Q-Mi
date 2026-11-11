package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

var ErrMessageAnchor = errors.New("消息不在指定会话中")

// MessageWindow returns a stable window containing the scoped anchor, newest first.
func (s *Store) MessageWindow(chatType string, chatID, messageID int64, limit int) ([]*MessageRecord, error) {
	target, err := s.GetMessage(messageID)
	if err != nil {
		return nil, err
	}
	if target == nil || target.Recalled || target.ChatType != chatType || target.ChatID != chatID {
		return nil, ErrMessageAnchor
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT `+messageCols+` FROM messages WHERE message_id IN (
		SELECT message_id FROM (SELECT message_id FROM messages
			WHERE chat_type=? AND chat_id=? AND recalled=0 AND (time,message_id) <= (?,?)
			ORDER BY time DESC,message_id DESC LIMIT ?)
		UNION ALL SELECT message_id FROM (SELECT message_id FROM messages
			WHERE chat_type=? AND chat_id=? AND recalled=0 AND (time,message_id) > (?,?)
			ORDER BY time ASC,message_id ASC LIMIT ?))
		ORDER BY time DESC,message_id DESC`,
		chatType, chatID, target.Time, messageID, (limit+1)/2,
		chatType, chatID, target.Time, messageID, limit/2)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*MessageRecord{}
	for rows.Next() {
		r, err := scanMessage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

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
	SenderName string // 发送者昵称;群聊为群名片(空则昵称);发出 = "我";旧行为空串
}

const messageCols = "message_id, direction, chat_type, chat_id, user_id, raw_message, segments, time, recalled, sender_name"

func (s *Store) PutMessage(r *MessageRecord) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开始写入消息事务失败: %w", err)
	}
	defer tx.Rollback()

	recalled := 0
	if r.Recalled {
		recalled = 1
	}
	_, err = tx.Exec(`
INSERT INTO messages (message_id, direction, chat_type, chat_id, user_id, raw_message, segments, time, recalled, sender_name)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ? OR EXISTS(SELECT 1 FROM message_recalls WHERE message_id = ?), ?)
ON CONFLICT(message_id) DO UPDATE SET
    direction=excluded.direction, chat_type=excluded.chat_type, chat_id=excluded.chat_id,
    user_id=excluded.user_id, raw_message=excluded.raw_message, segments=excluded.segments,
    time=excluded.time, recalled=messages.recalled OR excluded.recalled, sender_name=excluded.sender_name`,
		r.MessageID, r.Direction, r.ChatType, r.ChatID, r.UserID, r.RawMessage, string(r.Segments), r.Time, recalled, r.MessageID, r.SenderName)
	if err != nil {
		return fmt.Errorf("写入消息失败: %w", err)
	}
	// 同步 FTS 索引(按字分词);先删旧行以支持同 id 覆盖(upsert)
	if _, err := tx.Exec("DELETE FROM messages_fts WHERE rowid = ?", r.MessageID); err != nil {
		return fmt.Errorf("清理 FTS 索引失败: %w", err)
	}
	if _, err := tx.Exec("INSERT INTO messages_fts(rowid, raw_message) VALUES (?, ?)",
		r.MessageID, ftsIndex(r.RawMessage)); err != nil {
		return fmt.Errorf("写入 FTS 索引失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交消息事务失败: %w", err)
	}
	return nil
}

// ftsIndex 把文本转成按字分词的索引文本:非 ASCII 字符(中文等)两两之间插空格,
// 使 FTS5 unicode61 分词器按单字建索引;连续 ASCII 字母数字保持完整(英文单词不拆)。
func ftsIndex(s string) string {
	var b strings.Builder
	prevASCIIToken := false
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) { // 分隔符不入索引
			prevASCIIToken = false
			continue
		}
		asciiToken := r < 128
		if b.Len() > 0 && !(prevASCIIToken && asciiToken) {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
		prevASCIIToken = asciiToken
	}
	return b.String()
}

// ftsQuery 把用户查询转成 FTS5 短语序列:连续非 ASCII 字母数字按字组成短语、ASCII 连续字母数字为一个词,
// 每项加引号(顺带规避 FTS 语法注入);多项之间为 AND 语义。
func ftsQuery(query string) string {
	var terms []string
	var run strings.Builder
	flush := func() {
		if run.Len() > 0 {
			terms = append(terms, run.String())
			run.Reset()
		}
	}
	prevASCIIToken := false
	for _, r := range query {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) { // 与索引侧一致,分隔符丢弃
			flush()
			continue
		}
		asciiToken := r < 128
		if run.Len() > 0 {
			if asciiToken != prevASCIIToken {
				flush()
			} else if !asciiToken {
				run.WriteByte(' ')
			}
		}
		run.WriteRune(r)
		prevASCIIToken = asciiToken
	}
	flush()
	quoted := make([]string, len(terms))
	for i, t := range terms {
		quoted[i] = `"` + t + `"`
	}
	return strings.Join(quoted, " AND ")
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
		&r.RawMessage, &segments, &r.Time, &recalled, &r.SenderName); err != nil {
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
	Attention       *AttentionFilter // 非 nil 时按勿扰规则筛选最近上下文，先过滤再 LIMIT
}

func (s *Store) QueryMessages(f MessageFilter) ([]*MessageRecord, error) {
	return s.queryMessages(f, "")
}

func (s *Store) queryMessages(f MessageFilter, query string) ([]*MessageRecord, error) {
	if f.Limit <= 0 {
		f.Limit = 50
	}
	where := []string{"1=1"}
	var args []any
	if query != "" {
		where = append(where, "message_id IN (SELECT rowid FROM messages_fts WHERE messages_fts MATCH ?)")
		args = append(args, query)
	}
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
	if f.Attention != nil {
		clause, values := attentionWhere(f.Attention)
		where = append(where, clause)
		args = append(args, values...)
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

// SearchMessages FTS5 全文搜索,按时间倒序。
// 中文按字索引(见 ftsIndex),查询经 ftsQuery 转成短语序列后匹配。
func (s *Store) SearchMessages(query string, f MessageFilter) ([]*MessageRecord, error) {
	q := ftsQuery(query)
	if q == "" {
		return nil, nil
	}
	return s.queryMessages(f, q)
}

// NoticeRecord notices 表的一行
type NoticeRecord struct {
	NoticeType string
	SubType    string
	ChatID     int64
	UserID     int64
	Raw        []byte
	Time       float64
}

func (s *Store) PutNotice(n *NoticeRecord) error {
	_, err := s.db.Exec(`
INSERT INTO notices (notice_type, sub_type, chat_id, user_id, raw, time)
VALUES (?, ?, ?, ?, ?, ?)`,
		n.NoticeType, n.SubType, n.ChatID, n.UserID, string(n.Raw), n.Time)
	if err != nil {
		return fmt.Errorf("写入通知失败: %w", err)
	}
	return nil
}

// MarkRecalled 撤回只打标记不删行,重复标记幂等
func (s *Store) MarkRecalled(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("开始撤回事务失败: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("INSERT INTO message_recalls(message_id) VALUES (?) ON CONFLICT(message_id) DO NOTHING", id); err != nil {
		return fmt.Errorf("写入撤回记录失败: %w", err)
	}
	if _, err := tx.Exec("UPDATE messages SET recalled = 1 WHERE message_id = ?", id); err != nil {
		return fmt.Errorf("标记撤回失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("提交撤回事务失败: %w", err)
	}
	return nil
}

// Stats 返回 messages 与 notices 两表的行数（W1 WebUI 概览统计用）。
func (s *Store) Stats() (messages, notices int64, err error) {
	if err := s.db.QueryRow("SELECT COUNT(*) FROM messages").Scan(&messages); err != nil {
		return 0, 0, fmt.Errorf("统计 messages 失败: %w", err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM notices").Scan(&notices); err != nil {
		return 0, 0, fmt.Errorf("统计 notices 失败: %w", err)
	}
	return messages, notices, nil
}

// maxChatSummaryText 会话摘要文本截断长度（W2 WebUI 会话列表用）。
const maxChatSummaryText = 80

// ChatSummary 一个会话（chat_type + chat_id）的摘要：条数、最新时间、
// 最后一条消息文本（前 maxChatSummaryText 字符）。
type ChatSummary struct {
	ChatType string  `json:"chat_type"`
	ChatID   int64   `json:"chat_id"`
	LastText string  `json:"last_text"`
	LastTime float64 `json:"last_time"`
	Count    int64   `json:"count"`
}

// ChatSummaries 会话列表：按 chat_type+chat_id 分组统计，附最后一条消息
// 文本（相关子查询取同会话最新行的 raw_message），按最新时间倒序。
func (s *Store) ChatSummaries() ([]ChatSummary, error) {
	rows, err := s.db.Query(`
SELECT m.chat_type, m.chat_id, COUNT(*), MAX(m.time),
       (SELECT m2.raw_message FROM messages m2
        WHERE m2.chat_type = m.chat_type AND m2.chat_id = m.chat_id
        ORDER BY m2.time DESC, m2.message_id DESC LIMIT 1)
FROM messages m
GROUP BY m.chat_type, m.chat_id
ORDER BY MAX(m.time) DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询会话列表失败: %w", err)
	}
	defer rows.Close()

	out := []ChatSummary{}
	for rows.Next() {
		var c ChatSummary
		if err := rows.Scan(&c.ChatType, &c.ChatID, &c.Count, &c.LastTime, &c.LastText); err != nil {
			return nil, fmt.Errorf("扫描会话行失败: %w", err)
		}
		if runes := []rune(c.LastText); len(runes) > maxChatSummaryText {
			c.LastText = string(runes[:maxChatSummaryText])
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
