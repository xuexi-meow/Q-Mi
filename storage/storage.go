// Package storage 消息记忆库:收发消息、撤回标记、notice 事件的 SQLite 存取。
// 本包不 import event/handler,事件结构体到记录的转换在 handler 层完成。
package storage

import (
	"database/sql"
	"fmt"

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

CREATE TABLE IF NOT EXISTS notices (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    notice_type TEXT NOT NULL,
    sub_type    TEXT,
    chat_id     INTEGER,
    user_id     INTEGER,
    raw         TEXT NOT NULL,
    time        REAL NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts
    USING fts5(raw_message, content='messages', content_rowid='message_id');
CREATE TRIGGER IF NOT EXISTS messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, raw_message) VALUES (new.message_id, new.raw_message);
END;
CREATE TRIGGER IF NOT EXISTS messages_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, raw_message)
    VALUES ('delete', old.message_id, old.raw_message);
END;
`

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
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("建表失败: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
