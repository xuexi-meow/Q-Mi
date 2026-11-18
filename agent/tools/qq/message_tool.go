package qq

import (
	"Q-Mi/agent/tool"
	apimsg "Q-Mi/api/message"
	"encoding/json"
	"fmt"
)

func MessageTool(ctx *Dependencies) Tool {
	store := ctx.Store
	return Tool{
		Name:        "send_message",
		Description: "给指定会话发送一条消息：text 纯文本，或 segments 富消息段数组（at/reply/face/mface/sticker/dice/rps/json/image），两者二选一。正文只写给对方看的话，不要附上下文的时间/会话/发送者前缀或内部标签。通常合并为一条；不想发言就调用 finish。",
		Parameters: tool.ObjectParams(map[string]any{
			"chat_type": tool.StringProp("会话类型：private / group"),
			"chat_id":   tool.IntProp("会话 ID：群号；私聊 = 对方 QQ"),
			"text":      tool.StringProp("要发送的纯文本内容（与 segments 二选一）"),
			"segments": map[string]any{
				"type":        "array",
				"description": "富消息段数组（与 text 二选一），按顺序发送。段类型白名单：text/at/reply/face/mface/sticker/dice/rps/json/image",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"type": tool.StringProp("段类型：text / at / reply / face / mface / sticker / dice / rps / json / image"),
						"data": map[string]any{
							"type":        "object",
							"description": "段参数：text{text} / at{qq} / reply{id} / face{id} / mface{emoji_id, emoji_package_id, key, summary?} / sticker{id}（发自己收藏的表情，列表见 list_stickers） / dice{result} / rps{result} / json{data} / image{file, summary?}；image 的 file 只支持 http/https/base64:// 图片链接",
						},
					},
					"required": []string{"type", "data"},
				},
			},
		}, []string{"chat_type", "chat_id"}),
		Usage: "示例——引用回复某人并 @ 他说话：\n`{\"chat_type\":\"group\",\"chat_id\":928952016,\"segments\":[{\"type\":\"reply\",\"data\":{\"id\":12345}},{\"type\":\"at\",\"data\":{\"qq\":10001}},{\"type\":\"text\",\"data\":{\"text\":\"来了\"}}]}`\n错误形态：text/segments 皆缺或同给 → 报错回填；段类型不在白名单 → 报错；text 单次累计 ≤8000 字符。",
		Run: func(args json.RawMessage) (string, error) {
			var p struct {
				ChatType string         `json:"chat_type"`
				ChatID   int64          `json:"chat_id"`
				Text     string         `json:"text"`
				Segments []SegmentParam `json:"segments"`
			}
			if err := decodeArgs(args, &p); err != nil {
				return "", err
			}
			if p.ChatType != "private" && p.ChatType != "group" {
				return "", fmt.Errorf("chat_type 必须是 private 或 group, got %q", p.ChatType)
			}
			if p.ChatID == 0 {
				return "", fmt.Errorf("chat_id 不能为 0")
			}
			var segs []*apimsg.Message
			switch {
			case p.Text != "" && len(p.Segments) > 0:
				return "", fmt.Errorf("text 与 segments 只能二选一，避免歧义")
			case p.Text != "":
				if n := len([]rune(p.Text)); n > maxSendMessageTextRunes {
					return "", fmt.Errorf("text 内容 %d 字符，超过单次上限 %d", n, maxSendMessageTextRunes)
				}
				segs = []*apimsg.Message{apimsg.Text(p.Text)}
			case len(p.Segments) > 0:
				if err := validateReplyScope(store, p.ChatType, p.ChatID, p.Segments); err != nil {
					return "", err
				}
				// 已加入 QQ 收藏的表情只能单独一条发（协议端限制），
				// 单段 sticker 直接走 send_custom_face，保住表情样式。
				if len(p.Segments) == 1 && p.Segments[0].Type == "sticker" {
					if st := stickerBySegment(store, p.Segments[0].Data); st != nil && st.QQEmojiID != "" {
						data, err := ctx.sendFace(p.ChatType, p.ChatID, st.QQEmojiID, 0)
						if err != nil {
							return "", err
						}
						return marshalCompact(map[string]any{
							"sent": true, "as": "qq_custom_face", "sticker_id": st.ID, "result": data,
						})
					}
				}
				var err error
				if segs, err = buildSegments(store, ctx.Workspace, p.Segments); err != nil {
					return "", err
				}
			default:
				return "", fmt.Errorf("text 与 segments 必须提供一个")
			}
			return ctx.send(p.ChatType, p.ChatID, segs)
		},
	}
}
