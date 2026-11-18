package qq

import (
	"Q-Mi/agent/workspacepath"
	apimsg "Q-Mi/api/message"
	"Q-Mi/storage"
	"fmt"
	"strconv"
)

// maxSendMessageTextRunes send_message 单次调用 text 内容的累计上限
// （rune 计），纯 text 与 segments 里的 text 段共用该额度。
const maxSendMessageTextRunes = 8000

// SegmentParam send_message segments 数组里的单个消息段参数。
type SegmentParam struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

// buildSegments 把 segments 参数转成消息段数组（P2.6）。只接受白名单类型，
// 白名单外报错；text 段内容累计超过 maxSendMessageTextRunes 报错；
// image/video/file 段的 file 若为工作区相对路径自动映射为 NapCat(WSL) 侧
// /mnt/<盘符>/... 路径（模型不需要知道 WSL 的存在），http/https/base64://
// 原样透传；sticker 段查 storage 收藏表转换，需要 store 非 nil。
func buildSegments(store *storage.Store, workspace string, segs []SegmentParam) ([]*apimsg.Message, error) {
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
			if mapped, merr := workspacepath.NapcatPath(workspace, file); merr == nil {
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

// stickerBySegment 从 sticker 段参数解析收藏 id 并取记录；失败返回 nil。
func stickerBySegment(store *storage.Store, data map[string]any) *storage.Sticker {
	if store == nil {
		return nil
	}
	id, err := segID(data, "id", 1)
	if err != nil || id == 0 {
		return nil
	}
	st, err := store.GetSticker(id)
	if err != nil {
		return nil
	}
	return st
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
		if st.QQEmojiID != "" {
			return nil, fmt.Errorf("第 %d 段: 表情 %d 已加入 QQ 收藏，只能单独一条发；请单独发这个 sticker 段或用 send_qq_face", at, id)
		}
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
