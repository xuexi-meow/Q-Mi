package qq

import (
	"fmt"

	"Q-Mi/storage"
)

func validateReplyScope(store *storage.Store, chatType string, chatID int64, segments []SegmentParam) error {
	if store == nil {
		return nil
	}
	for index, segment := range segments {
		if segment.Type != "reply" {
			continue
		}
		id, err := segID(segment.Data, "id", index)
		if err != nil {
			return err
		}
		record, err := store.GetMessage(id)
		if err != nil {
			return fmt.Errorf("无法核对引用消息 %d: %w", id, err)
		}
		if record != nil && (record.ChatType != chatType || record.ChatID != chatID) {
			return fmt.Errorf("引用消息 %d 不属于发送目标 %s:%d，请核对会话，不能跨会话引用", id, chatType, chatID)
		}
	}
	return nil
}
