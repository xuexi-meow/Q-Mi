package agent

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

const maxQzoneImagesPerView = 5

func viewQzoneImagesTool(ctx *ToolContext) Tool {
	return Tool{
		Name:        "view_qzone_images",
		Description: "按需查看 QQ 空间图片：传入动态 images 中选中的图片链接，最多 5 张；成功下载的图片会作为多模态附件进入下一次模型请求。仅拿到链接不等于看过图片，失败项会明确列出。",
		Parameters: objectParams(map[string]any{
			"urls": map[string]any{
				"type": "array", "items": map[string]any{"type": "string"},
				"minItems": 1, "maxItems": maxQzoneImagesPerView,
				"description": "从 browse_qzone_feeds/read_qzone_post 或 napcat_api 空间结果中选取的 http(s) 图片链接，按希望查看的顺序传入",
			},
		}, []string{"urls"}),
		Usage: "先浏览动态，再选择感兴趣的 images 链接调用，例如 `{\"urls\":[\"https://example.com/photo.jpg\"]}`。attached_urls 与附件顺序对应；skipped 未成功附图。images 为 null 表示接口未提供图片信息，不代表没有图。链接失效时重新查询动态获取新链接，不要猜测图中内容。",
		Run: func(args json.RawMessage) (string, error) {
			if ctx.Turn == nil || ctx.Images == nil {
				return "", fmt.Errorf("当前活动或图片存储不可用，无法附入空间图片")
			}
			var p struct {
				URLs []string `json:"urls"`
			}
			if err := decodeArgs(args, &p); err != nil {
				return "", err
			}
			if len(p.URLs) < 1 || len(p.URLs) > maxQzoneImagesPerView {
				return "", fmt.Errorf("urls 需要 1 ~ %d 个图片链接，请选择后分批查看", maxQzoneImagesPerView)
			}
			// Validate the whole selection before downloading or queuing any image.
			for i, raw := range p.URLs {
				raw = strings.TrimSpace(raw)
				u, err := url.Parse(raw)
				if err != nil || u.Hostname() == "" || u.User != nil ||
					(u.Scheme != "http" && u.Scheme != "https") {
					return "", fmt.Errorf("urls 第 %d 项必须是不含登录凭据的 http(s) 图片链接", i+1)
				}
				p.URLs[i] = raw
			}
			attached := make([]string, 0, len(p.URLs))
			skipped := make([]map[string]string, 0)
			for _, source := range p.URLs {
				du := ctx.Images.dataURLFor(0, 0, source)
				if !strings.HasPrefix(du, "data:") {
					skipped = append(skipped, map[string]string{
						"url":    source,
						"reason": "下载失败：链接可能已过期、访问受限或图片格式不受支持；未附图，不能视为已看过",
					})
					continue
				}
				ctx.Turn.AddPendingImage(du)
				attached = append(attached, source)
			}
			note := fmt.Sprintf("已附入 %d 张图片到下一次模型请求，顺序与 attached_urls 一致；skipped 未附图", len(attached))
			if len(attached) == 0 {
				note = "没有图片成功附入上下文，不能据此描述图片；可重新查询动态获取新链接或跳过"
			}
			return marshalCompact(map[string]any{
				"total": len(p.URLs), "attached": len(attached),
				"attached_urls": attached, "skipped": skipped, "note": note,
			})
		},
	}
}
