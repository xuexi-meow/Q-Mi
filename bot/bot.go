// Package bot 业务助手:从消息事件中提取文本、识别@与管理员、按会话回复、匹配命令。
// 只组合 event/chain/config/request 的既有能力,不引入注册框架之外的新机制。
package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	apimsg "Q-Mi/api/message"
	"Q-Mi/app"
	"Q-Mi/chain"
	evmsg "Q-Mi/event/message"
	"Q-Mi/utils/request"
)

// Text 拼接消息中的纯文本段,去首尾空白;未解析的段会先做类型解析。
func Text(msgs []*evmsg.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		if m == nil || m.Type != string(apimsg.TypeText) {
			continue
		}
		if m.Data == nil {
			if _, err := m.TypeHandler(); err != nil {
				continue
			}
		}
		if txt, ok := m.Data.(evmsg.Text); ok {
			b.WriteString(txt.Text)
		}
	}
	return strings.TrimSpace(b.String())
}

// AtMe 判断消息是否@了机器人(配置 bot.qq_number)。
func AtMe(msgs []*evmsg.Message) bool {
	self := selfQQ()
	if self == 0 {
		return false
	}
	return AtUserID(msgs, int64(self))
}

// AtUserID 判断消息是否@了指定 QQ(推荐用事件自带的 self_id,不依赖配置填写)。
func AtUserID(msgs []*evmsg.Message, selfID int64) bool {
	if selfID == 0 {
		return false
	}
	for _, m := range msgs {
		if m == nil || m.Type != string(apimsg.TypeAt) {
			continue
		}
		if m.Data == nil {
			if _, err := m.TypeHandler(); err != nil {
				continue
			}
		}
		if at, ok := m.Data.(evmsg.At); ok && at.QQ == strconv.FormatInt(selfID, 10) {
			return true
		}
	}
	return false
}

// IsAdmin 判断 QQ 是否在配置 bot.admin_number 中。
func IsAdmin(qq int64) bool {
	cfg := app.GetApp().Current()
	if cfg == nil || cfg.Bot == nil {
		return false
	}
	for _, id := range cfg.Bot.Admin_Number {
		if int64(id) == qq {
			return true
		}
	}
	return false
}

// Reply 按会话类型把消息链发给对方;chatType 与 storage 一致("private"/"group")。
func Reply(ctx context.Context, chatType string, chatID int64, c chain.Chain) error {
	switch chatType {
	case "private":
		_, err := request.SendPrivateMsg(ctx, chatID, c...)
		return err
	case "group":
		_, err := request.SendGroupMsg(ctx, chatID, c...)
		return err
	default:
		return fmt.Errorf("未知会话类型 %q", chatType)
	}
}

func selfQQ() int {
	cfg := app.GetApp().Current()
	if cfg == nil || cfg.Bot == nil {
		return 0
	}
	return cfg.Bot.QQ_Number
}
