package system

import (
	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/tool"
	"encoding/json"
	"fmt"
)

// coreTools 核心板块：发消息、思考笔记、情绪、结束。finish 是唯一的轮终止工具。
func StateTools(notesPath, statePath string) []Tool {
	return []Tool{
		{
			Name:        "think",
			Description: "记一条思考笔记，持久化后下次模型请求前即注入最新状态快照，用于保持研究与活动连贯。",
			Parameters: tool.ObjectParams(map[string]any{
				"note": tool.StringProp("笔记内容"),
			}, []string{"note"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Note string `json:"note"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.Note == "" {
					return "", fmt.Errorf("note 不能为空")
				}
				if err := agentcontext.AppendNote(notesPath, p.Note); err != nil {
					return "", fmt.Errorf("写思考笔记失败: %w", err)
				}
				return "{}", nil
			},
		},
		{
			Name:        "update_mood",
			Description: "更新自己的当前情绪状态，会注入到之后的对话开头；只在情绪真的变化时调用。",
			Parameters: tool.ObjectParams(map[string]any{
				"mood": tool.StringProp("新的情绪状态，如 开心 / 无聊 / 有点生气"),
			}, []string{"mood"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Mood string `json:"mood"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.Mood == "" {
					return "", fmt.Errorf("mood 不能为空")
				}
				return "{}", agentcontext.WriteState(statePath, p.Mood)
			},
		},
		{
			Name:        "finish",
			Description: "结束本轮思考。可以发了消息再结束，也可以什么都不发直接结束；调用后本轮终止。",
			Parameters:  tool.ObjectParams(nil, nil),
			Usage:       "无参数。finish 前已发出的消息均已生效；不调用 send_message 直接 finish = 本轮不回复。",
		},
	}
}
