// 本文件只保留工具组装和 QQ 传输接线，能力实现位于 tools 子包。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"Q-Mi/agent/tools/qq"
	"Q-Mi/agent/tools/system"
	"Q-Mi/api"
	apimsg "Q-Mi/api/message"
	"Q-Mi/utils/request"
)

func toolDefs(tools []Tool) []ToolDef {
	defs := make([]ToolDef, 0, len(tools))
	for _, item := range tools {
		defs = append(defs, ToolDef{Type: "function", Function: ToolSchema{
			Name: item.Name, Description: item.Description, Parameters: toolParameters(item),
		}})
	}
	return defs
}

func coreTools(ctx *ToolContext) []Tool {
	tools := append([]Tool{qq.MessageTool(qqDependencies(ctx))},
		system.StateTools(ctx.NotesPath, ctx.StatePath)...)
	return append(tools, system.SleepTools(ctx.SleepStart, ctx.SleepStatus)...)
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

func sendSegments(chatType string, chatID int64, segments []*apimsg.Message) (string, error) {
	var text strings.Builder
	for _, segment := range segments {
		if segment != nil && segment.Type == apimsg.TypeText {
			if data, ok := segment.Data.(apimsg.MakeText); ok {
				text.WriteString(data.Text)
			}
		}
	}
	if err := qq.ValidateOutgoingText(text.String()); err != nil {
		return "", err
	}
	var response *api.Response[api.SendMsgData]
	var err error
	if chatType == "group" {
		response, err = request.SendGroupMsg(context.Background(), chatID, segments...)
	} else {
		response, err = request.SendPrivateMsg(context.Background(), chatID, segments...)
	}
	if err != nil {
		return "", err
	}
	return marshalCompact(map[string]any{"message_id": response.Data.MessageID})
}

func callNapCat(req api.Request) (json.RawMessage, error) {
	if err := qq.ValidateNapcatOutgoing(req); err != nil {
		return nil, err
	}
	response, err := request.Call(context.Background(), req)
	if err != nil {
		return nil, err
	}
	return response.Data, nil
}
