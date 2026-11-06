package api

import "encoding/json"

// 扩展接口、AI 扩展、QQ空间扩展与群相册(来源: https://napcat.apifox.cn/llms.txt 各动作详情页)
// feed/comment/reply 等要求"原样传回"的对象用 RawMessage 透传。

// ---------------- 扩展接口 ----------------

// SetGroupKickMembers 批量踢出群成员 POST /set_group_kick_members
type SetGroupKickMembers struct {
	GroupID          int64   `json:"group_id"`
	UserIDs          []int64 `json:"user_id"`
	RejectAddRequest bool    `json:"reject_add_request,omitempty"`
}

func (r *SetGroupKickMembers) Path() string { return "/set_group_kick_members" }

// CreateCollection 创建收藏 POST /create_collection
type CreateCollection struct {
	RawData string `json:"rawData"`
	Brief   string `json:"brief"`
}

func (r *CreateCollection) Path() string { return "/create_collection" }

// SetSelfLongNick 设置个性签名 POST /set_self_longnick
type SetSelfLongNick struct {
	LongNick string `json:"longNick"`
}

func (r *SetSelfLongNick) Path() string { return "/set_self_longnick" }

// SetQQAvatar 设置QQ头像 POST /set_qq_avatar
type SetQQAvatar struct {
	File string `json:"file"`
}

func (r *SetQQAvatar) Path() string { return "/set_qq_avatar" }

// TranslateEn2Zh 英文单词翻译 POST /translate_en2zh
type TranslateEn2Zh struct {
	Words []string `json:"words"`
}

func (r *TranslateEn2Zh) Path() string { return "/translate_en2zh" }

// GetClientKey 获取 ClientKey POST /get_clientkey
type GetClientKey struct{}

func (r *GetClientKey) Path() string { return "/get_clientkey" }

// SendQzoneMsg 发表QQ空间说说 POST /send_qzone_msg
type SendQzoneMsg struct {
	Content    string   `json:"content"`
	Images     []string `json:"images,omitempty"`
	UGCRight   int      `json:"ugc_right,omitempty"`
	TargetUins []int64  `json:"target_uins,omitempty"`
}

func (r *SendQzoneMsg) Path() string { return "/send_qzone_msg" }

// OCRImage 图片 OCR 识别 POST /ocr_image
type OCRImage struct {
	Image string `json:"image"`
}

func (r *OCRImage) Path() string { return "/ocr_image" }

// DeleteQzoneMsg 删除QQ空间说说 POST /delete_qzone_msg
type DeleteQzoneMsg struct {
	TID string `json:"tid"`
}

func (r *DeleteQzoneMsg) Path() string { return "/delete_qzone_msg" }

// OCRImageInternal 图片 OCR 识别(内部) POST /.ocr_image
type OCRImageInternal struct {
	Image string `json:"image"`
}

func (r *OCRImageInternal) Path() string { return "/.ocr_image" }

// SetGroupSpecialTitle 设置专属头衔 POST /set_group_special_title
type SetGroupSpecialTitle struct {
	GroupID      int64  `json:"group_id"`
	UserID       int64  `json:"user_id"`
	SpecialTitle string `json:"special_title"`
}

func (r *SetGroupSpecialTitle) Path() string { return "/set_group_special_title" }

// GetAICharacters 获取AI角色列表 POST /get_ai_characters
type GetAICharacters struct {
	GroupID  int64 `json:"group_id"`
	ChatType int   `json:"chat_type"`
}

func (r *GetAICharacters) Path() string { return "/get_ai_characters" }

// GetQzoneFeeds 获取QQ空间好友动态 POST /get_qzone_feeds
type GetQzoneFeeds struct {
	Format  string `json:"format,omitempty"`
	PageNum int    `json:"page_num,omitempty"`
	Count   int    `json:"count,omitempty"`
	Cursor  string `json:"cursor,omitempty"`
}

func (r *GetQzoneFeeds) Path() string { return "/get_qzone_feeds" }

// GetQzoneMsgList 获取QQ空间说说列表 POST /get_qzone_msg_list
type GetQzoneMsgList struct {
	Format    string `json:"format,omitempty"`
	TargetUin int64  `json:"target_uin,omitempty"`
	Pos       int    `json:"pos,omitempty"`
	Num       int    `json:"num,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
}

func (r *GetQzoneMsgList) Path() string { return "/get_qzone_msg_list" }

// GetQzoneMsgDetail 获取QQ空间说说详情 POST /get_qzone_msg_detail
type GetQzoneMsgDetail struct {
	FeedRef   json.RawMessage `json:"feed_ref,omitempty"`
	TID       string          `json:"tid,omitempty"`
	TargetUin int64           `json:"target_uin,omitempty"`
	Pos       int             `json:"pos,omitempty"`
	Num       int             `json:"num,omitempty"`
}

func (r *GetQzoneMsgDetail) Path() string { return "/get_qzone_msg_detail" }

// GetQzoneCommentList 获取QQ空间说说评论 POST /get_qzone_comment_list
type GetQzoneCommentList struct {
	FeedRef json.RawMessage `json:"feed_ref"`
	Cursor  string          `json:"cursor,omitempty"`
}

func (r *GetQzoneCommentList) Path() string { return "/get_qzone_comment_list" }

// GetQzoneCommentReplies 获取QQ空间评论楼中楼回复 POST /get_qzone_comment_replies
type GetQzoneCommentReplies struct {
	FeedRef json.RawMessage `json:"feed_ref"`
	Comment json.RawMessage `json:"comment"`
	Cursor  string          `json:"cursor,omitempty"`
}

func (r *GetQzoneCommentReplies) Path() string { return "/get_qzone_comment_replies" }

// LikeQzone 点赞QQ空间说说 POST /like_qzone
type LikeQzone struct {
	FeedRef   json.RawMessage `json:"feed_ref,omitempty"`
	TID       string          `json:"tid,omitempty"`
	TargetUin int64           `json:"target_uin,omitempty"`
	Abstime   int64           `json:"abstime,omitempty"`
}

func (r *LikeQzone) Path() string { return "/like_qzone" }

// UnlikeQzone 取消QQ空间说说点赞 POST /unlike_qzone
type UnlikeQzone struct {
	FeedRef   json.RawMessage `json:"feed_ref,omitempty"`
	TID       string          `json:"tid,omitempty"`
	TargetUin int64           `json:"target_uin,omitempty"`
	Abstime   int64           `json:"abstime,omitempty"`
}

func (r *UnlikeQzone) Path() string { return "/unlike_qzone" }

// CommentQzone 评论QQ空间说说 POST /comment_qzone
type CommentQzone struct {
	FeedRef    json.RawMessage `json:"feed_ref,omitempty"`
	TID        string          `json:"tid,omitempty"`
	TargetUin  int64           `json:"target_uin,omitempty"`
	Content    string          `json:"content"`
	Comment    json.RawMessage `json:"comment,omitempty"`
	ReplyUser  json.RawMessage `json:"reply_user,omitempty"`
	CommentID  string          `json:"comment_id,omitempty"`
	CommentUin int64           `json:"comment_uin,omitempty"`
	ReplyUin   int64           `json:"reply_uin,omitempty"`
	ReplyName  string          `json:"reply_name,omitempty"`
	Images     []string        `json:"images,omitempty"`
}

func (r *CommentQzone) Path() string { return "/comment_qzone" }

// GetQzoneProfile 获取QQ空间资料 POST /get_qzone_profile
type GetQzoneProfile struct {
	TargetUin int64 `json:"target_uin,omitempty"`
}

func (r *GetQzoneProfile) Path() string { return "/get_qzone_profile" }

// SetQzoneProfile 修改QQ空间资料 POST /set_qzone_profile
type SetQzoneProfile struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

func (r *SetQzoneProfile) Path() string { return "/set_qzone_profile" }

// SetQzoneMsgRight 修改QQ空间说说权限 POST /set_qzone_msg_right
type SetQzoneMsgRight struct {
	TID        string  `json:"tid"`
	UGCRight   int     `json:"ugc_right"`
	TargetUins []int64 `json:"target_uins,omitempty"`
}

func (r *SetQzoneMsgRight) Path() string { return "/set_qzone_msg_right" }

// SetQzoneBan 设置QQ空间访问限制 POST /set_qzone_ban
type SetQzoneBan struct {
	UserID int64 `json:"user_id"`
	Enable bool  `json:"enable,omitempty"`
}

func (r *SetQzoneBan) Path() string { return "/set_qzone_ban" }

// ---------------- AI 扩展 ----------------

// GetAIRecord 获取 AI 语音 POST /get_ai_record
type GetAIRecord struct {
	Character string `json:"character"`
	GroupID   int64  `json:"group_id"`
	Text      string `json:"text"`
}

func (r *GetAIRecord) Path() string { return "/get_ai_record" }

// SendGroupAIRecord 发送群 AI 语音 POST /send_group_ai_record
type SendGroupAIRecord struct {
	Character string `json:"character"`
	GroupID   int64  `json:"group_id"`
	Text      string `json:"text"`
}

func (r *SendGroupAIRecord) Path() string { return "/send_group_ai_record" }

// ---------------- QQ空间扩展(空友爱看) ----------------

// GetQzonePublicCapabilities 获取空友爱看接口能力 POST /get_qzone_public_capabilities
type GetQzonePublicCapabilities struct{}

func (r *GetQzonePublicCapabilities) Path() string { return "/get_qzone_public_capabilities" }

// GetQzonePublicFeeds 获取空友爱看动态 POST /get_qzone_public_feeds
type GetQzonePublicFeeds struct {
	Cursor string `json:"cursor,omitempty"`
}

func (r *GetQzonePublicFeeds) Path() string { return "/get_qzone_public_feeds" }

// GetQzonePublicFeedDetail 获取空友爱看动态详情 POST /get_qzone_public_feed_detail
type GetQzonePublicFeedDetail struct {
	Feed json.RawMessage `json:"feed"`
}

func (r *GetQzonePublicFeedDetail) Path() string { return "/get_qzone_public_feed_detail" }

// GetQzonePublicComments 获取空友爱看评论 POST /get_qzone_public_comments
type GetQzonePublicComments struct {
	Feed   json.RawMessage `json:"feed"`
	Cursor string          `json:"cursor"`
}

func (r *GetQzonePublicComments) Path() string { return "/get_qzone_public_comments" }

// GetQzonePublicReplies 获取空友爱看评论回复 POST /get_qzone_public_replies
type GetQzonePublicReplies struct {
	Feed     json.RawMessage `json:"feed"`
	Comment  json.RawMessage `json:"comment"`
	Cursor   string          `json:"cursor,omitempty"`
	PageSize int             `json:"page_size,omitempty"`
}

func (r *GetQzonePublicReplies) Path() string { return "/get_qzone_public_replies" }

// SetQzonePublicUpvote 推或取消推空友爱看动态 POST /set_qzone_public_upvote
type SetQzonePublicUpvote struct {
	Feed   json.RawMessage `json:"feed"`
	Enable bool            `json:"enable,omitempty"`
}

func (r *SetQzonePublicUpvote) Path() string { return "/set_qzone_public_upvote" }

// SetQzonePublicLike 设置空友爱看普通赞 POST /set_qzone_public_like
type SetQzonePublicLike struct {
	Feed   json.RawMessage `json:"feed"`
	Enable bool            `json:"enable,omitempty"`
}

func (r *SetQzonePublicLike) Path() string { return "/set_qzone_public_like" }

// SetQzonePublicFollow 蹲或取消蹲空友爱看作者 POST /set_qzone_public_follow
type SetQzonePublicFollow struct {
	Feed   json.RawMessage `json:"feed"`
	Enable bool            `json:"enable,omitempty"`
}

func (r *SetQzonePublicFollow) Path() string { return "/set_qzone_public_follow" }

// SetQzonePublicCommentLike 设置空友爱看评论或回复赞 POST /set_qzone_public_comment_like
type SetQzonePublicCommentLike struct {
	Feed    json.RawMessage `json:"feed"`
	Comment json.RawMessage `json:"comment"`
	Reply   json.RawMessage `json:"reply,omitempty"`
	Enable  bool            `json:"enable,omitempty"`
}

func (r *SetQzonePublicCommentLike) Path() string { return "/set_qzone_public_comment_like" }

// CommentQzonePublicFeed 评论空友爱看动态 POST /comment_qzone_public_feed
// content 兼容非空文本或富文本对象数组,用 RawMessage 透传
type CommentQzonePublicFeed struct {
	Feed    json.RawMessage `json:"feed"`
	Content json.RawMessage `json:"content"`
}

func (r *CommentQzonePublicFeed) Path() string { return "/comment_qzone_public_feed" }

// ReplyQzonePublicFeed 回复空友爱看评论 POST /reply_qzone_public_feed
type ReplyQzonePublicFeed struct {
	Feed    json.RawMessage `json:"feed"`
	Comment json.RawMessage `json:"comment"`
	Content json.RawMessage `json:"content"`
	Reply   json.RawMessage `json:"reply,omitempty"`
}

func (r *ReplyQzonePublicFeed) Path() string { return "/reply_qzone_public_feed" }

// DeleteQzonePublicComment 删除空友爱看评论 POST /delete_qzone_public_comment
type DeleteQzonePublicComment struct {
	Feed    json.RawMessage `json:"feed"`
	Comment json.RawMessage `json:"comment"`
}

func (r *DeleteQzonePublicComment) Path() string { return "/delete_qzone_public_comment" }

// DeleteQzonePublicReply 删除空友爱看回复 POST /delete_qzone_public_reply
type DeleteQzonePublicReply struct {
	Feed    json.RawMessage `json:"feed"`
	Comment json.RawMessage `json:"comment"`
	Reply   json.RawMessage `json:"reply"`
}

func (r *DeleteQzonePublicReply) Path() string { return "/delete_qzone_public_reply" }

// ForwardQzonePublicFeed 转发空友爱看动态 POST /forward_qzone_public_feed
type ForwardQzonePublicFeed struct {
	Feed   json.RawMessage `json:"feed"`
	Reason string          `json:"reason,omitempty"`
}

func (r *ForwardQzonePublicFeed) Path() string { return "/forward_qzone_public_feed" }

// ---------------- 群相册 ----------------

// DelGroupAlbumMedia 删除群相册媒体 POST /del_group_album_media
type DelGroupAlbumMedia struct {
	GroupID int64  `json:"group_id"`
	AlbumID string `json:"album_id"`
	Lloc    string `json:"lloc"`
}

func (r *DelGroupAlbumMedia) Path() string { return "/del_group_album_media" }

// SetGroupAlbumMediaLike 点赞群相册媒体 POST /set_group_album_media_like
type SetGroupAlbumMediaLike struct {
	GroupID int64  `json:"group_id"`
	AlbumID string `json:"album_id"`
	BatchID string `json:"batch_id"`
	Lloc    string `json:"lloc,omitempty"`
}

func (r *SetGroupAlbumMediaLike) Path() string { return "/set_group_album_media_like" }

// CancelGroupAlbumMediaLike 取消点赞群相册媒体 POST /cancel_group_album_media_like
type CancelGroupAlbumMediaLike struct {
	GroupID int64  `json:"group_id"`
	AlbumID string `json:"album_id"`
	BatchID string `json:"batch_id"`
	Lloc    string `json:"lloc,omitempty"`
}

func (r *CancelGroupAlbumMediaLike) Path() string { return "/cancel_group_album_media_like" }

// DoGroupAlbumComment 发表群相册评论 POST /do_group_album_comment
type DoGroupAlbumComment struct {
	GroupID int64  `json:"group_id"`
	AlbumID string `json:"album_id"`
	Lloc    string `json:"lloc,omitempty"`
	BatchID string `json:"batch_id,omitempty"`
	Content string `json:"content"`
}

func (r *DoGroupAlbumComment) Path() string { return "/do_group_album_comment" }

// GetGroupAlbumMediaList 获取群相册媒体列表 POST /get_group_album_media_list
type GetGroupAlbumMediaList struct {
	GroupID    int64  `json:"group_id"`
	AlbumID    string `json:"album_id"`
	AttachInfo string `json:"attach_info,omitempty"`
}

func (r *GetGroupAlbumMediaList) Path() string { return "/get_group_album_media_list" }

// GetQunAlbumList 获取群相册列表 POST /get_qun_album_list
type GetQunAlbumList struct {
	GroupID    int64  `json:"group_id"`
	AttachInfo string `json:"attach_info,omitempty"`
}

func (r *GetQunAlbumList) Path() string { return "/get_qun_album_list" }

// UploadImageToQunAlbum 上传图片到群相册 POST /upload_image_to_qun_album
type UploadImageToQunAlbum struct {
	GroupID   int64  `json:"group_id"`
	AlbumID   string `json:"album_id"`
	AlbumName string `json:"album_name"`
	File      string `json:"file"`
}

func (r *UploadImageToQunAlbum) Path() string { return "/upload_image_to_qun_album" }

// UploadImagesToQunAlbum 上传多张图片到群相册 POST /upload_images_to_qun_album
type UploadImagesToQunAlbum struct {
	GroupID   int64    `json:"group_id"`
	AlbumID   string   `json:"album_id"`
	AlbumName string   `json:"album_name"`
	Files     []string `json:"files"`
}

func (r *UploadImagesToQunAlbum) Path() string { return "/upload_images_to_qun_album" }

// UploadVideoToQunAlbum 上传视频到群相册 POST /upload_video_to_qun_album
type UploadVideoToQunAlbum struct {
	GroupID   int64  `json:"group_id"`
	AlbumID   string `json:"album_id"`
	AlbumName string `json:"album_name"`
	File      string `json:"file"`
}

func (r *UploadVideoToQunAlbum) Path() string { return "/upload_video_to_qun_album" }
