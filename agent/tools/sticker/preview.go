// sticker_preview.go 表情预览：收藏时尽量下载一张预览图落到图片缓存，
// 让模型能用 view_sticker 真正“看见”表情，而不是只看到一个 ID。
//
// 来源优先级：
//   - image：消息段里的 url
//   - mface：段里若带 url 就用；否则按 emoji_id（md5）推导 QQ 表情 CDN
//     （gxh.vip.qq.com/club/item/parcel/item/<前两位>/<md5>/300x300.png）
//   - face：内置小黄脸没有可下载图片，只保留名称
//
// 预览下载失败不影响收藏本身：记录留空，之后 view_sticker 会重试。
package sticker

import (
	"regexp"
	"strings"

	"Q-Mi/storage"
)

// md5HexRe QQ 商城表情的 emoji_id 通常是 32 位小写十六进制 md5。
var md5HexRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// mfaceCDNURL 由商城表情 emoji_id 推导官方 CDN 预览图地址；形态不符返回空串。
func MFaceCDNURL(emojiID string) string {
	id := strings.ToLower(strings.TrimSpace(emojiID))
	if !md5HexRe.MatchString(id) {
		return ""
	}
	return "https://gxh.vip.qq.com/club/item/parcel/item/" + id[:2] + "/" + id + "/300x300.png"
}

// stickerPreviewCandidates 按优先级给出预览图候选地址（去重由调用方做）。
func PreviewCandidates(kind, segURL, emojiID string) []string {
	switch kind {
	case "image":
		return []string{strings.TrimSpace(segURL)}
	case "mface":
		out := make([]string, 0, 3)
		if u := strings.TrimSpace(segURL); u != "" {
			out = append(out, u)
		}
		if cdn := MFaceCDNURL(emojiID); cdn != "" {
			out = append(out, cdn, strings.TrimSuffix(cdn, ".png")+".gif")
		}
		return out
	default:
		return nil
	}
}

// cacheStickerPreview 依次尝试候选地址，首个成功者落盘到图片缓存。
// 返回内容哈希、MIME 与实际命中的 URL；全部失败返回空串。
func cacheStickerPreview(ctx *Dependencies, candidates []string) (string, string, string) {
	if ctx == nil || ctx.Images == nil {
		return "", "", ""
	}
	seen := make(map[string]bool, len(candidates))
	for _, url := range candidates {
		url = strings.TrimSpace(url)
		if url == "" || seen[url] {
			continue
		}
		seen[url] = true
		data, mime, err := ctx.Images.Download(url)
		if err != nil || mime == "" {
			continue
		}
		return ctx.Images.Save(0, 0, data, mime), mime, url
	}
	return "", "", ""
}

// ensureStickerPreview 返回可直接附给模型的 data URL：
// 已有缓存读盘；文件被清理或从未下载时按当前记录重试一次并写回。
func EnsurePreview(ctx *Dependencies, st *storage.Sticker) string {
	if ctx == nil || st == nil || ctx.Images == nil {
		return ""
	}
	if st.PreviewSHA != "" {
		if du := ctx.Images.DataURLFromRef(storage.ImageRef{Sha256: st.PreviewSHA, Mime: st.PreviewMime}); du != "" {
			return du
		}
	}
	sha, mime, _ := cacheStickerPreview(ctx, PreviewCandidates(st.Kind, st.URL, st.EmojiID))
	if sha == "" {
		return ""
	}
	if ctx.Store != nil {
		if err := ctx.Store.UpdateSticker(st.ID, storage.StickerUpdate{
			PreviewSHA: &sha, PreviewMime: &mime,
		}); err == nil {
			st.PreviewSHA, st.PreviewMime = sha, mime
		}
	}
	return ctx.Images.DataURLFromRef(storage.ImageRef{Sha256: sha, Mime: mime})
}

// stickerDisplayName 展示名：备注优先，其次 QQ 上报的 summary，最后退化为 kind。
func stickerDisplayName(st storage.Sticker) string {
	if strings.TrimSpace(st.Note) != "" {
		return st.Note
	}
	if strings.TrimSpace(st.Summary) != "" {
		return st.Summary
	}
	return st.Kind
}
