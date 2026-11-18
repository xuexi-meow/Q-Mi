// Package qzone 提供 QQ 空间动态与图片查看工具。
package qzone

import (
	"encoding/json"
	"fmt"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/tool"
	"Q-Mi/api"
)

type ImageResolver interface {
	DataURLFor(messageID int64, index int, url string) string
}

type Dependencies struct {
	Turn      *agentcontext.Turn
	Images    ImageResolver
	Admin     bool
	Call      func(api.Request) (json.RawMessage, error)
	Authorize func(action string, admin bool) error
}

type Tool = tool.Tool

const (
	timeLayout               = "01-02 15:04"
	maxFeedsReturned         = 10
	maxQzoneCommentsReturned = 10
	maxQzoneRepliesReturned  = 3
	maxQzoneSummaryRunes     = 100
	maxQzonePostContentRunes = 200
)

func (d *Dependencies) call(req api.Request) (json.RawMessage, error) {
	if d.Call == nil {
		return nil, fmt.Errorf("NapCat API 不可用")
	}
	return d.Call(req)
}

func (d *Dependencies) authorize(action string) error {
	if d.Authorize == nil {
		return fmt.Errorf("权限校验不可用")
	}
	return d.Authorize(action, d.Admin)
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

func truncateRunes(text string, max int) string {
	runes := []rune(text)
	if len(runes) > max {
		return string(runes[:max])
	}
	return text
}

// emptyOK 调用成功即返回 "{}"。
func emptyOK(_ json.RawMessage, err error) (string, error) {
	if err != nil {
		return "", err
	}
	return "{}", nil
}

// passthrough data 原样返回（NapCat 返回的本身就是紧凑 JSON）。
func passthrough(data json.RawMessage, err error) (string, error) {
	if err != nil {
		return "", err
	}
	return string(data), nil
}
