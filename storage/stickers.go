// stickers.go 表情收藏表：face（自带小黄脸）/ mface（商城表情）/ image（图片）
// 三类，供 agent 收藏（save_sticker）与复发（send_message 的 sticker 段）。
// stickers 表在 schema 中以 CREATE TABLE IF NOT EXISTS 幂等建表，老库升级时
// 由 Open 自动补建（与 messages/notices 同机制）。
package storage

import (
	"database/sql"
	"fmt"
	"time"
)

// Sticker stickers 表的一行
type Sticker struct {
	ID             int64
	Kind           string // "face" / "mface" / "image"
	FaceID         string // face: 自带表情 id
	EmojiID        string // mface: 商城表情 emoji_id
	EmojiPackageID string // mface: 商城表情包 id
	Key            string // mface: 复发所需 key
	URL            string // image: 图片链接
	Summary        string // 名称/描述（face 为表情名，可空）
	CreatedAt      float64
}

// stickerCols 查询列（"key" 是 SQLite 关键字，加引号）。
const stickerCols = `id, kind, face_id, emoji_id, emoji_package_id, "key", url, summary, created_at`

// AddSticker 收藏一条表情，返回自增 id；CreatedAt 为零值时自动填当前时间。
func (s *Store) AddSticker(st Sticker) (int64, error) {
	if st.CreatedAt == 0 {
		st.CreatedAt = float64(time.Now().UnixMilli()) / 1000
	}
	res, err := s.db.Exec(`
INSERT INTO stickers (kind, face_id, emoji_id, emoji_package_id, "key", url, summary, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		st.Kind, st.FaceID, st.EmojiID, st.EmojiPackageID, st.Key, st.URL, st.Summary, st.CreatedAt)
	if err != nil {
		return 0, fmt.Errorf("写入表情收藏失败: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("获取表情收藏 id 失败: %w", err)
	}
	return id, nil
}

// ListStickers 全部收藏，按收藏先后（id 正序）；空库返回空切片。
func (s *Store) ListStickers() ([]Sticker, error) {
	rows, err := s.db.Query("SELECT " + stickerCols + " FROM stickers ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("查询表情收藏失败: %w", err)
	}
	defer rows.Close()

	out := []Sticker{}
	for rows.Next() {
		st, err := scanSticker(rows)
		if err != nil {
			return nil, fmt.Errorf("扫描表情收藏行失败: %w", err)
		}
		out = append(out, *st)
	}
	return out, rows.Err()
}

// GetSticker 按 id 查单条；不存在返回 nil, nil。
func (s *Store) GetSticker(id int64) (*Sticker, error) {
	row := s.db.QueryRow("SELECT "+stickerCols+" FROM stickers WHERE id = ?", id)
	st, err := scanSticker(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("查询表情 %d 失败: %w", id, err)
	}
	return st, nil
}

// DeleteSticker 按 id 删除；id 不存在时同样返回 nil（幂等）。
func (s *Store) DeleteSticker(id int64) error {
	if _, err := s.db.Exec("DELETE FROM stickers WHERE id = ?", id); err != nil {
		return fmt.Errorf("删除表情 %d 失败: %w", id, err)
	}
	return nil
}

// scanSticker 复用 store.go 的 scanner 抽象（*sql.Row / *sql.Rows 共用）。
func scanSticker(row scanner) (*Sticker, error) {
	var st Sticker
	if err := row.Scan(&st.ID, &st.Kind, &st.FaceID, &st.EmojiID, &st.EmojiPackageID,
		&st.Key, &st.URL, &st.Summary, &st.CreatedAt); err != nil {
		return nil, err
	}
	return &st, nil
}
