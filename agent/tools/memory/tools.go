package memory

import (
	"Q-Mi/agent/tool"
	"Q-Mi/api"
	"Q-Mi/storage"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Tools 记忆板块：历史查询/搜索/消息图片查看。查询结果自动附带最新
// 几条消息的图片（images_attached 标注）；store/images 为 nil 时工具报错。
func Tools(ctx *Dependencies) []Tool {
	if ctx == nil {
		ctx = &Dependencies{}
	}
	store, images, turn := ctx.Store, ctx.Images, ctx.Turn
	return []Tool{
		{
			Name:        "get_recent_messages",
			Description: "查询指定会话最近 N 条，旧到新返回限长 JSON；省略会话时仅使用本轮单一会话，混合来源必须明确指定。遵循 chat_dnd 勿扰；完整存档用 search_messages。",
			Parameters: tool.ObjectParams(map[string]any{
				"chat_type": tool.StringProp("可选，按会话类型过滤：private / group"),
				"chat_id":   tool.IntProp("可选，按会话 ID 过滤：群号；私聊 = 对方 QQ"),
				"limit":     tool.IntProp("可选，返回条数，默认 50，最大 100"),
			}, nil),
			Usage: "某群最近 10 条：`{\"chat_type\":\"group\",\"chat_id\":888,\"limit\":10}`。返回格式与截断规则同 search_messages。",
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					ChatType string `json:"chat_type"`
					ChatID   int64  `json:"chat_id"`
					Limit    int    `json:"limit"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.ChatType == "" && p.ChatID == 0 && turn != nil {
					p.ChatType, p.ChatID = turn.ChatType, turn.ChatID
				}
				if (p.ChatType != "group" && p.ChatType != "private") || p.ChatID <= 0 {
					return "", fmt.Errorf("需要一起指定 chat_type(group/private) 和正数 chat_id；混合来源没有默认会话，请按相关消息来源查询")
				}
				limit := p.Limit
				if limit <= 0 {
					limit = 50
				}
				if limit > 100 {
					limit = 100
				}
				return QueryRows(store, images, turn, storage.MessageFilter{
					ChatType:  p.ChatType,
					ChatID:    p.ChatID,
					Limit:     limit,
					Attention: ctx.attention(turn),
				})
			},
		},
		searchMessagesTool(ctx),
		getMessageTool(ctx),
		{
			Name:        "view_message_images",
			Description: "查看一条消息里的图片内容，取到的图片会附在你的下一条消息里（多模态直送）。查询工具的结果会自动附带最新几条消息的图片，单条消息的图或自动附带跳过的图用这个工具补看。",
			Parameters: tool.ObjectParams(map[string]any{
				"message_id": tool.IntProp("消息 ID（从聊天记录或上下文里拿）"),
			}, []string{"message_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if store == nil {
					return "", errors.New("记忆库未启用，无法查看消息图片")
				}
				if images == nil {
					return "", errors.New("图片存储不可用，无法查看消息图片")
				}
				var p struct {
					MessageID int64 `json:"message_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.MessageID == 0 {
					return "", fmt.Errorf("message_id 不能为 0")
				}
				rec, err := store.GetMessage(p.MessageID)
				if err != nil {
					return "", fmt.Errorf("查询消息 %d 失败: %w", p.MessageID, err)
				}
				if rec == nil {
					return "", fmt.Errorf("没有找到消息 %d", p.MessageID)
				}
				// 解析 image 段：idx 对全部 image 段计数（与 storage 映射锚
				// 一致）；url 供下载，file 留作 get_image 换链
				var segs []struct {
					Type string `json:"type"`
					Data struct {
						URL  string `json:"url"`
						File string `json:"file"`
					} `json:"data"`
				}
				if len(rec.Segments) > 0 {
					if err := json.Unmarshal(rec.Segments, &segs); err != nil {
						return "", fmt.Errorf("解析消息 %d 的消息段失败: %w", p.MessageID, err)
					}
				}
				type imgSeg struct {
					idx       int
					url, file string
				}
				var imgs []imgSeg
				idx := 0
				for _, seg := range segs {
					if seg.Type != "image" {
						continue
					}
					if seg.Data.URL != "" || seg.Data.File != "" {
						imgs = append(imgs, imgSeg{idx: idx, url: seg.Data.URL, file: seg.Data.File})
					}
					idx++
				}
				if len(imgs) == 0 {
					return "", fmt.Errorf("消息 %d 里没有图片", p.MessageID)
				}
				// 逐张取 data URL：锚定解析（映射/磁盘命中零网络）→ 原 url
				// 直连下载 → 失败走 NapCat get_image 换新链（成功则同一锚点
				// 回填映射，下次零网络）→ 仍不可用（如换链只给 NapCat 侧本地
				// 路径，跨 WSL 读不了）则该图跳过并在结果里注明。
				var (
					dus   []string
					skips []string
				)
				for _, img := range imgs {
					du := ""
					if img.url != "" {
						du = images.DataURLFor(p.MessageID, img.idx, img.url) // 成功必为 data: 前缀；失败回退原 url
					}
					if !strings.HasPrefix(du, "data:") && img.file != "" {
						if data, err := ctx.call(&api.GetImage{File: img.file}); err == nil {
							var gi struct {
								File string `json:"file"`
								URL  string `json:"url"`
							}
							if jerr := json.Unmarshal(data, &gi); jerr == nil && isDownloadableURL(gi.URL) {
								if d2 := images.DataURLFor(p.MessageID, img.idx, gi.URL); strings.HasPrefix(d2, "data:") {
									du = d2
								}
							}
						}
					}
					if !strings.HasPrefix(du, "data:") {
						switch {
						case img.url == "":
							skips = append(skips, fmt.Sprintf("第 %d 张：消息段缺 url，换链也未取得可用链接", img.idx+1))
						case img.file == "":
							skips = append(skips, fmt.Sprintf("第 %d 张：原 url 下载失败且消息段没有 file 可换链", img.idx+1))
						default:
							skips = append(skips, fmt.Sprintf("第 %d 张：原 url 与 get_image 换链均不可用（可能只有 NapCat 侧本地路径），已跳过", img.idx+1))
						}
						continue
					}
					dus = append(dus, du)
					turn.AddPendingImage(du)
				}
				note := fmt.Sprintf("共 %d 张图片，已附在下一条消息", len(imgs))
				if len(dus) < len(imgs) {
					note = fmt.Sprintf("共 %d 张图片，其中 %d 张已附在下一条消息（其余已跳过，见 skipped）", len(imgs), len(dus))
				}
				out := map[string]any{
					"total":    len(imgs),
					"attached": len(dus),
					"note":     note,
				}
				if len(skips) > 0 {
					out["skipped"] = skips
				}
				return marshalCompact(out)
			},
		},
		chatDNDTool(ctx),
	}
}
