// Package computer 提供受工作区与审批边界约束的电脑工具。
package computer

import (
	"encoding/json"
	"fmt"
	"regexp"

	"Q-Mi/agent/tool"
	"Q-Mi/api"
	"Q-Mi/storage"
)

type ImageResolver interface {
	DataURLFor(messageID int64, index int, url string) string
}

type Dependencies struct {
	Store     *storage.Store
	Images    ImageResolver
	Workspace string
	Approved  bool
	Call      func(api.Request) (json.RawMessage, error)
}

type Tool = tool.Tool

var windowsDrive = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

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

func downloadFilePath(data json.RawMessage) string {
	var obj struct {
		Path string `json:"path"`
		File string `json:"file"`
	}
	if json.Unmarshal(data, &obj) == nil {
		return firstNonEmpty([]string{obj.Path, obj.File})
	}
	var path string
	_ = json.Unmarshal(data, &path)
	return path
}
