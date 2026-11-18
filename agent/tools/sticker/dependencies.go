// Package sticker 提供表情收藏、预览与 QQ 收藏表情操作。
package sticker

import (
	"encoding/json"
	"fmt"
	"strings"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/tool"
	"Q-Mi/api"
	apimsg "Q-Mi/api/message"
	"Q-Mi/storage"
)

type ImageStore interface {
	Download(string) ([]byte, string, error)
	Save(int64, int, []byte, string) string
	DataURLFromRef(storage.ImageRef) string
}

type Dependencies struct {
	Store  *storage.Store
	Turn   *agentcontext.Turn
	Images ImageStore
	Call   func(api.Request) (json.RawMessage, error)
	Send   func(string, int64, []*apimsg.Message) (string, error)
}

type Tool = tool.Tool

func (d *Dependencies) call(req api.Request) (json.RawMessage, error) {
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
	return string(data), err
}

func isDownloadableURL(url string) bool {
	lower := strings.ToLower(url)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func downloadFilePath(data json.RawMessage) string {
	var obj struct {
		Path string `json:"path"`
		File string `json:"file"`
	}
	if json.Unmarshal(data, &obj) == nil {
		if obj.Path != "" {
			return obj.Path
		}
		return obj.File
	}
	var path string
	_ = json.Unmarshal(data, &path)
	return path
}
