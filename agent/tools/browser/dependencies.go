// Package browser owns browser state, automation, and browser tools.
package browser

import (
	"encoding/json"
	"fmt"

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/tool"
	"Q-Mi/agent/workspacepath"
)

type Dependencies struct {
	Browser   *Manager
	Turn      *agentcontext.Turn
	Workspace string
	Approved  bool
}

type Tool = tool.Tool
type Turn = agentcontext.Turn

func ws(deps *Dependencies) string {
	if deps.Workspace != "" {
		return deps.Workspace
	}
	return workspacepath.DefaultDir
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
