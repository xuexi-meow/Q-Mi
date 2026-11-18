// Package memory 提供消息检索、图片查看和会话勿扰工具。
package memory

import (
	"encoding/json"
	"fmt"
	"strings"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/tool"
	"Q-Mi/api"
	"Q-Mi/storage"
)

type ImageResolver interface {
	DataURLFor(messageID int64, index int, url string) string
}

type Dependencies struct {
	Store     *storage.Store
	Turn      *agentcontext.Turn
	Images    ImageResolver
	Attention agentcontext.HistoryAttention
	Call      func(api.Request) (json.RawMessage, error)
}

type Tool = tool.Tool
type Turn = agentcontext.Turn

const timeLayout = "01-02 15:04"

func (d *Dependencies) attention(turn *Turn) *storage.AttentionFilter {
	if d.Attention == nil {
		return nil
	}
	return d.Attention(turn)
}

func (d *Dependencies) call(req api.Request) (json.RawMessage, error) {
	if d.Call == nil {
		return nil, fmt.Errorf("NapCat API 不可用")
	}
	return d.Call(req)
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
