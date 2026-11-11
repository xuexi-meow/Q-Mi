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
    recalled    INTEGER NOT NULL DEFAULT 0,
    sender_name TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_messages_chat_time ON messages(chat_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_messages_user_time ON messages(user_id, time DESC);

CREATE TABLE IF NOT EXISTS chat_dnd (
    chat_type TEXT NOT NULL CHECK (chat_type IN ('private', 'group')),
    chat_id   INTEGER NOT NULL CHECK (chat_id > 0),
    PRIMARY KEY (chat_type, chat_id)
);

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

-- 表情收藏:face(自带表情)/mface(商城表情)/image(图片)三类
CREATE TABLE IF NOT EXISTS stickers (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    kind             TEXT NOT NULL CHECK (kind IN ('face','mface','image')),
    face_id          TEXT NOT NULL DEFAULT '',
    emoji_id         TEXT NOT NULL DEFAULT '',
    emoji_package_id TEXT NOT NULL DEFAULT '',
    "key"            TEXT NOT NULL DEFAULT '',
    url              TEXT NOT NULL DEFAULT '',
    summary          TEXT NOT NULL DEFAULT '',
    note             TEXT NOT NULL DEFAULT '',
    preview_sha      TEXT NOT NULL DEFAULT '',
    preview_mime     TEXT NOT NULL DEFAULT '',
    qq_emoji_id      TEXT NOT NULL DEFAULT '',
    qq_res_id        TEXT NOT NULL DEFAULT '',
    qq_md5           TEXT NOT NULL DEFAULT '',
    qq_desc          TEXT NOT NULL DEFAULT '',
    created_at       REAL NOT NULL
);

-- 图片内容寻址映射:消息第 idx 张图 → 磁盘内容哈希(文件在 agent/images/<sha256>)
CREATE TABLE IF NOT EXISTS message_images (
    message_id INTEGER NOT NULL,
    idx        INTEGER NOT NULL,
    sha256     TEXT NOT NULL,
    mime       TEXT NOT NULL,
    PRIMARY KEY (message_id, idx)
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
	// 旧库迁移:messages 表补 sender_name 列(幂等,事务内)
	var hasSenderName int
	if err := tx.QueryRow("SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name = 'sender_name'").Scan(&hasSenderName); err != nil {
		return err
	}
	if hasSenderName == 0 {
		if _, err := tx.Exec("ALTER TABLE messages ADD COLUMN sender_name TEXT NOT NULL DEFAULT ''"); err != nil {
			return fmt.Errorf("迁移 messages.sender_name 列失败: %w", err)
		}
	}
	// 旧库迁移:stickers 表补备注/预览列(幂等,事务内)。
	stickerColumns := []struct{ name, ddl string }{
		{"note", "ALTER TABLE stickers ADD COLUMN note TEXT NOT NULL DEFAULT ''"},
		{"preview_sha", "ALTER TABLE stickers ADD COLUMN preview_sha TEXT NOT NULL DEFAULT ''"},
		{"preview_mime", "ALTER TABLE stickers ADD COLUMN preview_mime TEXT NOT NULL DEFAULT ''"},
		{"qq_emoji_id", "ALTER TABLE stickers ADD COLUMN qq_emoji_id TEXT NOT NULL DEFAULT ''"},
		{"qq_res_id", "ALTER TABLE stickers ADD COLUMN qq_res_id TEXT NOT NULL DEFAULT ''"},
		{"qq_md5", "ALTER TABLE stickers ADD COLUMN qq_md5 TEXT NOT NULL DEFAULT ''"},
		{"qq_desc", "ALTER TABLE stickers ADD COLUMN qq_desc TEXT NOT NULL DEFAULT ''"},
	}
	for _, col := range stickerColumns {
		var exists int
		if err := tx.QueryRow("SELECT COUNT(*) FROM pragma_table_info('stickers') WHERE name = ?", col.name).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if _, err := tx.Exec(col.ddl); err != nil {
				return fmt.Errorf("迁移 stickers.%s 列失败: %w", col.name, err)
			}
		}
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
