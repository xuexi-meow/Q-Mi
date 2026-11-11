// qq_custom_faces.go QQ 账号收藏表情（SnowLuma 扩展接口）：
//
//	fetch_custom_face_detail 列收藏（emoji_id/resId/url/md5/desc）
//	add_custom_face          把协议端本机文件加入收藏（返回 emoji_id）
//	set_custom_face_desc     改收藏备注
//	send_custom_face         发送收藏表情（只能单独一条，不能拼消息数组）
//
// 这些是 SnowLuma 的扩展，NapCat/LLOneBot 可能没有：所有调用都返回 error，
// 由上层降级为「本地收藏可用、QQ 收藏不可用」，不影响主流程。
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"Q-Mi/api"
)

// qqFaceDetailLimit 默认拉取的收藏表情条数（SnowLuma 默认 48）。
const qqFaceDetailLimit = 48

// qqFaceList 拉取账号收藏表情详情。
func qqFaceList(count int) ([]api.CustomFaceDetail, error) {
	if count <= 0 {
		count = qqFaceDetailLimit
	}
	data, err := callNapCat(&api.FetchCustomFaceDetail{Count: count})
	if err != nil {
		return nil, err
	}
	var list []api.CustomFaceDetail
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("解析收藏表情详情失败: %w", err)
	}
	return list, nil
}

// qqFaceFind 按 emoji_id 或 md5 匹配一条详情（大小写不敏感）。
func qqFaceFind(list []api.CustomFaceDetail, key string) *api.CustomFaceDetail {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return nil
	}
	for i := range list {
		if strings.ToLower(list[i].EmojiID) == key || strings.ToLower(list[i].MD5) == key {
			return &list[i]
		}
	}
	return nil
}

// qqFaceAdd 让协议端下载 URL 并加入收藏，返回新表情详情（尽量补齐 resId/md5）。
func qqFaceAdd(imageURL string) (*api.CustomFaceDetail, error) {
	imageURL = strings.TrimSpace(imageURL)
	if !isDownloadableURL(imageURL) {
		return nil, fmt.Errorf("需要 http(s) 图片链接，got %q", imageURL)
	}
	data, err := callNapCat(&api.DownloadFile{URL: imageURL})
	if err != nil {
		return nil, fmt.Errorf("下载图片失败: %w", err)
	}
	path := downloadFilePath(data)
	if path == "" {
		return nil, fmt.Errorf("下载结果缺少文件路径: %s", strings.TrimSpace(string(data)))
	}
	resp, err := callNapCat(&api.AddCustomFace{File: path})
	if err != nil {
		return nil, fmt.Errorf("加入 QQ 收藏失败（协议端可能不支持 add_custom_face）: %w", err)
	}
	var out struct {
		EmojiID string `json:"emoji_id"`
	}
	_ = json.Unmarshal(resp, &out)
	if strings.TrimSpace(out.EmojiID) == "" {
		return nil, fmt.Errorf("添加收藏表情未返回 emoji_id: %s", strings.TrimSpace(string(resp)))
	}
	detail := &api.CustomFaceDetail{EmojiID: out.EmojiID}
	// 详情接口用于补齐 resId/md5；失败不阻断收藏，只是备注可能同步不了。
	if list, err := qqFaceList(qqFaceDetailLimit); err == nil {
		if found := qqFaceFind(list, out.EmojiID); found != nil {
			detail = found
		}
	}
	return detail, nil
}

// qqFaceSetDesc 同步备注到 QQ 收藏表情；缺 resId/md5 时无法同步。
func qqFaceSetDesc(detail *api.CustomFaceDetail, desc string) error {
	desc = strings.TrimSpace(desc)
	if detail == nil || desc == "" {
		return nil
	}
	if detail.ResID == "" || detail.MD5 == "" {
		return errors.New("收藏表情缺少 resId/md5，无法同步备注")
	}
	if detail.Desc == desc {
		return nil
	}
	_, err := callNapCat(&api.SetCustomFaceDesc{
		EmojiID: detail.EmojiID, ResID: detail.ResID, MD5: detail.MD5, Desc: desc,
	})
	return err
}

// sendQQFace 发送账号收藏表情（协议端扩展，一条消息只能放这一个表情）。
func sendQQFace(chatType string, chatID int64, emojiID string, replyTo int64) (string, error) {
	emojiID = strings.TrimSpace(emojiID)
	if emojiID == "" {
		return "", errors.New("emoji_id 不能为空")
	}
	req := &api.SendCustomFace{EmojiID: emojiID, ReplyTo: replyTo}
	switch chatType {
	case "group":
		req.GroupID = chatID
	case "private":
		req.UserID = chatID
	default:
		return "", fmt.Errorf("未知会话类型 %q", chatType)
	}
	data, err := callNapCat(req)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// qqFaceDescOr 备注优先、summary 兜底，作为 QQ 收藏表情的 desc。
func qqFaceDescOr(note, summary string) string {
	if d := strings.TrimSpace(note); d != "" {
		return d
	}
	return strings.TrimSpace(summary)
}
