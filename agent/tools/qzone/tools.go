package qzone

import (
	"encoding/json"
	"fmt"
	"time"

	"Q-Mi/agent/tool"
	"Q-Mi/api"
)

// Tools QQ 空间板块（全部走 SnowLuma 形态：tid/target_uin，
// 不用 feed_ref，两种定位方式不能混用）。
func Tools(ctx *Dependencies) []Tool {
	if ctx == nil {
		ctx = &Dependencies{}
	}
	return []Tool{
		{
			Name:        "post_qzone",
			Description: "在自己的 QQ 空间发一条说说：text 必填；images 可选，传图片链接数组（http/https/file/base64 均可，NapCat 自动上传）。空间是公开内容，发布前想清楚。",
			Parameters: tool.ObjectParams(map[string]any{
				"text":   tool.StringProp("说说文字内容（不能为空）"),
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
				return passthrough(ctx.call(&api.SendQzoneMsg{Content: p.Text, Images: p.Images}))
			},
		},
		{
			Name:        "browse_qzone_feeds",
			Description: "刷好友空间动态（每页最多 10 条：uin/author/time/summary/images/image_count/tid，has_more 表示可翻页）。只返回图片链接，不自动看图；选 images 中感兴趣的链接用 view_qzone_images。细看文字或互动用 read_qzone_post / qzone_like / comment_qzone。",
			Parameters: tool.ObjectParams(map[string]any{
				"page_num": tool.IntProp("可选，页码，默认 1"),
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
				data, err := ctx.call(&api.GetQzoneFeeds{PageNum: page})
				if err != nil {
					return "", err
				}
				var resp struct {
					Feeds []struct {
						Uin      int64    `json:"uin"`
						Nickname string   `json:"nickname"`
						Time     int64    `json:"time"`
						TID      *string  `json:"tid"`
						Content  *string  `json:"content"`
						Images   []string `json:"images"`
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
					var imageCount any
					if f.Images != nil {
						imageCount = len(f.Images)
					}
					rows = append(rows, map[string]any{
						"uin":         f.Uin,
						"author":      f.Nickname,
						"time":        time.Unix(f.Time, 0).Format(timeLayout),
						"summary":     truncateRunes(content, maxQzoneSummaryRunes),
						"images":      f.Images,
						"image_count": imageCount,
						"tid":         tid,
					})
				}
				return marshalCompact(map[string]any{"feeds": rows, "has_more": resp.HasMore})
			},
		},
		{
			Name:        "read_qzone_post",
			Description: "读空间说说正文与首批评论（正文限 200 字、评论限 10 条、单条限 100 字）。tid 来自 browse_qzone_feeds；target_uin 是作者 QQ，默认自己。接口若提供 images 则保留链接；为 null 时不代表无图，可用浏览列表中的链接调用 view_qzone_images。",
			Parameters: tool.ObjectParams(map[string]any{
				"tid":        tool.StringProp("说说 tid"),
				"target_uin": tool.IntProp("可选，说说作者的 QQ 号，默认自己的空间"),
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
				data, err := ctx.call(&api.GetQzoneMsgDetail{TID: p.TID, TargetUin: p.TargetUin})
				if err != nil {
					return "", err
				}
				var resp struct {
					Content    *string  `json:"content"`
					Images     []string `json:"images"`
					CommentNum int64    `json:"comment_num"`
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
				var imageCount any
				if resp.Images != nil {
					imageCount = len(resp.Images)
				}
				return marshalCompact(map[string]any{
					"tid":         p.TID,
					"content":     truncateRunes(content, maxQzonePostContentRunes),
					"images":      resp.Images,
					"image_count": imageCount,
					"comment_num": resp.CommentNum,
					"comments":    comments,
				})
			},
		},
		viewQzoneImagesTool(ctx),
		{
			Name:        "qzone_like",
			Description: "给一条空间说说点赞 / 取消点赞（enable 默认 true 点赞，false 取消）。tid 来自 browse_qzone_feeds；target_uin 是说说作者的 QQ 号，默认自己的空间。点赞要自然：真有感触才点，别批量刷。",
			Parameters: tool.ObjectParams(map[string]any{
				"tid":        tool.StringProp("说说 tid"),
				"target_uin": tool.IntProp("可选，说说作者的 QQ 号，默认自己的空间"),
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
				return emptyOK(ctx.call(req))
			},
		},
		{
			Name:        "comment_qzone",
			Description: "给一条空间说说发一级评论。content 必填；target_uin 是说说作者的 QQ 号，默认自己的空间。楼中楼回复暂不支持（可经 napcat_api 调 comment_qzone 的回复参数）。评论对内容说话，别复制粘贴同一句到处刷。",
			Parameters: tool.ObjectParams(map[string]any{
				"tid":        tool.StringProp("说说 tid"),
				"content":    tool.StringProp("评论文本（不能为空）"),
				"target_uin": tool.IntProp("可选，说说作者的 QQ 号，默认自己的空间"),
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
				return passthrough(ctx.call(&api.CommentQzone{TID: p.TID, Content: p.Content, TargetUin: p.TargetUin}))
			},
		},
		{
			Name:        "qzone_profile",
			Description: "查看自己 QQ 空间的名称与说明。",
			Parameters:  tool.ObjectParams(nil, nil),
			Run: func(json.RawMessage) (string, error) {
				return passthrough(ctx.call(&api.GetQzoneProfile{}))
			},
		},
		{
			Name:        "delete_qzone",
			Description: "删除自己发的一条空间说说（危险操作，需要管理员权限，删前想清楚）。tid 来自 browse_qzone_feeds / read_qzone_post。",
			Parameters: tool.ObjectParams(map[string]any{
				"tid": tool.StringProp("要删除的说说 tid"),
			}, []string{"tid"}),
			Run: func(args json.RawMessage) (string, error) {
				if err := ctx.authorize("delete_qzone_msg"); err != nil {
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
				return emptyOK(ctx.call(&api.DeleteQzoneMsg{TID: p.TID}))
			},
		},
	}
}
