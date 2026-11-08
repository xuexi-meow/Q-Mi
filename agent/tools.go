// tools.go 定义 P2 工具集（spec §3）与 P3-2 QQ 能力工具、万能逃生舱。
// help 的输出直接从工具定义生成（单一源头）；工具 Run 返回紧凑 JSON 字符串，
// 出错返回 error 由调用方（manager）包装成 {"error": ...} 回填思维链。
// finish 无 Run，仅作为 manager 思考循环的终止标记。
package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"Q-Mi/api"
	apimsg "Q-Mi/api/message"
	"Q-Mi/storage"
	"Q-Mi/utils/request"
)

// Tool 一个可被 LLM 调用的工具。Run 收到原始 JSON 参数，返回紧凑 JSON
// 字符串；Run 为 nil 表示纯标记工具（finish），由循环特殊处理、不执行。
// Usage/Asyncable 与定义同处一地：加工具 = 写齐本结构体，文档与 schema 自动生成。
type Tool struct {
	Name        string
	Description string // 一句话（schema 用，模型选工具的第一线索）
	Parameters  any
	// Usage 完整使用说明（markdown）：参数明细/最小示例/错误形态。
	// 随 PromptDocs 注入 system 静态区；必须确定性（禁时间戳/随机）。
	Usage string
	// Asyncable 允许模型以 {"_async":true,...} 调用走后台任务（方案 B）。
	// 仅慢 IO 类工具声明；Asyncable 工具的 Usage 里必须写明异步语义。
	Asyncable bool
	Run       func(args json.RawMessage) (string, error)
}

// toolDefs 把工具集转成 OpenAI tools 请求参数。schema 全量极简（{"type":"object"}）：
// 参数细节由 system 工具文档承载（PromptDocs 从 Tool.Parameters 渲染），
// 详见 docs/specs/2026-11-08-tool-registry-design.md §7。
func toolDefs(tools []Tool) []ToolDef {
	defs := make([]ToolDef, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, ToolDef{
			Type: "function",
			Function: ToolSchema{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  map[string]any{"type": "object"},
			},
		})
	}
	return defs
}

// objectParams 生成 {"type":"object","properties":...} 形态的参数 schema。
// required 为空时省略（部分 OpenAI 兼容提供商拒绝 "required": null），
// properties 为空时给空对象（同样不能是 null）。
func objectParams(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// strProp / intProp 属性快捷构造。
func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

// memoryTools 记忆板块：历史查询/搜索/消息图片查看。查询结果自动附带最新
// 几条消息的图片（images_attached 标注）；store/images 为 nil 时工具报错。
func memoryTools(ctx *ToolContext) []Tool {
	store, images, turn := ctx.Store, ctx.Images, ctx.Turn
	return []Tool{
		{
			Name:        "get_recent_messages",
			Description: "查询最近的聊天记录（跨会话混合，按时间正序返回 JSON 数组）。",
			Parameters: objectParams(map[string]any{
				"chat_type": strProp("可选，按会话类型过滤：private / group"),
				"chat_id":   intProp("可选，按会话 ID 过滤：群号；私聊 = 对方 QQ"),
				"limit":     intProp("可选，返回条数，默认 50，最大 100"),
			}, nil),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					ChatType string `json:"chat_type"`
					ChatID   int64  `json:"chat_id"`
					Limit    int    `json:"limit"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				limit := p.Limit
				if limit <= 0 {
					limit = 50
				}
				if limit > 100 {
					limit = 100
				}
				return queryMessageRows(store, images, turn, storage.MessageFilter{
					ChatType: p.ChatType,
					ChatID:   p.ChatID,
					Limit:    limit,
				})
			},
		},
		{
			Name:        "search_messages",
			Description: "按关键词全文搜索历史聊天记录（按时间正序返回 JSON 数组）。",
			Parameters: objectParams(map[string]any{
				"query":     strProp("搜索关键词"),
				"chat_type": strProp("可选，按会话类型过滤：private / group"),
				"chat_id":   intProp("可选，按会话 ID 过滤：群号；私聊 = 对方 QQ"),
				"limit":     intProp("可选，返回条数，默认 20，最大 50"),
			}, []string{"query"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Query    string `json:"query"`
					ChatType string `json:"chat_type"`
					ChatID   int64  `json:"chat_id"`
					Limit    int    `json:"limit"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.Query == "" {
					return "", fmt.Errorf("query 不能为空")
				}
				limit := p.Limit
				if limit <= 0 {
					limit = 20
				}
				if limit > 50 {
					limit = 50
				}
				return queryMessageRows(store, images, turn, storage.MessageFilter{
					ChatType: p.ChatType,
					ChatID:   p.ChatID,
					Limit:    limit,
				}, p.Query)
			},
		},
		{
			Name:        "view_message_images",
			Description: "查看一条消息里的图片内容，取到的图片会附在你的下一条消息里（多模态直送）。查询工具的结果会自动附带最新几条消息的图片，单条消息的图或自动附带跳过的图用这个工具补看。",
			Parameters: objectParams(map[string]any{
				"message_id": intProp("消息 ID（从聊天记录或上下文里拿）"),
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
						du = images.dataURLFor(p.MessageID, img.idx, img.url) // 成功必为 data: 前缀；失败回退原 url
					}
					if !strings.HasPrefix(du, "data:") && img.file != "" {
						if data, err := callNapCat(&api.GetImage{File: img.file}); err == nil {
							var gi struct {
								File string `json:"file"`
								URL  string `json:"url"`
							}
							if jerr := json.Unmarshal(data, &gi); jerr == nil && isDownloadableURL(gi.URL) {
								if d2 := images.dataURLFor(p.MessageID, img.idx, gi.URL); strings.HasPrefix(d2, "data:") {
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
	}
}

// coreTools 核心板块：发消息、思考笔记、情绪、结束。finish 是唯一的轮终止工具。
func coreTools(ctx *ToolContext) []Tool {
	store, notesPath, statePath := ctx.Store, ctx.NotesPath, ctx.StatePath
	return []Tool{
		{
			Name:        "send_message",
			Description: "给指定会话发送一条消息：text 纯文本，或 segments 富消息段数组（at/reply/face/mface/sticker/dice/rps/json/image），两者二选一。一个思考轮里可多次调用实现分条发送；不调用则不回复。",
			Parameters: objectParams(map[string]any{
				"chat_type": strProp("会话类型：private / group"),
				"chat_id":   intProp("会话 ID：群号；私聊 = 对方 QQ"),
				"text":      strProp("要发送的纯文本内容（与 segments 二选一）"),
				"segments": map[string]any{
					"type":        "array",
					"description": "富消息段数组（与 text 二选一），按顺序发送。段类型白名单：text/at/reply/face/mface/sticker/dice/rps/json/image",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type": strProp("段类型：text / at / reply / face / mface / sticker / dice / rps / json / image"),
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
					Segments []segmentParam `json:"segments"`
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
					var err error
					if segs, err = buildSegments(store, ctx.Workspace, p.Segments); err != nil {
						return "", err
					}
				default:
					return "", fmt.Errorf("text 与 segments 必须提供一个")
				}
				return sendSegments(p.ChatType, p.ChatID, segs)
			},
		},
		{
			Name:        "think",
			Description: "记一条思考笔记，持久化后下一轮对话会注入上下文，用于跨轮次保持思路连贯。",
			Parameters: objectParams(map[string]any{
				"note": strProp("笔记内容"),
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
				if err := AppendNote(notesPath, p.Note); err != nil {
					return "", fmt.Errorf("写思考笔记失败: %w", err)
				}
				return "{}", nil
			},
		},
		{
			Name:        "update_mood",
			Description: "更新自己的当前情绪状态，会注入到之后的对话开头；只在情绪真的变化时调用。",
			Parameters: objectParams(map[string]any{
				"mood": strProp("新的情绪状态，如 开心 / 无聊 / 有点生气"),
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
				return "{}", writeState(statePath, p.Mood)
			},
		},
		{
			Name:        "finish",
			Description: "结束本轮思考。可以发了消息再结束，也可以什么都不发直接结束；调用后本轮终止。",
			Parameters:  objectParams(nil, nil),
			Usage:       "无参数。finish 前已发出的消息均已生效；不调用 send_message 直接 finish = 本轮不回复。",
		},
	}
}

// stickerTools 表情收藏板块：本地收藏夹 + QQ 账号级收藏表情。
func stickerTools(ctx *ToolContext) []Tool {
	store := ctx.Store
	return []Tool{
		{
			Name:        "save_sticker",
			Description: "把一条消息里的表情/图片收藏起来（自带表情 face、商城表情 mface、图片 image），之后可用 send_message 的 sticker 段随时发出去。seg_index 是该消息里第几个消息段（从 0 开始，默认 0）。",
			Parameters: objectParams(map[string]any{
				"message_id": intProp("消息 ID（从聊天记录或上下文里拿）"),
				"seg_index":  intProp("可选，消息段下标（从 0 开始），默认 0"),
			}, []string{"message_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if store == nil {
					return "", errors.New("记忆库未启用，无法收藏表情")
				}
				var p struct {
					MessageID int64 `json:"message_id"`
					SegIndex  int   `json:"seg_index"`
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
				var segs []struct {
					Type string          `json:"type"`
					Data json.RawMessage `json:"data"`
				}
				if len(rec.Segments) > 0 {
					if err := json.Unmarshal(rec.Segments, &segs); err != nil {
						return "", fmt.Errorf("解析消息 %d 的消息段失败: %w", p.MessageID, err)
					}
				}
				if p.SegIndex < 0 || p.SegIndex >= len(segs) {
					return "", fmt.Errorf("seg_index %d 超出范围（该消息共 %d 段）", p.SegIndex, len(segs))
				}
				seg := segs[p.SegIndex]
				if seg.Type != "face" && seg.Type != "mface" && seg.Type != "image" {
					return "", fmt.Errorf("第 %d 段类型 %q 不能收藏，只支持 face/mface/image", p.SegIndex, seg.Type)
				}
				var data map[string]any
				if len(seg.Data) > 0 {
					if err := json.Unmarshal(seg.Data, &data); err != nil {
						return "", fmt.Errorf("解析消息段 data 失败: %w", err)
					}
				}
				st := storage.Sticker{Kind: seg.Type}
				switch seg.Type {
				case "face":
					st.FaceID, err = segFlex(data, "id", p.SegIndex)
					if err != nil {
						return "", err
					}
					if n, convErr := strconv.Atoi(st.FaceID); convErr == nil {
						st.Summary = QQFaceNames[n] // 内置对照表里的表情名
					}
				case "mface":
					if st.EmojiID, err = segFlex(data, "emoji_id", p.SegIndex); err != nil {
						return "", err
					}
					if st.EmojiPackageID, err = segFlex(data, "emoji_package_id", p.SegIndex); err != nil {
						return "", err
					}
					st.Key = anyString(data["key"])
					st.Summary = anyString(data["summary"])
				case "image":
					st.URL = anyString(data["url"])
					if st.URL == "" {
						return "", fmt.Errorf("第 %d 段 image 没有图片 url，无法收藏", p.SegIndex)
					}
					st.Summary = anyString(data["summary"])
				}
				id, err := store.AddSticker(st)
				if err != nil {
					return "", err
				}
				return marshalCompact(map[string]any{"sticker_id": id, "kind": st.Kind, "summary": st.Summary})
			},
		},
		{
			Name:        "list_stickers",
			Description: "列出你收藏的全部表情（id/kind/summary），用于选择 sticker 段要发的 id。",
			Parameters:  objectParams(nil, nil),
			Run: func(json.RawMessage) (string, error) {
				if store == nil {
					return "", errors.New("记忆库未启用，无法查询收藏")
				}
				list, err := store.ListStickers()
				if err != nil {
					return "", err
				}
				rows := make([]map[string]any, 0, len(list))
				for _, st := range list {
					rows = append(rows, map[string]any{"id": st.ID, "kind": st.Kind, "summary": st.Summary})
				}
				return marshalCompact(rows)
			},
		},
		{
			Name:        "delete_sticker",
			Description: "删除一条收藏的表情（传 list_stickers 里的 id）。收藏错了用它清理。",
			Parameters: objectParams(map[string]any{
				"sticker_id": intProp("收藏表情的 id"),
			}, []string{"sticker_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if store == nil {
					return "", errors.New("记忆库未启用，无法删除收藏")
				}
				var p struct {
					StickerID int64 `json:"sticker_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.StickerID == 0 {
					return "", fmt.Errorf("sticker_id 不能为 0")
				}
				st, err := store.GetSticker(p.StickerID)
				if err != nil {
					return "", err
				}
				if st == nil {
					return "", fmt.Errorf("没有找到表情 %d（可能已删除）", p.StickerID)
				}
				if err := store.DeleteSticker(p.StickerID); err != nil {
					return "", err
				}
				return marshalCompact(map[string]any{"deleted": true})
			},
		},
		{
			Name:        "collect_qq_sticker",
			Description: "把一个网络图片（http/https 链接）下载并加入 QQ 的收藏表情（QQ 账号自带的收藏夹，不是本列表）：先让 NapCat 下载到本地，再加入收藏。适合把好看的图变成账号级表情。",
			Parameters: objectParams(map[string]any{
				"url": strProp("图片链接（http/https）"),
			}, []string{"url"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					URL string `json:"url"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.URL == "" {
					return "", fmt.Errorf("缺少必填参数 url")
				}
				// 第一步：download_file 让 NapCat 自己下载（文件须落在 NapCat 机器上）
				data, err := callNapCat(&api.DownloadFile{URL: p.URL})
				if err != nil {
					return "", fmt.Errorf("下载图片失败: %w", err)
				}
				path := downloadFilePath(data)
				if path == "" {
					return "", fmt.Errorf("下载结果缺少文件路径: %s", data)
				}
				// 第二步：add_custom_face（file 是 NapCat 机器上的本地路径）
				if _, err := callNapCat(&api.AddCustomFace{File: path}); err != nil {
					return "", fmt.Errorf("加入 QQ 收藏失败: %w", err)
				}
				return marshalCompact(map[string]any{"path": path, "added": true})
			},
		},
	}
}

// systemTools 系统板块：定时器、后台任务管理、行为回溯。
func systemTools(ctx *ToolContext) []Tool {
	timer := ctx.Timer
	// schedule 的会话上下文在构造期从 turn 捕获（工具 Run 拿不到 turn）。
	var curChatType string
	var curChatID, curSelfID int64
	if turn := ctx.Turn; turn != nil {
		curChatType, curChatID, curSelfID = turn.ChatType, turn.ChatID, turn.SelfID
	}
	tools := []Tool{
		// P3-5 定时器工具：到点经 fire 回调合成为 [定时提醒] 消息排队唤醒。
		{
			Name:        "schedule",
			Description: "定一个一次性闹钟：delay_seconds 秒后你会收到一条 [定时提醒] 开头的消息（note 原样带给你）。提醒走正常消息队列排队，不会打断你正在做的思考。适合控制发言节奏、延时跟进（如「10分钟后看看他回了没」）、定时打卡；循环任务到点后自己再 schedule 一次。",
			Parameters: objectParams(map[string]any{
				"delay_seconds": intProp("多少秒后提醒，1 ~ 604800（最长 7 天）"),
				"note":          strProp("到点提醒内容：写清楚到时要做什么、发给谁（会话与内容）"),
			}, []string{"delay_seconds", "note"}),
			Usage: "示例——十分钟后在当前群跟进：\n`{\"delay_seconds\":600,\"note\":\"在群 928952016 看看他回了没，回了就接话\"}`\n错误形态：delay 超出 1~604800 或 note 为空 → 报错回填。",
			Run: func(args json.RawMessage) (string, error) {
				if timer == nil {
					return "", errors.New("定时器不可用")
				}
				var p struct {
					DelaySeconds int64  `json:"delay_seconds"`
					Note         string `json:"note"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.Note == "" {
					return "", fmt.Errorf("缺少必填参数 note")
				}
				if p.DelaySeconds < 1 || p.DelaySeconds > 604800 {
					return "", fmt.Errorf("delay_seconds 需在 1 ~ 604800 秒之间（最长 7 天），got %d", p.DelaySeconds)
				}
				rec, err := timer.Schedule(time.Duration(p.DelaySeconds)*time.Second, p.Note, curChatType, curChatID, curSelfID)
				if err != nil {
					return "", err
				}
				return marshalCompact(map[string]any{
					"id":      rec.ID,
					"fire_at": rec.FireAt.Format("01-02 15:04:05"),
				})
			},
		},
		{
			Name:        "cancel_timer",
			Description: "取消一个还没到点的定时提醒（传 schedule 返回的 id）。已触发或已取消的 id 会报错。",
			Parameters: objectParams(map[string]any{
				"timer_id": strProp("schedule 返回的定时器 id"),
			}, []string{"timer_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if timer == nil {
					return "", errors.New("定时器不可用")
				}
				var p struct {
					TimerID string `json:"timer_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.TimerID == "" {
					return "", fmt.Errorf("缺少必填参数 timer_id")
				}
				if !timer.Cancel(p.TimerID) {
					return "", fmt.Errorf("没有找到定时器 %q（可能已触发或已取消）", p.TimerID)
				}
				return marshalCompact(map[string]any{"cancelled": true})
			},
		},
	}
	// 后台任务管理（方案 B 的可见性/控制面）。
	tasks := ctx.Tasks
	tools = append(tools,
		Tool{
			Name:        "task_list",
			Description: "查看你的后台任务列表（_async 发起的任务）：id/工具/状态/耗时/参数摘要。结果全文用 task_get。",
			Parameters: objectParams(map[string]any{
				"status": strProp("可选，按状态过滤：running / done / failed / cancelled / all，缺省 all"),
			}, nil),
			Run: func(args json.RawMessage) (string, error) {
				if tasks == nil {
					return "", errors.New("后台任务不可用")
				}
				var p struct {
					Status string `json:"status"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				list := tasks.List(p.Status)
				now := time.Now()
				rows := make([]map[string]any, 0, len(list))
				for _, t := range list {
					row := map[string]any{
						"id": t.ID, "tool": t.Tool, "status": t.Status,
						"args": t.ArgsBrief,
					}
					end := now
					if t.DoneAt != nil {
						end = *t.DoneAt
					}
					row["elapsed_seconds"] = int(end.Sub(t.StartedAt).Seconds())
					if t.Err != "" {
						row["err"] = t.Err
					}
					rows = append(rows, row)
				}
				return marshalCompact(rows)
			},
		},
		Tool{
			Name:        "task_get",
			Description: "查一个后台任务的详情（含完成结果全文）。",
			Parameters: objectParams(map[string]any{
				"task_id": intProp("task_list 里的任务 id"),
			}, []string{"task_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if tasks == nil {
					return "", errors.New("后台任务不可用")
				}
				var p struct {
					TaskID int64 `json:"task_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				t, ok := tasks.Get(p.TaskID)
				if !ok {
					return "", fmt.Errorf("没有找到任务 %d", p.TaskID)
				}
				return marshalCompact(t)
			},
		},
		Tool{
			Name:        "task_cancel",
			Description: "停止一个还在运行的后台任务（结果不再投递）。已完成/已取消的任务会报错。",
			Parameters: objectParams(map[string]any{
				"task_id": intProp("task_list 里的任务 id"),
			}, []string{"task_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if tasks == nil {
					return "", errors.New("后台任务不可用")
				}
				var p struct {
					TaskID int64 `json:"task_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				t, err := tasks.Cancel(p.TaskID)
				if err != nil {
					return "", err
				}
				return marshalCompact(map[string]any{"cancelled": true, "task_id": t.ID, "tool": t.Tool})
			},
		},
	)

	// 行为回溯：EventLog（与 webui 思考链同源）暴露给模型。
	events := ctx.Events
	tools = append(tools,
		Tool{
			Name:        "self_events",
			Description: "回溯你自己的近期行为流（思考/工具调用/输出/回复，按时间倒序）。用于回忆上一轮做了什么、为什么没回、调过什么工具，防止前后割裂。只覆盖最近几百条事件，更早的靠消息库与思考笔记。",
			Parameters: objectParams(map[string]any{
				"kind":    strProp("可选，按类型过滤：turn_start / reasoning / output / tool_call / tool_result / reply / finish / usage / interrupted / error"),
				"chat_id": intProp("可选，按会话 ID 过滤"),
				"limit":   intProp("可选，返回条数，默认 50，最大 200"),
			}, nil),
			Run: func(args json.RawMessage) (string, error) {
				if events == nil {
					return "", errors.New("行为事件流不可用")
				}
				var p struct {
					Kind   string `json:"kind"`
					ChatID int64  `json:"chat_id"`
					Limit  int    `json:"limit"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				limit := p.Limit
				if limit <= 0 {
					limit = 50
				}
				if limit > 200 {
					limit = 200
				}
				all := events.After(0)
				var picked []Event
				for i := len(all) - 1; i >= 0 && len(picked) < limit; i-- { // 新→旧
					e := all[i]
					if p.Kind != "" && e.Kind != p.Kind {
						continue
					}
					if p.ChatID != 0 && e.ChatID != p.ChatID {
						continue
					}
					picked = append(picked, e)
				}
				lines := make([]string, 0, len(picked))
				for _, e := range picked {
					text := strings.ReplaceAll(e.Text, "\n", " ")
					lines = append(lines, fmt.Sprintf("#%d [%s] %s %s: %s",
						e.ID, e.Time.Format(timeLayout), chatSource(e.ChatType, e.ChatID), e.Kind, text))
				}
				return strings.Join(lines, "\n"), nil
			},
		},
	)
	return tools
}

// decodeArgs 解析工具参数；空参数视为无参。
func decodeArgs(args json.RawMessage, v any) error {
	if len(args) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("工具参数不是合法 JSON: %w", err)
	}
	return nil
}

// marshalCompact 序列化为紧凑 JSON（map 键自动排序，无缩进）。
func marshalCompact(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("序列化工具结果失败: %w", err)
	}
	return string(data), nil
}

// maxAutoAttachImages 查询工具单次自动附图上限（多模态直送下一条消息）。
const maxAutoAttachImages = 5

// queryMessageRows 查询（可选 FTS 关键词）并把结果序列化为与上下文行
// 同构的紧凑 JSON 数组（旧→新）：message_id/chat_type/chat_id/sender_name/
// text/time，带图行额外有 images（图片张数）。最新消息的图片自动解析并附在
// 下一条消息里（turn/images 非 nil 时），单次最多 maxAutoAttachImages 张，
// 附上的行带 images_attached 标记——查到具体消息即看图，URL 失效的旧图自动
// 跳过（可再调 view_message_images 走换链兜底）。
func queryMessageRows(store *storage.Store, images *imageStore, turn *Turn, f storage.MessageFilter, keyword ...string) (string, error) {
	if store == nil {
		return "", fmt.Errorf("记忆库未启用，无法查询消息")
	}
	var (
		records []*storage.MessageRecord
		err     error
	)
	if len(keyword) > 0 {
		records, err = store.SearchMessages(keyword[0], f)
	} else {
		records, err = store.QueryMessages(f)
	}
	if err != nil {
		return "", fmt.Errorf("查询消息失败: %w", err)
	}
	rows := make([]map[string]any, 0, len(records))
	for i := len(records) - 1; i >= 0; i-- { // 倒序 → 正序（旧→新）
		r := records[i]
		row := map[string]any{
			"message_id":  r.MessageID,
			"chat_type":   r.ChatType,
			"chat_id":     r.ChatID,
			"sender_name": r.SenderName,
			"text":        r.RawMessage,
			"time":        time.Unix(int64(r.Time), 0).Format(timeLayout),
		}
		if refs := extractImageRefs(r.Segments); len(refs) > 0 {
			row["images"] = len(refs)
		}
		rows = append(rows, row)
	}
	// 自动附图：从最新记录往前解析（records 为时间倒序，最新在前）
	if turn != nil && images != nil {
		attached := 0
		for i := 0; i < len(records) && attached < maxAutoAttachImages; i++ {
			r := records[i]
			refs := extractImageRefs(r.Segments)
			if len(refs) == 0 {
				continue
			}
			row := rows[len(records)-1-i] // 正序行数组里对应本条记录的下标
			for _, ref := range refs {
				if attached >= maxAutoAttachImages {
					break
				}
				du := images.dataURLFor(r.MessageID, ref.Idx, ref.URL)
				if !strings.HasPrefix(du, "data:") { // URL 失效等，跳过
					continue
				}
				turn.AddPendingImage(du)
				row["images_attached"] = true
				attached++
			}
		}
	}
	return marshalCompact(rows)
}

// maxSendMessageTextRunes send_message 单次调用 text 内容的累计上限
// （rune 计），纯 text 与 segments 里的 text 段共用该额度。
const maxSendMessageTextRunes = 8000

// segmentParam send_message segments 数组里的单个消息段参数。
type segmentParam struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

// buildSegments 把 segments 参数转成消息段数组（P2.6）。只接受白名单类型，
// 白名单外报错；text 段内容累计超过 maxSendMessageTextRunes 报错；
// image/video/file 段的 file 若为工作区相对路径自动映射为 NapCat(WSL) 侧
// /mnt/<盘符>/... 路径（模型不需要知道 WSL 的存在），http/https/base64://
// 原样透传；sticker 段查 storage 收藏表转换，需要 store 非 nil。
func buildSegments(store *storage.Store, workspace string, segs []segmentParam) ([]*apimsg.Message, error) {
	out := make([]*apimsg.Message, 0, len(segs))
	textLen := 0
	for i, s := range segs {
		at := i + 1
		switch s.Type {
		case "text":
			v, err := segString(s.Data, "text", at)
			if err != nil {
				return nil, err
			}
			if v == "" {
				return nil, fmt.Errorf("第 %d 段 text 不能为空", at)
			}
			textLen += len([]rune(v))
			if textLen > maxSendMessageTextRunes {
				return nil, fmt.Errorf("text 内容累计 %d 字符，超过单次上限 %d", textLen, maxSendMessageTextRunes)
			}
			out = append(out, apimsg.Text(v))
		case "at":
			v, err := segString(s.Data, "qq", at)
			if err != nil {
				return nil, err
			}
			out = append(out, apimsg.At(v))
		case "reply":
			v, err := segString(s.Data, "id", at)
			if err != nil {
				return nil, err
			}
			out = append(out, apimsg.Reply(v))
		case "face":
			v, err := segString(s.Data, "id", at)
			if err != nil {
				return nil, err
			}
			out = append(out, apimsg.Face(v))
		case "dice":
			v, err := segString(s.Data, "result", at)
			if err != nil {
				return nil, err
			}
			out = append(out, apimsg.Dice(v))
		case "rps":
			v, err := segString(s.Data, "result", at)
			if err != nil {
				return nil, err
			}
			out = append(out, apimsg.RPS(v))
		case "json":
			data, ok := s.Data["data"]
			if !ok {
				return nil, fmt.Errorf("第 %d 段 json 缺少 %q 字段", at, "data")
			}
			out = append(out, apimsg.Json(data))
		case "image":
			file, err := segString(s.Data, "file", at)
			if err != nil {
				return nil, err
			}
			if mapped, merr := napcatFilePath(workspace, file); merr == nil {
				file = mapped
			}
			var summary *string
			if raw, ok := s.Data["summary"]; ok {
				str, ok := raw.(string)
				if !ok {
					return nil, fmt.Errorf("第 %d 段 image 的 %q 应为字符串", at, "summary")
				}
				summary = &str
			}
			out = append(out, apimsg.Image(file, summary, nil))
		case "mface":
			// 商城表情：emoji_id/package_id/key 必填（数字或字符串形态都收），
			// summary 可选；透传构造 apimsg.Message 让 NapCat 处理。
			emojiID, err := segFlex(s.Data, "emoji_id", at)
			if err != nil {
				return nil, err
			}
			pkg, err := segFlex(s.Data, "emoji_package_id", at)
			if err != nil {
				return nil, err
			}
			key, err := segFlex(s.Data, "key", at)
			if err != nil {
				return nil, err
			}
			var summary *string
			if raw, ok := s.Data["summary"]; ok {
				str, ok := raw.(string)
				if !ok {
					return nil, fmt.Errorf("第 %d 段 mface 的 %q 应为字符串", at, "summary")
				}
				summary = &str
			}
			out = append(out, apimsg.MFace(emojiID, pkg, &key, summary))
		case "sticker":
			// 发自己收藏的表情：查 stickers 表后按 kind 转成对应段。
			if store == nil {
				return nil, fmt.Errorf("第 %d 段 sticker 需要查询收藏，但记忆库未启用", at)
			}
			id, err := segID(s.Data, "id", at)
			if err != nil {
				return nil, err
			}
			seg, err := stickerSegment(store, id, at)
			if err != nil {
				return nil, err
			}
			out = append(out, seg)
		default:
			return nil, fmt.Errorf("第 %d 段不支持的消息段类型 %q，可用：text/at/reply/face/mface/sticker/dice/rps/json/image", at, s.Type)
		}
	}
	return out, nil
}

// segFlex 取消息段 data 里的必填字段，兼容字符串与数字两种 JSON 形态
// （NapCat 上报的 emoji_id/emoji_package_id 等在版本间互换形态）。
func segFlex(data map[string]any, key string, at int) (string, error) {
	raw, ok := data[key]
	if !ok {
		return "", fmt.Errorf("第 %d 段缺少 %q 字段", at, key)
	}
	switch v := raw.(type) {
	case string:
		return v, nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	default:
		return "", fmt.Errorf("第 %d 段的 %q 应为字符串或数字", at, key)
	}
}

// segID 取消息段 data 里的必填整数（数字或数字字符串形态都收）。
func segID(data map[string]any, key string, at int) (int64, error) {
	raw, ok := data[key]
	if !ok {
		return 0, fmt.Errorf("第 %d 段缺少 %q 字段", at, key)
	}
	switch v := raw.(type) {
	case float64:
		return int64(v), nil
	case string:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("第 %d 段的 %q 应为整数, got %q", at, key, v)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("第 %d 段的 %q 应为整数", at, key)
	}
}

// anyString 宽松取字符串：字符串原样，数字转十进制，其余（含缺失）返回空串。
func anyString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	}
	return ""
}

// stickerSegment 把收藏的表情转成可发送的消息段：face→face 段、
// mface→mface 段（key 必需，缺失报错）、image→image 段（file=url）。
func stickerSegment(store *storage.Store, id int64, at int) (*apimsg.Message, error) {
	st, err := store.GetSticker(id)
	if err != nil {
		return nil, fmt.Errorf("查询表情 %d 失败: %w", id, err)
	}
	if st == nil {
		return nil, fmt.Errorf("第 %d 段: 没有找到表情 %d（用 list_stickers 查看收藏）", at, id)
	}
	var summary *string
	if st.Summary != "" {
		summary = &st.Summary
	}
	switch st.Kind {
	case "face":
		return apimsg.Face(st.FaceID), nil
	case "mface":
		if st.Key == "" {
			return nil, fmt.Errorf("第 %d 段: 表情 %d 缺少 key，无法发送", at, id)
		}
		return apimsg.MFace(st.EmojiID, st.EmojiPackageID, &st.Key, summary), nil
	case "image":
		return apimsg.Image(st.URL, summary, nil), nil
	default:
		return nil, fmt.Errorf("第 %d 段: 表情 %d 类型未知 %q", at, id, st.Kind)
	}
}

// segString 取消息段 data 里的必填字符串字段，缺失或类型不符报错。
func segString(data map[string]any, key string, at int) (string, error) {
	raw, ok := data[key]
	if !ok {
		return "", fmt.Errorf("第 %d 段缺少 %q 字段", at, key)
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("第 %d 段的 %q 应为字符串", at, key)
	}
	return s, nil
}

// sendSegments 经 NapCat 发送消息段链并返回 {"message_id":N}
// （NapCat 调用是短请求，用独立 Background ctx，打断逻辑在 P2 后续处理）。
func sendSegments(chatType string, chatID int64, segs []*apimsg.Message) (string, error) {
	ctx := context.Background()
	var (
		resp *api.Response[api.SendMsgData]
		err  error
	)
	if chatType == "group" {
		resp, err = request.SendGroupMsg(ctx, chatID, segs...)
	} else {
		resp, err = request.SendPrivateMsg(ctx, chatID, segs...)
	}
	if err != nil {
		return "", err
	}
	return marshalCompact(map[string]any{"message_id": resp.Data.MessageID})
}

// agentState state.json 的内容结构。
type agentState struct {
	Mood    string    `json:"mood"`
	Updated time.Time `json:"updated"`
}

// readStateFrom 读取状态文件；文件不存在返回零值状态。
func readStateFrom(path string) (agentState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return agentState{}, nil
		}
		return agentState{}, err
	}
	var s agentState
	if err := json.Unmarshal(data, &s); err != nil {
		return agentState{}, err
	}
	return s, nil
}

// writeState 写入情绪状态；mood 与现值相同则不写盘（避免无谓的 mtime 抖动）。
func writeState(path, mood string) error {
	cur, err := readStateFrom(path)
	if err != nil {
		return fmt.Errorf("读取状态文件失败: %w", err)
	}
	if cur.Mood == mood {
		return nil
	}
	data, err := json.Marshal(agentState{Mood: mood, Updated: time.Now()})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// ================= P3-2：QQ 能力工具与万能逃生舱 =================

// errAdminRequired 非管理员调用危险操作的错误文案（回填思维链，模型能看懂）。
var errAdminRequired = errors.New("此操作需要管理员权限")

// dangerousNapcatActions napcat_api 逃生舱中需要管理员权限的动作集合。
var dangerousNapcatActions = map[string]bool{
	"set_group_kick":   true,
	"set_group_leave":  true,
	"delete_friend":    true,
	"set_qq_avatar":    true,
	"set_qq_profile":   true,
	"set_group_name":   true,
	"delete_qzone_msg": true, // P4：删说说（工具级门槛见 delete_qzone）
}

// maxBanDuration 单次禁言时长上限：30 天（秒）。
const maxBanDuration = 2592000

// maxMembersReturned lookup_members 单次返回的成员条数上限（防撑爆上下文）。
const maxMembersReturned = 100

// maxMsgHistoryCount get_msg_history 单次返回条数上限。
const maxMsgHistoryCount = 20

// maxFeedsReturned browse_qzone_feeds 单次返回的动态条数上限。
const maxFeedsReturned = 10

// maxQzoneCommentsReturned read_qzone_post 单次返回的评论条数上限。
const maxQzoneCommentsReturned = 10

// maxQzoneRepliesReturned read_qzone_post 单条评论附带的楼中楼回复上限。
const maxQzoneRepliesReturned = 3

// maxQzoneSummaryRunes 空间动态摘要 / 评论 / 回复单条文本的长度上限（rune 计）。
const maxQzoneSummaryRunes = 100

// maxQzonePostContentRunes read_qzone_post 说说正文的长度上限（rune 计）。
const maxQzonePostContentRunes = 200

// truncateRunes 按 rune 截断字符串到 max 长度（空间工具的限长裁剪）。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// callNapCat 用独立 ctx 调一次 NapCat API 并返回 data（RawMessage 原样透传）。
// 出错时返回 request 层的错误（含 retcode/wording，模型可据此调整）。
func callNapCat(req api.Request) (json.RawMessage, error) {
	resp, err := request.Call(context.Background(), req)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// isDownloadableURL 判断是否为 Q-Mi 可直接下载的 http(s) 链接。NapCat 的
// get_image 在拿不到图片外链时 url 字段会回退为 NapCat 侧本地路径（NapCat
// 常驻 WSL、Q-Mi 在 Windows，跨系统读不了），不能当下载链接用。
func isDownloadableURL(s string) bool {
	low := strings.ToLower(s)
	return strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://")
}

// downloadFilePath 从 download_file 的 data 里取本地文件路径：
// 兼容 {"path":...} / {"file":...} / 裸字符串三种返回形态。
func downloadFilePath(data json.RawMessage) string {
	if len(data) == 0 {
		return ""
	}
	var obj struct {
		Path string `json:"path"`
		File string `json:"file"`
	}
	if err := json.Unmarshal(data, &obj); err == nil {
		if obj.Path != "" {
			return obj.Path
		}
		if obj.File != "" {
			return obj.File
		}
	}
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		return s
	}
	return ""
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

// qqTools admin 板块：QQ 能力工具（群/好友/文件/请求处理）与 napcat 逃生舱。
// ctx.Admin 决定危险工具是否放行；ctx.Workspace 用于本地文件路径映射。
func qqTools(ctx *ToolContext) []Tool {
	admin := ctx.Admin
	// requireAdmin 危险工具的统一门槛。
	requireAdmin := func() error {
		if !admin {
			return errAdminRequired
		}
		return nil
	}

	return []Tool{
		{
			Name:        "recall_message",
			Description: "撤回一条消息（群消息需要自己是管理员或发送者）。传消息的 message_id。",
			Parameters: objectParams(map[string]any{
				"message_id": intProp("要撤回的消息 ID"),
			}, []string{"message_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					MessageID int64 `json:"message_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.MessageID == 0 {
					return "", fmt.Errorf("message_id 不能为 0")
				}
				return emptyOK(callNapCat(&api.DeleteMsg{MessageID: p.MessageID}))
			},
		},
		{
			Name:        "get_friend_list",
			Description: "获取好友列表（user_id/nickname/remark）。",
			Parameters:  objectParams(nil, nil),
			Run: func(json.RawMessage) (string, error) {
				data, err := callNapCat(&api.GetFriendList{})
				if err != nil {
					return "", err
				}
				var friends []struct {
					UserID   int64  `json:"user_id"`
					Nickname string `json:"nickname"`
					Remark   string `json:"remark"`
				}
				if err := json.Unmarshal(data, &friends); err != nil {
					return "", fmt.Errorf("解析好友列表失败: %w", err)
				}
				rows := make([]map[string]any, 0, len(friends))
				for _, f := range friends {
					rows = append(rows, map[string]any{
						"user_id": f.UserID, "nickname": f.Nickname, "remark": f.Remark,
					})
				}
				return marshalCompact(rows)
			},
		},
		{
			Name:        "lookup_user",
			Description: "查一个 QQ 的资料（昵称、年龄、等级等）。传对方 QQ 号。",
			Parameters: objectParams(map[string]any{
				"qq": intProp("对方 QQ 号"),
			}, []string{"qq"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					QQ int64 `json:"qq"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.QQ == 0 {
					return "", fmt.Errorf("缺少必填参数 qq（且不能为 0）")
				}
				return passthrough(callNapCat(&api.GetStrangerInfo{UserID: p.QQ}))
			},
		},
		{
			Name:        "lookup_groups",
			Description: "获取自己加入的群列表（group_id/group_name/member_count）。",
			Parameters:  objectParams(nil, nil),
			Run: func(json.RawMessage) (string, error) {
				data, err := callNapCat(&api.GetGroupList{})
				if err != nil {
					return "", err
				}
				var groups []struct {
					GroupID     int64  `json:"group_id"`
					GroupName   string `json:"group_name"`
					MemberCount int    `json:"member_count"`
				}
				if err := json.Unmarshal(data, &groups); err != nil {
					return "", fmt.Errorf("解析群列表失败: %w", err)
				}
				rows := make([]map[string]any, 0, len(groups))
				for _, g := range groups {
					rows = append(rows, map[string]any{
						"group_id": g.GroupID, "group_name": g.GroupName, "member_count": g.MemberCount,
					})
				}
				return marshalCompact(rows)
			},
		},
		{
			Name:        "lookup_group",
			Description: "查一个群的详细信息（基本信息 + 扩展信息合并）。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
			}, []string{"group_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID int64 `json:"group_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				info, err := callNapCat(&api.GetGroupInfo{GroupID: p.GroupID})
				if err != nil {
					return "", err
				}
				ex, err := callNapCat(&api.GetGroupInfoEx{GroupID: p.GroupID})
				if err != nil {
					return "", err
				}
				base := map[string]any{}
				if err := json.Unmarshal(info, &base); err != nil {
					return "", fmt.Errorf("解析群信息失败: %w", err)
				}
				extra := map[string]any{}
				if err := json.Unmarshal(ex, &extra); err != nil {
					return "", fmt.Errorf("解析群扩展信息失败: %w", err)
				}
				for k, v := range extra { // ex 补充 info 没有的键，info 优先
					if _, ok := base[k]; !ok {
						base[k] = v
					}
				}
				return marshalCompact(base)
			},
		},
		{
			Name:        "lookup_members",
			Description: "获取群成员列表（user_id/nickname/card/role），最多返回 100 条并注明是否截断。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
			}, []string{"group_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID int64 `json:"group_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				data, err := callNapCat(&api.GetGroupMemberList{GroupID: p.GroupID})
				if err != nil {
					return "", err
				}
				var members []map[string]any
				if err := json.Unmarshal(data, &members); err != nil {
					return "", fmt.Errorf("解析群成员列表失败: %w", err)
				}
				rows := make([]map[string]any, 0, len(members))
				for _, m := range members {
					rows = append(rows, map[string]any{
						"user_id": m["user_id"], "nickname": m["nickname"],
						"card": m["card"], "role": m["role"],
					})
				}
				total := len(rows)
				truncated := total > maxMembersReturned
				if truncated {
					rows = rows[:maxMembersReturned]
				}
				return marshalCompact(map[string]any{
					"total": total, "returned": len(rows), "truncated": truncated, "members": rows,
				})
			},
		},
		{
			Name:        "lookup_member",
			Description: "查群成员的详细资料（名片、角色、等级、入群时间等）。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"user_id":  intProp("成员 QQ 号"),
			}, []string{"group_id", "user_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID int64 `json:"group_id"`
					UserID  int64 `json:"user_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 || p.UserID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id / user_id（且不能为 0）")
				}
				return passthrough(callNapCat(&api.GetGroupMemberInfo{GroupID: p.GroupID, UserID: p.UserID}))
			},
		},
		{
			Name:        "set_group_card",
			Description: "设置群成员的群名片（管理员能力）。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"user_id":  intProp("成员 QQ 号"),
				"card":     strProp("新群名片（空串表示清除）"),
			}, []string{"group_id", "user_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID int64  `json:"group_id"`
					UserID  int64  `json:"user_id"`
					Card    string `json:"card"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 || p.UserID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id / user_id（且不能为 0）")
				}
				return emptyOK(callNapCat(&api.SetGroupCard{GroupID: p.GroupID, UserID: p.UserID, Card: p.Card}))
			},
		},
		{
			Name:        "set_group_special_title",
			Description: "设置群成员的专属头衔（管理员能力）。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"user_id":  intProp("成员 QQ 号"),
				"title":    strProp("专属头衔（空串表示清除）"),
			}, []string{"group_id", "user_id", "title"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID int64  `json:"group_id"`
					UserID  int64  `json:"user_id"`
					Title   string `json:"title"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 || p.UserID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id / user_id（且不能为 0）")
				}
				if p.Title == "" {
					return "", fmt.Errorf("缺少必填参数 title（清除头衔传空格等占位即可）")
				}
				return emptyOK(callNapCat(&api.SetGroupSpecialTitle{
					GroupID: p.GroupID, UserID: p.UserID, SpecialTitle: p.Title,
				}))
			},
		},
		{
			Name:        "group_ban",
			Description: "禁言群成员一段时间（管理员能力）。duration 单位秒，最长 30 天。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"user_id":  intProp("成员 QQ 号"),
				"duration": intProp("禁言时长（秒），0 表示解除禁言，上限 2592000"),
			}, []string{"group_id", "user_id", "duration"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID  int64 `json:"group_id"`
					UserID   int64 `json:"user_id"`
					Duration int64 `json:"duration"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 || p.UserID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id / user_id（且不能为 0）")
				}
				if p.Duration < 0 || p.Duration > maxBanDuration {
					return "", fmt.Errorf("duration 需在 0 ~ %d 秒之间（30 天），got %d", maxBanDuration, p.Duration)
				}
				return emptyOK(callNapCat(&api.SetGroupBan{GroupID: p.GroupID, UserID: p.UserID, Duration: p.Duration}))
			},
		},
		{
			Name:        "group_whole_ban",
			Description: "开启/关闭群全员禁言（管理员能力）。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"enable":   map[string]any{"type": "boolean", "description": "true 开启全员禁言，false 关闭"},
			}, []string{"group_id", "enable"}),
			Run: func(args json.RawMessage) (string, error) {
				var raw struct {
					GroupID int64 `json:"group_id"`
					Enable  *bool `json:"enable"`
				}
				if err := decodeArgs(args, &raw); err != nil {
					return "", err
				}
				if raw.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				if raw.Enable == nil {
					return "", fmt.Errorf("缺少必填参数 enable（true/false）")
				}
				return emptyOK(callNapCat(&api.SetGroupWholeBan{GroupID: raw.GroupID, Enable: *raw.Enable}))
			},
		},
		{
			Name:        "kick_member",
			Description: "把成员踢出群聊（管理员能力，危险操作，需要管理员权限）。",
			Parameters: objectParams(map[string]any{
				"group_id":           intProp("群号"),
				"user_id":            intProp("成员 QQ 号"),
				"reject_add_request": map[string]any{"type": "boolean", "description": "是否拒绝此人再次加群，默认 false"},
			}, []string{"group_id", "user_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if err := requireAdmin(); err != nil {
					return "", err
				}
				var p struct {
					GroupID          int64 `json:"group_id"`
					UserID           int64 `json:"user_id"`
					RejectAddRequest bool  `json:"reject_add_request"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 || p.UserID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id / user_id（且不能为 0）")
				}
				return emptyOK(callNapCat(&api.SetGroupKick{
					GroupID: p.GroupID, UserID: p.UserID, RejectAddRequest: p.RejectAddRequest,
				}))
			},
		},
		{
			Name:        "set_group_admin",
			Description: "设置/取消群管理员（危险操作，需要管理员权限）。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"user_id":  intProp("成员 QQ 号"),
				"enable":   map[string]any{"type": "boolean", "description": "true 设为管理员，false 取消"},
			}, []string{"group_id", "user_id", "enable"}),
			Run: func(args json.RawMessage) (string, error) {
				if err := requireAdmin(); err != nil {
					return "", err
				}
				var raw struct {
					GroupID int64 `json:"group_id"`
					UserID  int64 `json:"user_id"`
					Enable  *bool `json:"enable"`
				}
				if err := decodeArgs(args, &raw); err != nil {
					return "", err
				}
				if raw.GroupID == 0 || raw.UserID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id / user_id（且不能为 0）")
				}
				if raw.Enable == nil {
					return "", fmt.Errorf("缺少必填参数 enable（true/false）")
				}
				return emptyOK(callNapCat(&api.SetGroupAdmin{GroupID: raw.GroupID, UserID: raw.UserID, Enable: *raw.Enable}))
			},
		},
		{
			Name:        "set_group_name",
			Description: "修改群名称。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"name":     strProp("新群名称"),
			}, []string{"group_id", "name"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID int64  `json:"group_id"`
					Name    string `json:"name"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				if p.Name == "" {
					return "", fmt.Errorf("name 不能为空")
				}
				return emptyOK(callNapCat(&api.SetGroupName{GroupID: p.GroupID, GroupName: p.Name}))
			},
		},
		{
			Name:        "leave_group",
			Description: "退出指定群聊（危险操作，需要管理员权限；群主解散群请通过逃生舱 is_dismiss）。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("要退出的群号"),
			}, []string{"group_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if err := requireAdmin(); err != nil {
					return "", err
				}
				var p struct {
					GroupID int64 `json:"group_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				return emptyOK(callNapCat(&api.SetGroupLeave{GroupID: p.GroupID}))
			},
		},
		{
			Name:        "send_group_notice",
			Description: "在群里发布一条公告。content（文字）与 image（图片 URL/路径）至少提供一个。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"content":  strProp("公告文字内容"),
				"image":    strProp("可选，公告图片（路径或 URL）"),
			}, []string{"group_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID int64  `json:"group_id"`
					Content string `json:"content"`
					Image   string `json:"image"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				if p.Content == "" && p.Image == "" {
					return "", fmt.Errorf("content 与 image 至少提供一个")
				}
				return emptyOK(callNapCat(&api.SendGroupNotice{
					GroupID: p.GroupID, Content: p.Content, Image: p.Image,
					Pinned: 0, Type: 1, ConfirmRequired: 1, IsShowEditCard: 0, TipWindowType: 0,
				}))
			},
		},
		{
			Name:        "set_essence",
			Description: "把一条群消息设为精华消息。",
			Parameters: objectParams(map[string]any{
				"message_id": intProp("消息 ID"),
			}, []string{"message_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					MessageID int64 `json:"message_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.MessageID == 0 {
					return "", fmt.Errorf("message_id 不能为 0")
				}
				return emptyOK(callNapCat(&api.SetEssenceMsg{MessageID: p.MessageID}))
			},
		},
		{
			Name:        "remove_essence",
			Description: "把一条消息移出群精华消息。",
			Parameters: objectParams(map[string]any{
				"message_id": intProp("消息 ID"),
			}, []string{"message_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					MessageID int64 `json:"message_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.MessageID == 0 {
					return "", fmt.Errorf("message_id 不能为 0")
				}
				return emptyOK(callNapCat(&api.DeleteEssenceMsg{MessageID: p.MessageID}))
			},
		},
		{
			Name:        "poke",
			Description: "戳一戳对方（群聊戳群成员，私聊戳好友）。",
			Parameters: objectParams(map[string]any{
				"chat_type": strProp("会话类型：private / group"),
				"chat_id":   intProp("群号（group 时）；私聊可传对方 QQ"),
				"user_id":   intProp("要戳的人的 QQ 号"),
			}, []string{"chat_type", "user_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					ChatType string `json:"chat_type"`
					ChatID   int64  `json:"chat_id"`
					UserID   int64  `json:"user_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.UserID == 0 {
					return "", fmt.Errorf("缺少必填参数 user_id（且不能为 0）")
				}
				switch p.ChatType {
				case "group":
					if p.ChatID == 0 {
						return "", fmt.Errorf("group 戳一戳需要 chat_id（群号）")
					}
					return emptyOK(callNapCat(&api.SendGroupPoke{GroupID: &p.ChatID, UserID: p.UserID}))
				case "private":
					return emptyOK(callNapCat(&api.SendFriendPoke{UserID: p.UserID}))
				default:
					return "", fmt.Errorf("chat_type 必须是 private 或 group, got %q", p.ChatType)
				}
			},
		},
		{
			Name:        "send_like",
			Description: "给指定 QQ 点赞（每天最多 10 个赞）。",
			Parameters: objectParams(map[string]any{
				"qq":    intProp("对方 QQ 号"),
				"times": intProp("点赞次数，默认 1，最大 10"),
			}, []string{"qq"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					QQ    int64 `json:"qq"`
					Times int   `json:"times"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.QQ == 0 {
					return "", fmt.Errorf("缺少必填参数 qq（且不能为 0）")
				}
				times := p.Times
				if times <= 0 {
					times = 1
				}
				if times > 10 {
					times = 10
				}
				return emptyOK(callNapCat(&api.SendLike{UserID: p.QQ, Times: times}))
			},
		},
		{
			Name:        "handle_friend_request",
			Description: "同意或拒绝加好友请求。flag 从好友申请事件/系统消息里拿到。",
			Parameters: objectParams(map[string]any{
				"flag":    strProp("请求 flag"),
				"approve": map[string]any{"type": "boolean", "description": "是否同意"},
			}, []string{"flag"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Flag    string `json:"flag"`
					Approve bool   `json:"approve"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.Flag == "" {
					return "", fmt.Errorf("缺少必填参数 flag")
				}
				return emptyOK(callNapCat(&api.SetFriendAddRequest{Flag: p.Flag, Approve: p.Approve}))
			},
		},
		{
			Name:        "handle_group_request",
			Description: "同意或拒绝加群请求/邀请。flag 从群系统消息里拿到；sub_type 为 add（申请）或 invite（邀请）。",
			Parameters: objectParams(map[string]any{
				"flag":     strProp("请求 flag"),
				"sub_type": strProp("add（加群申请）或 invite（邀请）"),
				"approve":  map[string]any{"type": "boolean", "description": "是否同意"},
			}, []string{"flag", "sub_type"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Flag    string `json:"flag"`
					SubType string `json:"sub_type"`
					Approve bool   `json:"approve"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.Flag == "" {
					return "", fmt.Errorf("缺少必填参数 flag")
				}
				if p.SubType != "add" && p.SubType != "invite" {
					return "", fmt.Errorf("sub_type 必须是 add 或 invite, got %q", p.SubType)
				}
				// api.SetGroupAddRequest 缺 sub_type 字段（文档示例要求传），
				// 用本地结构体补齐。
				return emptyOK(callNapCat(&groupAddRequestReq{
					Flag: p.Flag, SubType: p.SubType, Approve: p.Approve,
				}))
			},
		},
		{
			Name:        "get_msg_history",
			Description: "拉取某个会话在 NapCat 侧的历史消息（不受本地记忆库上限约束），返回 messages 数组。",
			Parameters: objectParams(map[string]any{
				"chat_type":   strProp("会话类型：private / group"),
				"chat_id":     intProp("群号；私聊 = 对方 QQ"),
				"message_seq": intProp("可选，起始消息序号（翻页用）"),
				"count":       intProp("可选，条数，默认 20，最大 20"),
			}, []string{"chat_type", "chat_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					ChatType   string `json:"chat_type"`
					ChatID     int64  `json:"chat_id"`
					MessageSeq int64  `json:"message_seq"`
					Count      int    `json:"count"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.ChatID == 0 {
					return "", fmt.Errorf("缺少必填参数 chat_id（且不能为 0）")
				}
				count := p.Count
				if count <= 0 {
					count = maxMsgHistoryCount
				}
				if count > maxMsgHistoryCount {
					count = maxMsgHistoryCount
				}
				switch p.ChatType {
				case "group":
					return passthrough(callNapCat(&api.GetGroupMsgHistory{
						GroupID: p.ChatID, MessageSeq: p.MessageSeq, Count: count,
					}))
				case "private":
					return passthrough(callNapCat(&api.GetFriendMsgHistory{
						UserID: p.ChatID, MessageSeq: p.MessageSeq, Count: count,
					}))
				default:
					return "", fmt.Errorf("chat_type 必须是 private 或 group, got %q", p.ChatType)
				}
			},
		},
		{
			Name:        "get_forward_detail",
			Description: "查看一条合并转发消息的具体内容。forward_id 从消息段/发送结果里拿到。",
			Parameters: objectParams(map[string]any{
				"forward_id": strProp("合并转发消息的 ID"),
			}, []string{"forward_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					ForwardID string `json:"forward_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.ForwardID == "" {
					return "", fmt.Errorf("缺少必填参数 forward_id")
				}
				// 文档：get_forward_msg 收字符串形态的 message_id
				return passthrough(callNapCat(&api.GetForwardMsg{MessageID: p.ForwardID}))
			},
		},
		{
			Name:        "upload_group_file",
			Description: "上传本地文件到群文件系统。file 仅支持本地路径，且文件必须与 NapCat 在同一台机器上（不支持 http/base64）。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"file":     strProp("本地文件绝对路径"),
				"name":     strProp("上传后显示的文件名"),
			}, []string{"group_id", "file", "name"}),
			Usage:     "上传耗时与文件大小相关，大文件建议 `\"_async\": true` 后台执行：立即返回任务回执，完成后自动送达结果。\n错误形态：file 为 http/base64 链接 → 报错；群文件系统未开启 → NapCat 报错回填。",
			Asyncable: true,
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID int64  `json:"group_id"`
					File    string `json:"file"`
					Name    string `json:"name"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				if p.Name == "" {
					return "", fmt.Errorf("缺少必填参数 name")
				}
				if p.File == "" {
					return "", fmt.Errorf("缺少必填参数 file")
				}
				low := strings.ToLower(p.File)
				if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "base64://") {
					return "", fmt.Errorf("upload_group_file 仅支持本地路径（NapCat 与文件须在同一台机器上），不支持 http/base64: %s", p.File)
				}
				// 工作区相对路径映射为 NapCat(WSL) 侧路径；绝对 Windows 路径
				// 自动映射 /mnt/<盘符>/...，模型不需要知道 WSL 的存在
				file, merr := napcatFilePath(ctx.Workspace, p.File)
				if merr != nil {
					return "", merr
				}
				return passthrough(callNapCat(&api.UploadGroupFile{
					GroupID: p.GroupID, File: file, Name: p.Name, UploadFile: true,
				}))
			},
		},
		{
			Name:        "get_group_files",
			Description: "查看群文件列表：不传 folder_id 列根目录，传 folder_id 列该文件夹内容。",
			Parameters: objectParams(map[string]any{
				"group_id":  intProp("群号"),
				"folder_id": strProp("可选，文件夹 ID（不传则列根目录）"),
			}, []string{"group_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID  int64  `json:"group_id"`
					FolderID string `json:"folder_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				if p.FolderID == "" {
					return passthrough(callNapCat(&api.GetGroupRootFiles{GroupID: p.GroupID}))
				}
				return passthrough(callNapCat(&api.GetGroupFilesByFolder{GroupID: p.GroupID, FolderID: p.FolderID}))
			},
		},
		{
			Name:        "napcat_api",
			Description: "万能逃生舱：调用任意 NapCat API 动作。action 为动作名（可带 / 前缀），params 为参数对象。先用 napcat_docs 查常用动作；未封装的动作参数细节见 apifox 文档。部分危险动作需要管理员权限。",
			Parameters: objectParams(map[string]any{
				"action": strProp("NapCat 动作名，如 get_group_honor_info（可带 / 前缀）"),
				"params": map[string]any{
					"type":        "object",
					"description": "动作参数对象，键为文档里的参数名；无参动作可省略",
				},
			}, []string{"action"}),
			Usage: "示例——查群荣耀：\n`{\"action\":\"get_group_honor_info\",\"params\":{\"group_id\":928952016,\"type\":\"legend\"}}`\n未知动作会列出全部可用动作名；params 键名必须与文档一致（驼峰），解析失败报错回填。",
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Action string          `json:"action"`
					Params json.RawMessage `json:"params"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p.Action), "/"))
				if name == "" {
					return "", fmt.Errorf("缺少必填参数 action")
				}
				typ, ok := actionRegistry[name]
				if !ok {
					names := make([]string, 0, len(actionRegistry))
					for n := range actionRegistry {
						names = append(names, n)
					}
					sort.Strings(names)
					return "", fmt.Errorf("未知动作 %q，可用动作：%s", p.Action, strings.Join(names, ", "))
				}
				if !admin && dangerousNapcatActions[name] {
					return "", errAdminRequired
				}
				reqVal := reflect.New(typ) // *T，T 为该动作的请求结构体
				if len(p.Params) > 0 {
					if err := json.Unmarshal(p.Params, reqVal.Interface()); err != nil {
						return "", fmt.Errorf("params 解析失败（应为参数对象）: %w", err)
					}
				}
				return passthrough(callNapCat(reqVal.Interface().(api.Request)))
			},
		},
		{
			Name:        "napcat_docs",
			Description: "返回常用 NapCat 动作速查（动作名 + 用途 + 关键参数）。配合 napcat_api 使用；全部动作见 apifox 文档。",
			Parameters:  objectParams(nil, nil),
			Run: func(json.RawMessage) (string, error) {
				return strings.TrimSpace(napcatActionsJSON), nil
			},
		},
	}
}

// qzoneTools QQ 空间板块（全部走 SnowLuma 形态：tid/target_uin，
// 不用 feed_ref，两种定位方式不能混用）。
func qzoneTools(admin bool) []Tool {
	requireAdmin := func() error {
		if !admin {
			return errAdminRequired
		}
		return nil
	}
	return []Tool{
		{
			Name:        "post_qzone",
			Description: "在自己的 QQ 空间发一条说说：text 必填；images 可选，传图片链接数组（http/https/file/base64 均可，NapCat 自动上传）。空间是公开内容，发布前想清楚。",
			Parameters: objectParams(map[string]any{
				"text":   strProp("说说文字内容（不能为空）"),
				"images": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "可选，配图链接数组（http/https/file/base64，NapCat 自动上传）"},
			}, []string{"text"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Text   string   `json:"text"`
					Images []string `json:"images"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.Text == "" {
					return "", fmt.Errorf("text 不能为空")
				}
				return passthrough(callNapCat(&api.SendQzoneMsg{Content: p.Text, Images: p.Images}))
			},
		},
		{
			Name:        "browse_qzone_feeds",
			Description: "刷好友空间动态（每页最多 10 条：uin/author/time/summary/image_count/tid，has_more 表示可翻页）。想细看或互动某条时，拿它的 tid 走 read_qzone_post / qzone_like / comment_qzone。",
			Parameters: objectParams(map[string]any{
				"page_num": intProp("可选，页码，默认 1"),
			}, nil),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					PageNum int `json:"page_num"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				page := p.PageNum
				if page < 1 {
					page = 1
				}
				data, err := callNapCat(&api.GetQzoneFeeds{PageNum: page})
				if err != nil {
					return "", err
				}
				var resp struct {
					Feeds []struct {
						Uin      int64   `json:"uin"`
						Nickname string  `json:"nickname"`
						Time     int64   `json:"time"`
						TID      *string `json:"tid"`
						Content  *string `json:"content"`
						Images   []any   `json:"images"`
					} `json:"feeds"`
					HasMore bool `json:"has_more"`
				}
				if err := json.Unmarshal(data, &resp); err != nil {
					return "", fmt.Errorf("解析空间动态失败: %w", err)
				}
				rows := make([]map[string]any, 0, len(resp.Feeds))
				for i, f := range resp.Feeds {
					if i >= maxFeedsReturned {
						break
					}
					tid, content := "", ""
					if f.TID != nil {
						tid = *f.TID
					}
					if f.Content != nil {
						content = *f.Content
					}
					rows = append(rows, map[string]any{
						"uin":         f.Uin,
						"author":      f.Nickname,
						"time":        time.Unix(f.Time, 0).Format(timeLayout),
						"summary":     truncateRunes(content, maxQzoneSummaryRunes),
						"image_count": len(f.Images),
						"tid":         tid,
					})
				}
				return marshalCompact(map[string]any{"feeds": rows, "has_more": resp.HasMore})
			},
		},
		{
			Name:        "read_qzone_post",
			Description: "读一条空间说说的完整内容与首批评论（正文限 200 字、评论限 10 条、单条限 100 字）。tid 来自 browse_qzone_feeds；target_uin 是说说作者的 QQ 号，默认自己的空间。",
			Parameters: objectParams(map[string]any{
				"tid":        strProp("说说 tid"),
				"target_uin": intProp("可选，说说作者的 QQ 号，默认自己的空间"),
			}, []string{"tid"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					TID       string `json:"tid"`
					TargetUin int64  `json:"target_uin"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.TID == "" {
					return "", fmt.Errorf("缺少必填参数 tid")
				}
				data, err := callNapCat(&api.GetQzoneMsgDetail{TID: p.TID, TargetUin: p.TargetUin})
				if err != nil {
					return "", err
				}
				var resp struct {
					Content    *string `json:"content"`
					CommentNum int64   `json:"comment_num"`
					Comments   []struct {
						Name    string  `json:"name"`
						Time    int64   `json:"time"`
						Content *string `json:"content"`
						Replies []struct {
							Name    string  `json:"name"`
							Content *string `json:"content"`
						} `json:"replies"`
					} `json:"comments"`
				}
				if err := json.Unmarshal(data, &resp); err != nil {
					return "", fmt.Errorf("解析说说详情失败: %w", err)
				}
				content := ""
				if resp.Content != nil {
					content = *resp.Content
				}
				comments := make([]map[string]any, 0, len(resp.Comments))
				for i, c := range resp.Comments {
					if i >= maxQzoneCommentsReturned {
						break
					}
					text := ""
					if c.Content != nil {
						text = *c.Content
					}
					replies := make([]map[string]any, 0, len(c.Replies))
					for j, r := range c.Replies {
						if j >= maxQzoneRepliesReturned {
							break
						}
						rtext := ""
						if r.Content != nil {
							rtext = *r.Content
						}
						replies = append(replies, map[string]any{
							"name": r.Name, "content": truncateRunes(rtext, maxQzoneSummaryRunes),
						})
					}
					comments = append(comments, map[string]any{
						"name":    c.Name,
						"time":    time.Unix(c.Time, 0).Format(timeLayout),
						"content": truncateRunes(text, maxQzoneSummaryRunes),
						"replies": replies,
					})
				}
				return marshalCompact(map[string]any{
					"tid":         p.TID,
					"content":     truncateRunes(content, maxQzonePostContentRunes),
					"comment_num": resp.CommentNum,
					"comments":    comments,
				})
			},
		},
		{
			Name:        "qzone_like",
			Description: "给一条空间说说点赞 / 取消点赞（enable 默认 true 点赞，false 取消）。tid 来自 browse_qzone_feeds；target_uin 是说说作者的 QQ 号，默认自己的空间。点赞要自然：真有感触才点，别批量刷。",
			Parameters: objectParams(map[string]any{
				"tid":        strProp("说说 tid"),
				"target_uin": intProp("可选，说说作者的 QQ 号，默认自己的空间"),
				"enable":     map[string]any{"type": "boolean", "description": "true 点赞（默认），false 取消点赞"},
			}, []string{"tid"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					TID       string `json:"tid"`
					TargetUin int64  `json:"target_uin"`
					Enable    *bool  `json:"enable"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.TID == "" {
					return "", fmt.Errorf("缺少必填参数 tid")
				}
				enable := true
				if p.Enable != nil {
					enable = *p.Enable
				}
				var req api.Request = &api.LikeQzone{TID: p.TID, TargetUin: p.TargetUin}
				if !enable {
					req = &api.UnlikeQzone{TID: p.TID, TargetUin: p.TargetUin}
				}
				return emptyOK(callNapCat(req))
			},
		},
		{
			Name:        "comment_qzone",
			Description: "给一条空间说说发一级评论。content 必填；target_uin 是说说作者的 QQ 号，默认自己的空间。楼中楼回复暂不支持（可经 napcat_api 调 comment_qzone 的回复参数）。评论对内容说话，别复制粘贴同一句到处刷。",
			Parameters: objectParams(map[string]any{
				"tid":        strProp("说说 tid"),
				"content":    strProp("评论文本（不能为空）"),
				"target_uin": intProp("可选，说说作者的 QQ 号，默认自己的空间"),
			}, []string{"tid", "content"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					TID       string `json:"tid"`
					Content   string `json:"content"`
					TargetUin int64  `json:"target_uin"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.TID == "" {
					return "", fmt.Errorf("缺少必填参数 tid")
				}
				if p.Content == "" {
					return "", fmt.Errorf("缺少必填参数 content")
				}
				return passthrough(callNapCat(&api.CommentQzone{TID: p.TID, Content: p.Content, TargetUin: p.TargetUin}))
			},
		},
		{
			Name:        "qzone_profile",
			Description: "查看自己 QQ 空间的名称与说明。",
			Parameters:  objectParams(nil, nil),
			Run: func(json.RawMessage) (string, error) {
				return passthrough(callNapCat(&api.GetQzoneProfile{}))
			},
		},
		{
			Name:        "delete_qzone",
			Description: "删除自己发的一条空间说说（危险操作，需要管理员权限，删前想清楚）。tid 来自 browse_qzone_feeds / read_qzone_post。",
			Parameters: objectParams(map[string]any{
				"tid": strProp("要删除的说说 tid"),
			}, []string{"tid"}),
			Run: func(args json.RawMessage) (string, error) {
				if err := requireAdmin(); err != nil {
					return "", err
				}
				var p struct {
					TID string `json:"tid"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.TID == "" {
					return "", fmt.Errorf("缺少必填参数 tid")
				}
				return emptyOK(callNapCat(&api.DeleteQzoneMsg{TID: p.TID}))
			},
		},
	}
}

// groupAddRequestReq 带 sub_type 的处理加群请求（文档示例要求 sub_type，
// api.SetGroupAddRequest 未含该字段，这里在 agent 侧补齐）。
type groupAddRequestReq struct {
	Flag    string `json:"flag"`
	SubType string `json:"sub_type"`
	Approve bool   `json:"approve,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func (r *groupAddRequestReq) Path() string { return "/set_group_add_request" }

// actionTypes 全量 NapCat 动作请求类型（与 api 包 196 个动作一一对应：
// actions_test.go 登记的 192 个 + api.go 定义的 4 个）。
func actionTypes() []api.Request {
	return []api.Request{
		// api.go 已有
		&api.SendGroupMsg{}, &api.SendPrivateMsg{}, &api.GetMsg{}, &api.DeleteMsg{},

		// ---------------- 核心接口 ----------------
		&api.SetGroupTodo{}, &api.CompleteGroupTodo{}, &api.CancelGroupTodo{},
		&api.SendGroupPoke{}, &api.SendFriendPoke{}, &api.SendPoke{},

		// ---------------- 群组接口 ----------------
		&api.GetGroupDetailInfo{}, &api.GetGroupList{}, &api.GetGroupInfo{},
		&api.GetGroupMemberList{}, &api.GetGroupMemberInfo{},
		&api.SetGroupAddRequest{}, &api.SetGroupLeave{}, &api.SetGroupWholeBan{},
		&api.SetGroupBan{}, &api.SetGroupKick{}, &api.SetGroupAdmin{},
		&api.SetGroupName{}, &api.SetGroupCard{}, &api.GetEssenceMsgList{},
		&api.GetGroupIgnoredNotifies{}, &api.DeleteEssenceMsg{}, &api.GetGroupNotice{},
		&api.SetEssenceMsg{}, &api.DeleteGroupNotice{}, &api.GetGroupShutList{},
		&api.GetGroupIgnoreAddRequest{}, &api.SetGroupMemberInvitePolicy{},
		&api.SetGroupMemberPermissions{}, &api.SetGroupNewMemberHistoryVisibility{},
		&api.GetGroupHonorInfo{}, &api.GetGroupAtAllRemain{}, &api.SetGroupPortrait{},

		// ---------------- 群组扩展 ----------------
		&api.SetGroupAddOption{}, &api.SetGroupRobotAddOption{}, &api.SetGroupSearch{},
		&api.SetGroupRemark{}, &api.GetGroupInfoEx{}, &api.SetGroupSign{}, &api.SendGroupSign{},
		&api.GetGroupSignedList{}, &api.GetGroupShareLink{},

		// ---------------- 好友/用户 ----------------
		&api.SetFriendRemark{}, &api.SendLike{}, &api.GetFriendList{},
		&api.SetFriendAddRequest{}, &api.GetCookies{}, &api.GetRecentContact{},
		&api.GetFriendsWithCategory{}, &api.GetProfileLike{}, &api.SetDiyOnlineStatus{},
		&api.GetUnidirectionalFriendList{}, &api.DeleteFriend{},

		// ---------------- 消息接口 ----------------
		&api.SendMsg{}, &api.ForwardFriendSingleMsg{}, &api.ForwardGroupSingleMsg{},
		&api.MarkGroupMsgAsRead{}, &api.MarkPrivateMsgAsRead{}, &api.MarkMsgAsRead{},
		&api.MarkAllAsRead{},
		&api.SendForwardMsg{}, &api.SendGroupForwardMsg{}, &api.SendPrivateForwardMsg{},
		&api.GetForwardMsg{}, &api.GetGroupMsgHistory{}, &api.GetFriendMsgHistory{},

		// ---------------- 消息扩展 ----------------
		&api.FetchEmojiLike{}, &api.GetEmojiLikes{}, &api.FetchPttText{},
		&api.ArkShareGroup{}, &api.ArkSharePeer{}, &api.SendGroupArkShare{}, &api.SendArkShare{},
		&api.SetMsgEmojiLike{}, &api.ClickInlineKeyboardButton{},

		// ---------------- 文件接口 ----------------
		&api.GetFile{}, &api.GetImage{}, &api.GetRecord{},
		&api.GetGroupFileURL{}, &api.GetPrivateFileURL{},

		// ---------------- 文件扩展 ----------------
		&api.MoveGroupFile{}, &api.RenameGroupFile{}, &api.TransGroupFile{},
		&api.CreateFlashTask{}, &api.GetFlashFileList{}, &api.GetFlashFileURL{},
		&api.SendFlashMsg{}, &api.GetShareLink{}, &api.GetFilesetInfo{},
		&api.GetOnlineFileMsg{}, &api.SendOnlineFile{}, &api.SendOnlineFolder{},
		&api.ReceiveOnlineFile{}, &api.RefuseOnlineFile{}, &api.CancelOnlineFile{},
		&api.DownloadFileset{}, &api.GetFilesetID{},

		// ---------------- Go-CQHTTP 文件 ----------------
		&api.GetGroupRootFiles{}, &api.UploadGroupFile{}, &api.DeleteGroupFile{},
		&api.CreateGroupFileFolder{}, &api.DeleteGroupFolder{}, &api.GetGroupFileSystemInfo{},
		&api.GetGroupFilesByFolder{}, &api.DownloadFile{}, &api.UploadPrivateFile{},

		// ---------------- 系统接口 ----------------
		&api.GetLoginInfo{}, &api.GetVersionInfo{}, &api.CanSendRecord{}, &api.CanSendImage{},
		&api.GetStatus{}, &api.GetCsrfToken{}, &api.GetCredentials{}, &api.NcGetPacketStatus{},
		&api.SetRestart{}, &api.GetGroupSystemMsg{}, &api.CleanCache{},
		&api.SetDoubtFriendsAddRequest{}, &api.GetDoubtFriendsAddRequest{},

		// ---------------- 系统扩展 ----------------
		&api.GetRkey{}, &api.GetRkeyServer{}, &api.SetOnlineStatus{}, &api.GetRobotUinRange{},
		&api.FetchCustomFace{}, &api.FetchCustomFaceDetail{}, &api.AddCustomFace{},
		&api.DeleteCustomFace{}, &api.SetCustomFaceDesc{}, &api.SetInputStatus{},
		&api.NcGetUserStatus{}, &api.NcGetRkey{}, &api.GetMiniAppArk{}, &api.SendPacket{},
		&api.BotExit{}, &api.GetCollectionList{},

		// ---------------- Go-CQHTTP 其他 ----------------
		&api.SetQQProfile{}, &api.CheckURLSafely{}, &api.GetOnlineClients{},
		&api.GetStrangerInfo{}, &api.GetModelShow{}, &api.SetModelShow{},
		&api.SendGroupNotice{}, &api.HandleQuickOperation{},

		// ---------------- 扩展接口 ----------------
		&api.SetGroupKickMembers{}, &api.CreateCollection{}, &api.SetSelfLongNick{},
		&api.SetQQAvatar{}, &api.TranslateEn2Zh{}, &api.GetClientKey{},
		&api.SendQzoneMsg{}, &api.OCRImage{}, &api.DeleteQzoneMsg{}, &api.OCRImageInternal{},
		&api.SetGroupSpecialTitle{}, &api.GetAICharacters{},
		&api.GetQzoneFeeds{}, &api.GetQzoneMsgList{}, &api.GetQzoneMsgDetail{},
		&api.GetQzoneCommentList{}, &api.GetQzoneCommentReplies{},
		&api.LikeQzone{}, &api.UnlikeQzone{}, &api.CommentQzone{},
		&api.GetQzoneProfile{}, &api.SetQzoneProfile{}, &api.SetQzoneMsgRight{}, &api.SetQzoneBan{},

		// ---------------- AI 扩展 ----------------
		&api.GetAIRecord{}, &api.SendGroupAIRecord{},

		// ---------------- QQ空间扩展(空友爱看) ----------------
		&api.GetQzonePublicCapabilities{}, &api.GetQzonePublicFeeds{},
		&api.GetQzonePublicFeedDetail{}, &api.GetQzonePublicComments{},
		&api.GetQzonePublicReplies{}, &api.SetQzonePublicUpvote{}, &api.SetQzonePublicLike{},
		&api.SetQzonePublicFollow{}, &api.SetQzonePublicCommentLike{},
		&api.CommentQzonePublicFeed{}, &api.ReplyQzonePublicFeed{},
		&api.DeleteQzonePublicComment{}, &api.DeleteQzonePublicReply{},
		&api.ForwardQzonePublicFeed{},

		// ---------------- 群相册 ----------------
		&api.DelGroupAlbumMedia{}, &api.SetGroupAlbumMediaLike{},
		&api.CancelGroupAlbumMediaLike{}, &api.DoGroupAlbumComment{},
		&api.GetGroupAlbumMediaList{}, &api.GetQunAlbumList{},
		&api.UploadImageToQunAlbum{}, &api.UploadImagesToQunAlbum{}, &api.UploadVideoToQunAlbum{},
	}
}

// actionRegistry 动作名（无 / 前缀）→ 请求结构体类型。napcat_api 逃生舱
// 用 reflect.New 现场构造零值请求后反序列化 params。init 期构建一次。
var actionRegistry = buildActionRegistry(actionTypes())

// buildActionRegistry 由动作实例列表构建名称 → 类型映射。
func buildActionRegistry(list []api.Request) map[string]reflect.Type {
	m := make(map[string]reflect.Type, len(list))
	for _, a := range list {
		name := strings.TrimPrefix(a.Path(), "/")
		if _, dup := m[name]; dup {
			panic("agent: 动作注册表路径重复 " + name)
		}
		m[name] = reflect.TypeOf(a).Elem()
	}
	return m
}

// napcatActionsJSON 常用动作速查资源（napcat_docs 工具输出）。
//
//go:embed napcat_actions.json
var napcatActionsJSON string
