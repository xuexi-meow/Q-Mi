// Package qq 提供 QQ 消息与管理工具，以及受权限约束的 NapCat 网关。
package qq

import (
	"encoding/json"
	"errors"
	"fmt"

	"Q-Mi/agent/tool"
	"Q-Mi/api"
	apimsg "Q-Mi/api/message"
	"Q-Mi/storage"
)

type Dependencies struct {
	Store     *storage.Store
	Workspace string
	Admin     bool
	Call      func(api.Request) (json.RawMessage, error)
	Send      func(string, int64, []*apimsg.Message) (string, error)
	SendFace  func(string, int64, string, int64) (string, error)
}

type Tool = tool.Tool

var errAdminRequired = errors.New("此操作需要管理员权限")

const (
	maxBanDuration     = 2592000
	maxMembersReturned = 100
	maxMsgHistoryCount = 20
)

func (d *Dependencies) call(req api.Request) (json.RawMessage, error) {
	if err := ValidateNapcatOutgoing(req); err != nil {
		return nil, err
	}
	if d.Call == nil {
		return nil, fmt.Errorf("NapCat API 不可用")
	}
	return d.Call(req)
}

func (d *Dependencies) send(chatType string, chatID int64, segments []*apimsg.Message) (string, error) {
	if d.Send == nil {
		return "", fmt.Errorf("消息发送不可用")
	}
	return d.Send(chatType, chatID, segments)
}

func (d *Dependencies) sendFace(chatType string, chatID int64, emojiID string, replyTo int64) (string, error) {
	if d.SendFace == nil {
		return "", fmt.Errorf("收藏表情发送不可用")
	}
	return d.SendFace(chatType, chatID, emojiID, replyTo)
}

func decodeArgs(args json.RawMessage, value any) error {
	if len(args) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, value); err != nil {
		return fmt.Errorf("工具参数不是合法 JSON: %w", err)
	}
	return nil
}

func marshalCompact(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("序列化工具结果失败: %w", err)
	}
	return string(data), nil
}

func truncateRunes(text string, max int) string {
	runes := []rune(text)
	if len(runes) > max {
		return string(runes[:max])
	}
	return text
}

func emptyOK(_ json.RawMessage, err error) (string, error) {
	if err != nil {
		return "", err
	}
	return "{}", nil
}

func passthrough(data json.RawMessage, err error) (string, error) {
	return string(data), err
}
