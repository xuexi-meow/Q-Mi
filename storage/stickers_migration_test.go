package storage

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// TestStickerColumnsMigrateFromOldSchema 旧库（无 note/preview 列）打开后应
// 自动补列，旧数据可读、新字段可写。
func TestStickerColumnsMigrateFromOldSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
CREATE TABLE stickers (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    kind             TEXT NOT NULL,
    face_id          TEXT NOT NULL DEFAULT '',
    emoji_id         TEXT NOT NULL DEFAULT '',
    emoji_package_id TEXT NOT NULL DEFAULT '',
    "key"            TEXT NOT NULL DEFAULT '',
    url              TEXT NOT NULL DEFAULT '',
    summary          TEXT NOT NULL DEFAULT '',
    created_at       REAL NOT NULL
);
INSERT INTO stickers (kind, url, summary, created_at) VALUES ('image', 'http://old/x.png', '旧表情', 1);
`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(path)
	if err != nil {
		t.Fatalf("旧库打开失败: %v", err)
	}
	defer s.Close()

	old, err := s.GetSticker(1)
	if err != nil || old == nil {
		t.Fatalf("旧数据读取失败: %v %v", old, err)
	}
	if old.Summary != "旧表情" || old.Note != "" || old.PreviewSHA != "" {
		t.Fatalf("旧行字段 = %+v", old)
	}

	note := "新备注"
	sha := "abc123"
	mime := "image/png"
	if err := s.UpdateSticker(1, StickerUpdate{Note: &note, PreviewSHA: &sha, PreviewMime: &mime}); err != nil {
		t.Fatalf("迁移后更新失败: %v", err)
	}
	got, err := s.GetSticker(1)
	if err != nil || got == nil {
		t.Fatal(err)
	}
	if got.Note != note || got.PreviewSHA != sha || got.PreviewMime != mime {
		t.Fatalf("更新未生效: %+v", got)
	}
}

func TestUpdateStickerUnknownID(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "x.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	note := "x"
	if err := s.UpdateSticker(42, StickerUpdate{Note: &note}); err == nil {
		t.Fatal("不存在的 id 应报错")
	}
}
