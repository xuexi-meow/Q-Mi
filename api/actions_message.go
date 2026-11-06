package api

import apimsg "Q-Mi/api/message"

// 消息接口与消息扩展(来源: https://napcat.apifox.cn/llms.txt 各动作详情页)
// 发送私聊/群消息、获取消息、撤回消息已在 api.go 定义,此处不重复。

// ForwardNews 合并转发消息的 news 摘要项
type ForwardNews struct {
	Text string `json:"text"`
}

// ---------------- 消息接口 ----------------

// SendMsg 发送消息(私聊或群聊) POST /send_msg
type SendMsg struct {
	MessageType string            `json:"message_type,omitempty"`
	UserID      int64             `json:"user_id,omitempty"`
	GroupID     int64             `json:"group_id,omitempty"`
	Message     []*apimsg.Message `json:"message"`
	AutoEscape  bool              `json:"auto_escape,omitempty"`
	Source      string            `json:"source,omitempty"`
	News        []ForwardNews     `json:"news,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Prompt      string            `json:"prompt,omitempty"`
	Timeout     int               `json:"timeout,omitempty"`
}

func (r *SendMsg) Path() string { return "/send_msg" }

// ForwardFriendSingleMsg 转发单条消息(好友) POST /forward_friend_single_msg
type ForwardFriendSingleMsg struct {
	MessageID int64 `json:"message_id"`
	GroupID   int64 `json:"group_id,omitempty"`
	UserID    int64 `json:"user_id,omitempty"`
}

func (r *ForwardFriendSingleMsg) Path() string { return "/forward_friend_single_msg" }

// ForwardGroupSingleMsg 转发单条消息(群聊) POST /forward_group_single_msg
type ForwardGroupSingleMsg struct {
	MessageID int64 `json:"message_id"`
	GroupID   int64 `json:"group_id,omitempty"`
	UserID    int64 `json:"user_id,omitempty"`
}

func (r *ForwardGroupSingleMsg) Path() string { return "/forward_group_single_msg" }

// MarkGroupMsgAsRead 标记群聊已读 POST /mark_group_msg_as_read
type MarkGroupMsgAsRead struct {
	UserID    int64  `json:"user_id,omitempty"`
	GroupID   int64  `json:"group_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
}

func (r *MarkGroupMsgAsRead) Path() string { return "/mark_group_msg_as_read" }

// MarkPrivateMsgAsRead 标记私聊已读 POST /mark_private_msg_as_read
type MarkPrivateMsgAsRead struct {
	UserID    int64  `json:"user_id,omitempty"`
	GroupID   int64  `json:"group_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
}

func (r *MarkPrivateMsgAsRead) Path() string { return "/mark_private_msg_as_read" }

// MarkMsgAsRead 标记消息已读(Go-CQHTTP) POST /mark_msg_as_read
type MarkMsgAsRead struct {
	UserID    int64  `json:"user_id,omitempty"`
	GroupID   int64  `json:"group_id,omitempty"`
	MessageID string `json:"message_id,omitempty"`
}

func (r *MarkMsgAsRead) Path() string { return "/mark_msg_as_read" }

// MarkAllAsRead 标记所有消息已读 POST /_mark_all_as_read
type MarkAllAsRead struct{}

func (r *MarkAllAsRead) Path() string { return "/_mark_all_as_read" }

// SendForwardMsg 发送合并转发消息 POST /send_forward_msg
type SendForwardMsg struct {
	MessageType string            `json:"message_type,omitempty"`
	UserID      int64             `json:"user_id,omitempty"`
	GroupID     int64             `json:"group_id,omitempty"`
	Message     []*apimsg.Message `json:"message"`
	AutoEscape  bool              `json:"auto_escape,omitempty"`
	Source      string            `json:"source,omitempty"`
	News        []ForwardNews     `json:"news,omitempty"`
	Summary     string            `json:"summary,omitempty"`
	Prompt      string            `json:"prompt,omitempty"`
	Timeout     int               `json:"timeout,omitempty"`
}

func (r *SendForwardMsg) Path() string { return "/send_forward_msg" }

// SendGroupForwardMsg 发送群合并转发消息 POST /send_group_forward_msg
type SendGroupForwardMsg struct {
	GroupID    int64             `json:"group_id"`
	Message    []*apimsg.Message `json:"message"`
	AutoEscape bool              `json:"auto_escape,omitempty"`
	Source     string            `json:"source,omitempty"`
	News       []ForwardNews     `json:"news,omitempty"`
	Summary    string            `json:"summary,omitempty"`
	Prompt     string            `json:"prompt,omitempty"`
	Timeout    int               `json:"timeout,omitempty"`
}

func (r *SendGroupForwardMsg) Path() string { return "/send_group_forward_msg" }

// SendPrivateForwardMsg 发送私聊合并转发消息 POST /send_private_forward_msg
type SendPrivateForwardMsg struct {
	UserID     int64             `json:"user_id"`
	Message    []*apimsg.Message `json:"message"`
	AutoEscape bool              `json:"auto_escape,omitempty"`
	Source     string            `json:"source,omitempty"`
	News       []ForwardNews     `json:"news,omitempty"`
	Summary    string            `json:"summary,omitempty"`
	Prompt     string            `json:"prompt,omitempty"`
	Timeout    int               `json:"timeout,omitempty"`
}

func (r *SendPrivateForwardMsg) Path() string { return "/send_private_forward_msg" }

// GetForwardMsg 获取合并转发消息 POST /get_forward_msg
type GetForwardMsg struct {
	MessageID string `json:"message_id,omitempty"`
	ID        string `json:"id,omitempty"`
}

func (r *GetForwardMsg) Path() string { return "/get_forward_msg" }

// GetMsgHistoryData 历史消息接口返回的业务数据(messages 数组复用 GetMsgData)
type GetMsgHistoryData struct {
	Messages []GetMsgData `json:"messages"`
}

// GetGroupMsgHistory 获取群历史消息 POST /get_group_msg_history
type GetGroupMsgHistory struct {
	GroupID       int64 `json:"group_id"`
	MessageSeq    int64 `json:"message_seq,omitempty"`
	Count         int   `json:"count"`
	ReverseOrder  bool  `json:"reverse_order"`
	DisableGetURL bool  `json:"disable_get_url"`
	ParseMultMsg  bool  `json:"parse_mult_msg"`
	QuickReply    bool  `json:"quick_reply"`
	ReverseOrder2 bool  `json:"reverseOrder,omitempty"` // 旧版本兼容字段
}

func (r *GetGroupMsgHistory) Path() string { return "/get_group_msg_history" }

// GetFriendMsgHistory 获取好友历史消息 POST /get_friend_msg_history
type GetFriendMsgHistory struct {
	UserID        int64 `json:"user_id"`
	MessageSeq    int64 `json:"message_seq,omitempty"`
	Count         int   `json:"count"`
	ReverseOrder  bool  `json:"reverse_order"`
	DisableGetURL bool  `json:"disable_get_url"`
	ParseMultMsg  bool  `json:"parse_mult_msg"`
	QuickReply    bool  `json:"quick_reply"`
	ReverseOrder2 bool  `json:"reverseOrder,omitempty"` // 旧版本兼容字段
}

func (r *GetFriendMsgHistory) Path() string { return "/get_friend_msg_history" }

// ---------------- 消息扩展 ----------------

// FetchEmojiLike 获取表情点赞详情 POST /fetch_emoji_like
type FetchEmojiLike struct {
	MessageID int64  `json:"message_id"`
	EmojiID   string `json:"emojiId"`
	EmojiType string `json:"emojiType"`
	Count     int    `json:"count"`
	Cookie    string `json:"cookie"`
}

func (r *FetchEmojiLike) Path() string { return "/fetch_emoji_like" }

// GetEmojiLikes 获取消息表情点赞列表 POST /get_emoji_likes
type GetEmojiLikes struct {
	GroupID   int64  `json:"group_id,omitempty"`
	MessageID string `json:"message_id"`
	EmojiID   string `json:"emoji_id"`
	EmojiType string `json:"emoji_type,omitempty"`
	Count     int    `json:"count"`
}

func (r *GetEmojiLikes) Path() string { return "/get_emoji_likes" }

// FetchPttText 获取语音转文字结果 POST /fetch_ptt_text
type FetchPttText struct {
	MessageID int64 `json:"message_id"`
}

func (r *FetchPttText) Path() string { return "/fetch_ptt_text" }

// ArkShareGroup 分享群(Ark) POST /ArkShareGroup
type ArkShareGroup struct {
	GroupID int64 `json:"group_id"`
}

func (r *ArkShareGroup) Path() string { return "/ArkShareGroup" }

// ArkSharePeer 分享用户(Ark) POST /ArkSharePeer
type ArkSharePeer struct {
	UserID      int64  `json:"user_id,omitempty"`
	GroupID     int64  `json:"group_id,omitempty"`
	PhoneNumber string `json:"phone_number"`
}

func (r *ArkSharePeer) Path() string { return "/ArkSharePeer" }

// SendGroupArkShare 分享群(Ark) POST /send_group_ark_share
type SendGroupArkShare struct {
	GroupID int64 `json:"group_id"`
}

func (r *SendGroupArkShare) Path() string { return "/send_group_ark_share" }

// SendArkShare 分享用户(Ark) POST /send_ark_share
type SendArkShare struct {
	UserID      int64  `json:"user_id,omitempty"`
	GroupID     int64  `json:"group_id,omitempty"`
	PhoneNumber string `json:"phone_number"`
}

func (r *SendArkShare) Path() string { return "/send_ark_share" }

// SetMsgEmojiLike 设置消息表情点赞 POST /set_msg_emoji_like
type SetMsgEmojiLike struct {
	MessageID int64  `json:"message_id"`
	EmojiID   string `json:"emoji_id"`
	Set       bool   `json:"set,omitempty"`
}

func (r *SetMsgEmojiLike) Path() string { return "/set_msg_emoji_like" }

// ClickInlineKeyboardButton 点击内联键盘按钮 POST /click_inline_keyboard_button
type ClickInlineKeyboardButton struct {
	GroupID      int64  `json:"group_id"`
	BotAppID     string `json:"bot_appid"`
	ButtonID     string `json:"button_id"`
	CallbackData string `json:"callback_data"`
	MsgSeq       string `json:"msg_seq"`
}

func (r *ClickInlineKeyboardButton) Path() string { return "/click_inline_keyboard_button" }
