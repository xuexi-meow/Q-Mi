package sticker

import (
	"Q-Mi/agent/tool"
	"Q-Mi/api"
	apimsg "Q-Mi/api/message"
	"Q-Mi/storage"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// stickerTools 表情收藏板块：本地收藏夹 + QQ 账号级收藏表情。
func Tools(ctx *Dependencies) []Tool {
	store := ctx.Store
	return []Tool{
		{
			Name:        "save_sticker",
			Description: "把一条消息里的表情/图片收藏起来（face/mface/image），并默认顺手加入 QQ 账号收藏表情（协议端 SnowLuma 扩展），之后可用 send_message 的 sticker 段或 send_qq_face 发出。seg_index 是该消息里第几个消息段（从 0 开始，默认 0）。",
			Parameters: tool.ObjectParams(map[string]any{
				"message_id": tool.IntProp("消息 ID（从聊天记录或上下文里拿）"),
				"seg_index":  tool.IntProp("可选，消息段下标（从 0 开始），默认 0"),
				"note":       tool.StringProp("可选备注：给这个表情起的名字或使用场景，会同步到 QQ 收藏表情的备注"),
				"qq":         tool.BoolProp("是否加入 QQ 账号收藏表情，默认 true；填 false 只存本地"),
			}, []string{"message_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if store == nil {
					return "", errors.New("记忆库未启用，无法收藏表情")
				}
				var p struct {
					MessageID int64  `json:"message_id"`
					SegIndex  int    `json:"seg_index"`
					Note      string `json:"note"`
					QQ        *bool  `json:"qq"`
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
				st := storage.Sticker{Kind: seg.Type, Note: strings.TrimSpace(p.Note)}
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
				// 预览是尽力而为：下载失败不影响收藏，之后 view_sticker 会再试。
				segURL := anyString(data["url"])
				previewSHA, previewMime, previewURL := cacheStickerPreview(ctx,
					PreviewCandidates(st.Kind, segURL, st.EmojiID))
				if previewSHA != "" {
					if err := store.UpdateSticker(id, storage.StickerUpdate{
						PreviewSHA: &previewSHA, PreviewMime: &previewMime,
					}); err == nil {
						st.PreviewSHA, st.PreviewMime = previewSHA, previewMime
					}
				}

				// 顺手加入 QQ 账号收藏表情：成功后有 emoji_id，可当真正的收藏表情发。
				addToQQ := p.QQ == nil || *p.QQ
				qqSaved, qqEmojiID, qqErrMsg := false, "", ""
				if addToQQ {
					switch {
					case st.Kind == "face":
						qqErrMsg = "自带小黄脸无需加入收藏"
					case previewURL == "":
						qqErrMsg = "没有可交给协议端下载的图片地址"
					default:
						detail, qqErr := ctx.qqFaceAdd(previewURL)
						if qqErr != nil {
							qqErrMsg = qqErr.Error()
						} else {
							qqSaved, qqEmojiID = true, detail.EmojiID
							upd := storage.StickerUpdate{QQEmojiID: &detail.EmojiID}
							if detail.ResID != "" {
								upd.QQResID = &detail.ResID
							}
							if detail.MD5 != "" {
								upd.QQMD5 = &detail.MD5
							}
							desc := qqFaceDescOr(st.Note, st.Summary)
							if desc != "" {
								if err := ctx.qqFaceSetDesc(detail, desc); err == nil {
									upd.QQDesc = &desc
								}
							}
							if err := store.UpdateSticker(id, upd); err != nil {
								qqErrMsg = "本地记录 QQ 收藏信息失败: " + err.Error()
							}
						}
					}
				}

				out := map[string]any{
					"sticker_id": id, "kind": st.Kind, "summary": st.Summary, "note": st.Note,
					"has_preview": previewSHA != "", "preview_mime": previewMime,
					"qq_saved": qqSaved, "qq_emoji_id": qqEmojiID,
					"hint": "QQ 收藏表情用 send_qq_face 单独发；本地预览用 view_sticker",
				}
				if qqErrMsg != "" {
					out["qq_error"] = qqErrMsg
				}
				return marshalCompact(out)
			},
		},
		{
			Name:        "list_stickers",
			Description: "列出收藏的表情：id/kind/summary/note/has_preview。发的时候用 sticker 段的 id；不确定长相用 view_sticker 看预览，备注用 update_sticker 补。",
			Parameters:  tool.ObjectParams(nil, nil),
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
					rows = append(rows, map[string]any{
						"id": st.ID, "kind": st.Kind, "summary": st.Summary, "note": st.Note,
						"display": stickerDisplayName(st), "has_preview": st.PreviewSHA != "",
						"qq_face": st.QQEmojiID != "",
					})
				}
				return marshalCompact(rows)
			},
		},
		{
			Name:        "delete_sticker",
			Description: "删除一条收藏的表情（传 list_stickers 里的 id）。收藏错了用它清理。",
			Parameters: tool.ObjectParams(map[string]any{
				"sticker_id": tool.IntProp("收藏表情的 id"),
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
			Name:        "view_sticker",
			Description: "查看收藏表情的预览图（附图到下一轮），一次 1-4 个。确认长相后再决定发哪个；没有预览的会列在 missing 里。",
			Parameters: tool.ObjectParams(map[string]any{
				"sticker_ids": map[string]any{
					"type":        "array",
					"description": "要查看的收藏 id 列表（1-4 个）",
					"items":       map[string]any{"type": "integer"},
				},
				"sticker_id": tool.IntProp("单个收藏 id（与 sticker_ids 二选一）"),
			}, nil),
			Run: func(args json.RawMessage) (string, error) {
				if store == nil {
					return "", errors.New("记忆库未启用，无法查看收藏")
				}
				if ctx.Turn == nil {
					return "", errors.New("无会话上下文，预览图无处附加")
				}
				var p struct {
					StickerIDs []int64 `json:"sticker_ids"`
					StickerID  int64   `json:"sticker_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				ids := make([]int64, 0, len(p.StickerIDs)+1)
				if p.StickerID != 0 {
					ids = append(ids, p.StickerID)
				}
				ids = append(ids, p.StickerIDs...)
				if len(ids) == 0 {
					return "", errors.New("需要 sticker_id 或 sticker_ids")
				}
				if len(ids) > 4 {
					ids = ids[:4]
				}
				viewed := make([]map[string]any, 0, len(ids))
				missing := make([]map[string]any, 0)
				seen := make(map[int64]bool, len(ids))
				for _, id := range ids {
					if id == 0 || seen[id] {
						continue
					}
					seen[id] = true
					st, err := store.GetSticker(id)
					if err != nil {
						return "", err
					}
					if st == nil {
						missing = append(missing, map[string]any{"id": id, "reason": "不存在"})
						continue
					}
					du := EnsurePreview(ctx, st)
					if du == "" {
						reason := "无预览（自带表情只有名称，或图片下载失败）"
						missing = append(missing, map[string]any{"id": id, "reason": reason})
						continue
					}
					ctx.Turn.AddPendingImage(du)
					viewed = append(viewed, map[string]any{
						"id": id, "kind": st.Kind, "name": st.Summary, "note": st.Note,
					})
				}
				return marshalCompact(map[string]any{
					"attached": len(viewed), "viewed": viewed, "missing": missing,
				})
			},
		},
		{
			Name:        "update_sticker",
			Description: "给收藏表情补备注或改名：note 是你自己的叫法/使用场景；name 会覆盖展示名（商城表情默认 summary 可能只是「动画表情」）。两个都不传则报错。",
			Parameters: tool.ObjectParams(map[string]any{
				"sticker_id": tool.IntProp("收藏 id"),
				"note":       tool.StringProp("备注（叫法、风格、适合什么场景），传空串表示清空"),
				"name":       tool.StringProp("覆盖展示名（同时作为发送时的 summary），传空串表示清空"),
				"qq":         tool.BoolProp("可选：把这张老收藏补录进 QQ 收藏表情（已加入则忽略）"),
			}, []string{"sticker_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if store == nil {
					return "", errors.New("记忆库未启用，无法修改收藏")
				}
				var p struct {
					StickerID int64   `json:"sticker_id"`
					Note      *string `json:"note"`
					Name      *string `json:"name"`
					QQ        *bool   `json:"qq"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.StickerID == 0 {
					return "", errors.New("sticker_id 不能为 0")
				}
				if p.Note == nil && p.Name == nil && (p.QQ == nil || !*p.QQ) {
					return "", errors.New("note / name / qq 至少要给一个")
				}
				update := storage.StickerUpdate{Note: p.Note, Summary: p.Name}
				if err := store.UpdateSticker(p.StickerID, update); err != nil {
					return "", err
				}
				st, err := store.GetSticker(p.StickerID)
				if err != nil {
					return "", err
				}
				if st == nil {
					return "", fmt.Errorf("没有找到表情 %d", p.StickerID)
				}
				out := map[string]any{
					"sticker_id": st.ID, "kind": st.Kind, "name": st.Summary,
					"note": st.Note, "display": stickerDisplayName(*st),
				}
				if p.QQ != nil && *p.QQ && st.QQEmojiID == "" {
					if st.URL == "" {
						out["qq_error"] = "这张收藏没有图片地址，无法补录进 QQ 收藏"
					} else if detail, qqErr := ctx.qqFaceAdd(st.URL); qqErr != nil {
						out["qq_error"] = qqErr.Error()
					} else {
						upd := storage.StickerUpdate{QQEmojiID: &detail.EmojiID}
						if detail.ResID != "" {
							upd.QQResID = &detail.ResID
						}
						if detail.MD5 != "" {
							upd.QQMD5 = &detail.MD5
						}
						desc := qqFaceDescOr(st.Note, st.Summary)
						if desc != "" {
							if err := ctx.qqFaceSetDesc(detail, desc); err == nil {
								upd.QQDesc = &desc
							}
						}
						if err := store.UpdateSticker(st.ID, upd); err != nil {
							out["qq_error"] = "记录 QQ 收藏信息失败: " + err.Error()
						} else {
							st.QQEmojiID, st.QQResID, st.QQMD5 = detail.EmojiID, detail.ResID, detail.MD5
							if upd.QQDesc != nil {
								st.QQDesc = *upd.QQDesc
							}
							out["qq_saved"] = true
							out["qq_emoji_id"] = detail.EmojiID
						}
					}
				}
				if st.QQEmojiID != "" {
					detail := &api.CustomFaceDetail{
						EmojiID: st.QQEmojiID, ResID: st.QQResID, MD5: st.QQMD5, Desc: st.QQDesc,
					}
					if desc := qqFaceDescOr(st.Note, st.Summary); desc != "" {
						if err := ctx.qqFaceSetDesc(detail, desc); err == nil {
							if err := store.UpdateSticker(st.ID, storage.StickerUpdate{QQDesc: &desc}); err == nil {
								st.QQDesc = desc
								out["qq_desc_synced"] = true
							}
						}
					}
				}
				return marshalCompact(out)
			},
		},
		{
			Name:        "send_qq_face",
			Description: "把 QQ 账号收藏表情以真正的表情样式发给指定会话（协议端 SnowLuma 扩展）。表情只能单独一条：需要配文字时用 text 参数先发文字，再发这条表情。",
			Parameters: tool.ObjectParams(map[string]any{
				"chat_type": tool.StringProp("会话类型：private / group"),
				"chat_id":   tool.IntProp("会话 ID：群号；私聊 = 对方 QQ"),
				"emoji_id":  tool.StringProp("收藏表情的 emoji_id 或 32 位 md5（list_qq_faces / list_stickers 里拿）"),
				"text":      tool.StringProp("可选：先发一条文字，再发表情"),
				"reply_to":  tool.IntProp("可选：要引用的消息 ID"),
			}, []string{"chat_type", "chat_id", "emoji_id"}),
			Usage: "示例——先说话再甩表情：`{\"chat_type\":\"group\",\"chat_id\":928952016,\"text\":\"笑死\",\"emoji_id\":\"abc123...\"}`。协议端不支持扩展接口时返回错误，可退回图片发送；emoji_id 为空直接报错。",
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					ChatType string `json:"chat_type"`
					ChatID   int64  `json:"chat_id"`
					EmojiID  string `json:"emoji_id"`
					Text     string `json:"text"`
					ReplyTo  int64  `json:"reply_to"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.ChatType != "private" && p.ChatType != "group" {
					return "", fmt.Errorf("chat_type 必须是 private 或 group, got %q", p.ChatType)
				}
				if p.ChatID == 0 {
					return "", errors.New("chat_id 不能为 0")
				}
				out := map[string]any{"sent": true, "as": "qq_custom_face", "emoji_id": p.EmojiID}
				if text := strings.TrimSpace(p.Text); text != "" {
					if _, err := ctx.send(p.ChatType, p.ChatID, []*apimsg.Message{apimsg.Text(text)}); err != nil {
						return "", fmt.Errorf("先发文字失败: %w", err)
					}
					out["text_sent"] = true
				}
				data, err := ctx.SendQQFace(p.ChatType, p.ChatID, p.EmojiID, p.ReplyTo)
				if err != nil {
					return "", fmt.Errorf("发送收藏表情失败（协议端需支持 send_custom_face）: %w", err)
				}
				out["result"] = data
				return marshalCompact(out)
			},
		},
		{
			Name:        "list_qq_faces",
			Description: "列出 QQ 账号收藏表情（协议端 SnowLuma 扩展）：emoji_id / md5 / 备注。按备注挑好后用 send_qq_face 发；要看图片先用 sync_qq_faces 导入本地再 view_sticker。",
			Parameters: tool.ObjectParams(map[string]any{
				"query": tool.StringProp("可选：按备注或 emoji_id 过滤"),
				"count": tool.IntProp("可选：拉取条数，默认 48"),
			}, nil),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Query string `json:"query"`
					Count int    `json:"count"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				list, err := ctx.qqFaceList(p.Count)
				if err != nil {
					return "", fmt.Errorf("获取 QQ 收藏表情失败（协议端需支持 fetch_custom_face_detail）: %w", err)
				}
				q := strings.ToLower(strings.TrimSpace(p.Query))
				rows := make([]map[string]any, 0, len(list))
				for _, d := range list {
					if q != "" && !strings.Contains(strings.ToLower(d.Desc), q) &&
						!strings.Contains(strings.ToLower(d.EmojiID), q) &&
						!strings.Contains(strings.ToLower(d.MD5), q) {
						continue
					}
					rows = append(rows, map[string]any{
						"emoji_id": d.EmojiID, "md5": d.MD5, "desc": d.Desc,
						"has_url": d.URL != "",
					})
				}
				return marshalCompact(map[string]any{"total": len(rows), "faces": rows})
			},
		},
		{
			Name:        "sync_qq_faces",
			Description: "把 QQ 账号收藏表情导入本地收藏（带备注和预览），之后可用 list_stickers/view_sticker 统一管理、用 sticker 段或 send_qq_face 发。已导入的按 emoji_id 跳过。",
			Parameters: tool.ObjectParams(map[string]any{
				"count": tool.IntProp("可选：拉取条数，默认 48"),
			}, nil),
			Run: func(args json.RawMessage) (string, error) {
				if store == nil {
					return "", errors.New("记忆库未启用，无法导入收藏")
				}
				var p struct {
					Count int `json:"count"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				list, err := ctx.qqFaceList(p.Count)
				if err != nil {
					return "", fmt.Errorf("获取 QQ 收藏表情失败（协议端需支持 fetch_custom_face_detail）: %w", err)
				}
				existing, err := store.ListStickers()
				if err != nil {
					return "", err
				}
				known := make(map[string]bool, len(existing))
				for _, st := range existing {
					if st.QQEmojiID != "" {
						known[strings.ToLower(st.QQEmojiID)] = true
					}
				}
				imported, skipped := 0, 0
				for _, d := range list {
					key := strings.ToLower(strings.TrimSpace(d.EmojiID))
					if key == "" || known[key] {
						skipped++
						continue
					}
					id, err := store.AddSticker(storage.Sticker{
						Kind: "image", URL: d.URL, Summary: d.Desc, Note: d.Desc,
						QQEmojiID: d.EmojiID, QQResID: d.ResID, QQMD5: d.MD5, QQDesc: d.Desc,
					})
					if err != nil {
						skipped++
						continue
					}
					known[key] = true
					imported++
					if ctx.Images != nil && d.URL != "" {
						if sha, mime, _ := cacheStickerPreview(ctx, []string{d.URL}); sha != "" {
							_ = store.UpdateSticker(id, storage.StickerUpdate{
								PreviewSHA: &sha, PreviewMime: &mime,
							})
						}
					}
				}
				return marshalCompact(map[string]any{
					"total": len(list), "imported": imported, "skipped": skipped,
					"hint": "导入后 list_stickers 可见，view_sticker 看预览",
				})
			},
		},
		{
			Name:        "collect_qq_sticker",
			Description: "把一个网络图片（http/https 链接）下载并加入 QQ 的收藏表情（QQ 账号自带的收藏夹，不是本列表）：先让 NapCat 下载到本地，再加入收藏。适合把好看的图变成账号级表情。",
			Parameters: tool.ObjectParams(map[string]any{
				"url": tool.StringProp("图片链接（http/https）"),
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
				data, err := ctx.call(&api.DownloadFile{URL: p.URL})
				if err != nil {
					return "", fmt.Errorf("下载图片失败: %w", err)
				}
				path := downloadFilePath(data)
				if path == "" {
					return "", fmt.Errorf("下载结果缺少文件路径: %s", data)
				}
				// 第二步：add_custom_face（file 是 NapCat 机器上的本地路径）
				if _, err := ctx.call(&api.AddCustomFace{File: path}); err != nil {
					return "", fmt.Errorf("加入 QQ 收藏失败: %w", err)
				}
				return marshalCompact(map[string]any{"path": path, "added": true})
			},
		},
	}
}
