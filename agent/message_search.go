package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"Q-Mi/storage"
)

func searchMessagesTool(ctx *ToolContext) Tool {
	return Tool{
		Name:        "search_messages",
		Description: "查证历史：按私聊/群聊、会话、发送者 QQ、时间和可选关键词查询。含勿扰存档，排除撤回；取最近匹配记录按旧到新返回。",
		Parameters: objectParams(map[string]any{
			"query":     strProp("可选，全文搜索关键词；省略或空白时按其他条件查聊天记录"),
			"chat_type": strProp("可选，会话类型 private / group；传 chat_id 时必填，防止群号与私聊 QQ 混淆"),
			"chat_id":   intProp("可选，群号或私聊对象 QQ；需同时传 chat_type"),
			"user_id":   intProp("可选，发送者 QQ；群内查某人的发言用此项，不是群号"),
			"since":     strProp("可选，起始时间（包含）：RFC3339 带时区，或服务器本地时间 YYYY-MM-DD HH:MM[:SS] / YYYY-MM-DD"),
			"until":     strProp("可选，截止时间（不包含），格式同 since；单独日期表示当天零点，不是当天结束"),
			"limit":     intProp("可选，最近匹配条数，默认 20，最大 50"),
		}, nil),
		Usage: "关键词、会话类型、发送者、时间至少一项。某群最近 10 条：`{\"chat_type\":\"group\",\"chat_id\":888,\"limit\":10}`；私聊用 private。可加 query/user_id/since/until；时间范围 [since, until)，until 须晚于 since。\n" +
			"返回 {messages,returned,omitted,truncated}，消息旧到新；含 message_id/chat_type/chat_id/user_id/sender_name/text/time/timestamp（完整时间）。正文最多 1000 字符，超限标 text_truncated/text_length；整份 JSON 最多 12000 字符，优先留最新。omitted 只计所选窗口中因长度省略的条数，不是历史总数；昵称过长也截断并标记。无命中可换词、去掉关键词或缩小范围重查；不等于没发生。",
		Run: func(args json.RawMessage) (string, error) {
			var p struct {
				Query    string `json:"query"`
				ChatType string `json:"chat_type"`
				ChatID   int64  `json:"chat_id"`
				UserID   int64  `json:"user_id"`
				Since    string `json:"since"`
				Until    string `json:"until"`
				Limit    int    `json:"limit"`
			}
			if err := decodeArgs(args, &p); err != nil {
				return "", err
			}
			p.Query = strings.TrimSpace(p.Query)
			if p.ChatType != "" && p.ChatType != "private" && p.ChatType != "group" {
				return "", fmt.Errorf("chat_type 必须是 private 或 group")
			}
			if p.ChatID < 0 || p.UserID < 0 {
				return "", fmt.Errorf("chat_id/user_id 必须是正整数，或省略")
			}
			if p.ChatID != 0 && p.ChatType == "" {
				return "", fmt.Errorf("传 chat_id 时必须指定 chat_type（private / group）")
			}
			since, err := parseMessageSearchTime("since", p.Since)
			if err != nil {
				return "", err
			}
			until, err := parseMessageSearchTime("until", p.Until)
			if err != nil {
				return "", err
			}
			if !since.IsZero() && !until.IsZero() && !since.Before(until) {
				return "", fmt.Errorf("until 必须晚于 since；起始包含、截止不包含")
			}
			if p.Query == "" && p.ChatType == "" && p.UserID == 0 && since.IsZero() && until.IsZero() {
				return "", fmt.Errorf("请提供关键词、会话、发送者或时间范围，避免无目标地读取全部历史")
			}
			limit := p.Limit
			if limit <= 0 {
				limit = 20
			}
			if limit > 50 {
				limit = 50
			}
			f := storage.MessageFilter{
				ChatType: p.ChatType, ChatID: p.ChatID, UserID: p.UserID,
				Since: since, Until: until, Limit: limit,
			}
			if p.Query != "" {
				return queryMessageRows(ctx.Store, ctx.Images, ctx.Turn, f, p.Query)
			}
			return queryMessageRows(ctx.Store, ctx.Images, ctx.Turn, f)
		},
	}
}

func parseMessageSearchTime(field, value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("%s 时间无效：使用 RFC3339（如 2026-11-09T10:00:00+08:00）或本地时间 YYYY-MM-DD HH:MM[:SS] / YYYY-MM-DD", field)
}
