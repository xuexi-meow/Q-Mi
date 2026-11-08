// storage/images.go 图片内容寻址映射：message_images 表记录某条消息第 idx
// 张图片对应的内容哈希与 MIME，磁盘文件由 agent 层按 agent/images/<sha256>
// 存取。QQ 图片 URL 是一次性临时链（rkey 随时失效），映射以消息为锚后同一张
// 图全生命周期只需下载一次，跨重启字节稳定。
package storage

import "fmt"

// ImageRef message_images 表一行：消息第 Idx 张图的内容哈希与 MIME。
type ImageRef struct {
	Idx    int
	Sha256 string
	Mime   string
}

// PutImageRef 写入（幂等覆盖）；重复下载同一张图内容不变，覆盖无副作用。
func (s *Store) PutImageRef(messageID int64, idx int, sha256, mime string) error {
	_, err := s.db.Exec(
		"INSERT OR REPLACE INTO message_images (message_id, idx, sha256, mime) VALUES (?, ?, ?, ?)",
		messageID, idx, sha256, mime)
	if err != nil {
		return fmt.Errorf("写入图片映射失败: %w", err)
	}
	return nil
}

// ImageRefs 返回某条消息全部图片映射，按 idx 升序；无记录返回空切片。
func (s *Store) ImageRefs(messageID int64) ([]ImageRef, error) {
	rows, err := s.db.Query(
		"SELECT idx, sha256, mime FROM message_images WHERE message_id = ? ORDER BY idx", messageID)
	if err != nil {
		return nil, fmt.Errorf("查询图片映射失败: %w", err)
	}
	defer rows.Close()
	var refs []ImageRef
	for rows.Next() {
		var r ImageRef
		if err := rows.Scan(&r.Idx, &r.Sha256, &r.Mime); err != nil {
			return nil, fmt.Errorf("读取图片映射失败: %w", err)
		}
		refs = append(refs, r)
	}
	return refs, rows.Err()
}
