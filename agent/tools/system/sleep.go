package system

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	agentruntime "Q-Mi/agent/runtime"
	"Q-Mi/agent/tool"
)

func SleepTools(start func(string) (agentruntime.SleepSnapshot, error), status func() (agentruntime.SleepSnapshot, error)) []Tool {
	return []Tool{
		{
			Name:        "sleep",
			Description: "自主短时休息。程序随机安排醒来时间；睡眠期间消息存档但不参与聊天，密集的真实指向消息或 WebUI 可提前叫醒。成功后立即结束本次活动。必须单独调用；需要道晚安请先在上一轮 send_message，不与其他工具同批。",
			Parameters:  tool.ObjectParams(map[string]any{"reason": tool.StringProp("可选，简短的休息原因，不是对外消息")}, nil),
			Run: func(args json.RawMessage) (string, error) {
				if start == nil {
					return "", errors.New("睡眠服务不可用")
				}
				var p struct {
					Reason string `json:"reason"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if utf8.RuneCountInString(p.Reason) > 300 {
					return "", errors.New("休息原因不能超过 300 字")
				}
				state, err := start(strings.TrimSpace(p.Reason))
				if err != nil {
					return "", err
				}
				state.Memories, state.Deferred, state.Signals = nil, nil, nil
				state.ObservedIDs = nil
				return marshalCompact(state)
			},
		},
		{
			Name: "sleep_status", Description: "查看自己的作息状态、上次睡眠与唤醒原因；不会对外发言。",
			Parameters: tool.ObjectParams(nil, nil),
			Run: func(json.RawMessage) (string, error) {
				if status == nil {
					return "", errors.New("睡眠服务不可用")
				}
				state, err := status()
				if err != nil {
					return "", err
				}
				state.Memories, state.Deferred, state.Signals = nil, nil, nil
				state.ObservedIDs = nil
				return marshalCompact(state)
			},
		},
	}
}
