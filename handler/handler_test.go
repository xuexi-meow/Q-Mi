package handler

import (
	"testing"

	"Q-Mi/event"
)

// 验证 handler 包的 init 注册确实挂到了 event 注册表上
// (依赖 main.go 的 blank import,测试里 import handler 包本身即触发 init)
func TestRegisteredKeys(t *testing.T) {
	keys := []string{
		"message.private",
		"message.group",
		"notice.group_upload",
		"notice.group_admin",
		"notice.group_decrease",
		"notice.group_increase",
		"notice.group_ban",
		"notice.friend_add",
		"notice.group_recall",
		"notice.friend_recall",
		"notice.group_card",
		"notice.notify.group_name",
		"notice.notify.group_title",
		"notice.group_essence",
		"notice.group_msg_emoji_like",
		"notice.notify.poke",
		"notice.notify.profile_like",
		"notice.notify.input_status",
		"notice.bot_offline",
		"request.friend",
		"request.group",
		"meta.heartbeat",
		"meta.lifecycle",
	}
	for _, key := range keys {
		if !event.Registered(key) {
			t.Errorf("key %s 未注册,检查 handler 包是否被 import", key)
		}
	}
}
