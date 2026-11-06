// Package storage 消息记忆库:收发消息、撤回标记、notice 事件的 SQLite 存取。
// 本包不 import event/handler,事件结构体到记录的转换在 handler 层完成。
package storage

import (
	"database/sql"
	"fmt"
	"regexp"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS messages (
    message_id  INTEGER PRIMARY KEY,
    direction   TEXT NOT NULL,
    chat_type   TEXT NOT NULL,
    chat_id     INTEGER NOT NULL,
    user_id     INTEGER NOT NULL,
    raw_message TEXT NOT NULL,
    segments    TEXT NOT NULL,
    time        REAL NOT NULL,
    recalled    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_messages_chat_time ON messages(chat_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_messages_user_time ON messages(user_id, time DESC);

CREATE TABLE IF NOT EXISTS message_recalls (
    message_id INTEGER PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS notices (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    notice_type TEXT NOT NULL,
    sub_type    TEXT,
    chat_id     INTEGER,
    user_id     INTEGER,
    raw         TEXT NOT NULL,
    time        REAL NOT NULL
);

-- FTS5 独立表:索引内容由 PutMessage 在 Go 侧按字分词后写入
-- (unicode61 分词器会把连续 CJK 当成单个 token,无法子串匹配,
--  触发器又做不了按字拆分,故弃用 content 表 + 触发器方案)
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(raw_message);
DROP TRIGGER IF EXISTS messages_ai;
DROP TRIGGER IF EXISTS messages_ad;
DROP TRIGGER IF EXISTS messages_au;
`

var externalContentFTS = regexp.MustCompile(`(?i)\bcontent\s*=`)

type Store struct {
	db *sql.DB
}

// Open 打开(必要时创建)数据库并执行幂等建表。
// path 传 ":memory:" 可得进程内库(测试用)。
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// modernc 驱动多连接写会撞锁,单连接 + WAL 足够本项目的写入量
	db.SetMaxOpenConns(1)
	if err := initSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化数据库失败: %w", err)
	}
	return &Store{db: db}, nil
}

func initSchema(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	var definition string
	if err := tx.QueryRow("SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'messages_fts'").Scan(&definition); err != nil {
		return err
	}
	if externalContentFTS.MatchString(definition) {
		if _, err := tx.Exec("DROP TABLE messages_fts; CREATE VIRTUAL TABLE messages_fts USING fts5(raw_message)"); err != nil {
			return fmt.Errorf("重建 FTS 表失败: %w", err)
		}
		rows, err := tx.Query("SELECT message_id, raw_message FROM messages")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			var text string
			if err := rows.Scan(&id, &text); err != nil {
				return err
			}
			if _, err := tx.Exec("INSERT INTO messages_fts(rowid, raw_message) VALUES (?, ?)", id, ftsIndex(text)); err != nil {
				return fmt.Errorf("迁移 FTS 索引失败: %w", err)
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) Close() error {
	return s.db.Close()
}
