package event

import (
	"Q-Mi/event/file"
	"Q-Mi/event/message"
	"Q-Mi/event/user"
)

// ============================================================
// 本文件只放事件类型常量与结构体定义(纯数据)。
// 事件处理逻辑统一放在 Q-Mi/handler 包,通过 Register / RegisterTyped 注册。
// 新增事件:在这里定义结构体,再到 handler 包加一行注册。
// ============================================================

type MessageType string

const (
	PrivateMessage MessageType = "private"
	GroupMessage   MessageType = "group"
)

type NoticeType string

const (
	GroupUpload       NoticeType = "group_upload"
	GroupAdmin        NoticeType = "group_admin"
	GroupDecrease     NoticeType = "group_decrease"
	GroupIncrease     NoticeType = "group_increase"
	GroupBan          NoticeType = "group_ban"
	FriendAdd         NoticeType = "friend_add"
	GroupRecall       NoticeType = "group_recall"
	FriendRecall      NoticeType = "friend_recall"
	Notify            NoticeType = "notify"
	LuckyKing         NoticeType = "lucky_king"
	Poke              NoticeType = "poke"
	GroupCard         NoticeType = "group_card"
	GroupName         NoticeType = "group_name"
	GroupTitle        NoticeType = "group_title"
	GroupMsgEmojiLike NoticeType = "group_msg_emoji_like"
	GroupGrayTip      NoticeType = "group_gray_tip"
	GroupEssence      NoticeType = "group_essence"
	ProfileLike       NoticeType = "profile_like"
	InputStatus       NoticeType = "input_status"
	BotOffline        NoticeType = "bot_offline"
)

type EventType string

const (
	EventTypeMeta        EventType = "meta_event"
	EventTypeRequest     EventType = "request"
	EventTypeNotice      EventType = "notice"
	EventTypeMessage     EventType = "message"
	EventTypeMessageSent EventType = "message_sent"
)

type LifeCycleSubType string

const (
	LifeCycleSubTypeEnable  LifeCycleSubType = "enable"
	LifeCycleSubTypeDisable LifeCycleSubType = "disable"
	LifeCycleSubTypeConnect LifeCycleSubType = "connect"
)

// ============================================================
// 事件结构体定义(纯数据,json 反序列化用)
// ============================================================

// MainEvent 事件公共字段,嵌入到所有具体事件中
type MainEvent struct {
	Time     float64   `json:"time"`
	PostType EventType `json:"post_type"`
	SelfID   int       `json:"self_id"`
}

// MessageEvent 消息事件公共字段
type MessageEvent struct {
	MainEvent
	MessageType MessageType `json:"message_type"`
}

// 私聊消息
type PrivateMessageEvent struct {
	MessageEvent
	SubType    string             `json:"sub_type"`
	MessageID  int                `json:"message_id"`
	UserID     int                `json:"user_id"`
	Message    []*message.Message `json:"message"`
	RawMessage string             `json:"raw_message"`
	Font       int                `json:"font"`
	Sender     *user.FriendSender `json:"sender"`
	SelfID     int                `json:"self_id"`
}

// 群聊消息
type GroupMessageEvent struct {
	MessageEvent
	SubType    string             `json:"sub_type"`
	MessageID  int                `json:"message_id"`
	GroupID    int                `json:"group_id"`
	UserID     int                `json:"user_id"`
	Message    []*message.Message `json:"message"`
	RawMessage string             `json:"raw_message"`
	Font       int                `json:"font"`
	Sender     *user.GroupSender  `json:"sender"`
	SelfID     int                `json:"self_id"`
}

// MessageSentEvent 机器人自己发出的消息(message_sent)。
// 注意:发出消息的 user_id 是机器人自身,target_id 是对方(好友 QQ 或群号)。
// 字段以 NapCat 实际上报为准,接入后先打一条日志核对。
type MessageSentEvent struct {
	MessageEvent
	SubType    string             `json:"sub_type"`
	MessageID  int                `json:"message_id"`
	UserID     int                `json:"user_id"`
	GroupID    int                `json:"group_id"`
	TargetID   int                `json:"target_id"`
	Message    []*message.Message `json:"message"`
	RawMessage string             `json:"raw_message"`
}

func (e *PrivateMessageEvent) Messages() []*message.Message {
	return e.Message
}

func (e *GroupMessageEvent) Messages() []*message.Message {
	return e.Message
}

// NoticeEvent 通知事件公共字段
type NoticeEvent struct {
	MainEvent
	NoticeType string `json:"notice_type"`
}

// WhyNoticeEvent notify 类通知公共字段，notice_type=notify，靠 sub_type 区分。
// 未知含义，不知道为啥要单独分出来，严重怀疑是QQ的石山代码遗留，所以我给它加了个Why以表示我的疑惑
type WhyNoticeEvent struct {
	NoticeEvent
	SubType string `json:"sub_type"`
}

// 群文件上传
type GroupUploadEvent struct {
	NoticeEvent
	GroupID int       `json:"group_id"`
	UserID  int       `json:"user_id"`
	File    file.File `json:"file"`
}

// 群管理员变动
type GroupAdminEvent struct {
	NoticeEvent
	SubType string `json:"sub_type"`
	GroupID int    `json:"group_id"`
	UserID  int    `json:"user_id"`
}

// 群成员减少
type GroupDecreaseEvent struct {
	NoticeEvent
	SubType    string `json:"sub_type"`
	GroupID    int    `json:"group_id"`
	OperatorID int    `json:"operator_id"`
	UserID     int    `json:"user_id"`
}

// 群成员增加
type GroupIncreaseEvent struct {
	NoticeEvent
	SubType    string `json:"sub_type"`
	GroupID    int    `json:"group_id"`
	OperatorID int    `json:"operator_id"`
	UserID     int    `json:"user_id"`
}

// 群禁言
type GroupBanEvent struct {
	NoticeEvent
	SubType    string `json:"sub_type"`
	GroupID    int    `json:"group_id"`
	OperatorID int    `json:"operator_id"`
	UserID     int    `json:"user_id"`
	Duration   int    `json:"duration"`
}

// 好友添加
type FriendAddEvent struct {
	NoticeEvent
	UserID int `json:"user_id"`
}

// 群消息撤回
type GroupRecallEvent struct {
	NoticeEvent
	GroupID    int `json:"group_id"`
	UserID     int `json:"user_id"`
	OperatorID int `json:"operator_id"`
	MessageID  int `json:"message_id"`
}

// 好友消息撤回
type FriendRecallEvent struct {
	NoticeEvent
	UserID    int `json:"user_id"`
	MessageID int `json:"message_id"`
}

// 群名片变更
type GroupCardEvent struct {
	NoticeEvent
	GroupID int    `json:"group_id"`
	UserID  int    `json:"user_id"`
	CardNew string `json:"card_new"`
	CardOld string `json:"card_old"`
}

// 群名变更(notify)
type GroupNameEvent struct {
	WhyNoticeEvent
	GroupID int    `json:"group_id"`
	UserID  int    `json:"user_id"`
	NameNew string `json:"name_new"`
}

// 群头衔变更(notify)
type GroupTitleEvent struct {
	WhyNoticeEvent
	GroupID int    `json:"group_id"`
	UserID  int    `json:"user_id"`
	Title   string `json:"title"`
}

// 群精华消息
type GroupEssenceEvent struct {
	NoticeEvent
	MessageID  int    `json:"message_id"`
	GroupID    int    `json:"group_id"`
	UserID     int    `json:"user_id"`
	OperatorID int    `json:"operator_id"`
	SubType    string `json:"sub_type"`
	SenderID   int    `json:"sender_id"`
}

// 群灰条消息
type GroupGrayTipEvent struct {
	WhyNoticeEvent
	GroupID   int    `json:"group_id"`
	UserID    int    `json:"user_id"`
	MessageID int    `json:"message_id"`
	BusiID    string `json:"busi_id"`
	Content   string `json:"content"`
	RawInfo   any    `json:"raw_info"`
}

// 表情回应通知
type GroupMsgEmojiLikeEvent struct {
	NoticeEvent
	GroupID   int                     `json:"group_id"`
	UserID    int                     `json:"user_id"`
	MessageID int                     `json:"message_id"`
	Likes     []*message.MsgEmojiLike `json:"likes"`
}

// 戳一戳(notify),具体是群戳还是好友戳看原始数据里有没有 group_id / sender_id
type PokeEvent struct {
	WhyNoticeEvent
	TargetID int `json:"target_id"`
	UserID   int `json:"user_id"`
	RawInfo  any `json:"raw_info"`
}

// 好友戳一戳
type FriendPokeEvent struct {
	PokeEvent
	SenderID int `json:"sender_id"`
	RawInfo  any `json:"raw_info"`
}

// 群戳一戳
type GroupPokeEvent struct {
	PokeEvent
	GroupID int `json:"group_id"`
	RawInfo any `json:"raw_info"`
}

// 个人资料点赞(notify)
type ProfileLikeEvent struct {
	WhyNoticeEvent
	OperatorID   int    `json:"operator_id"`
	OperatorNick string `json:"operator_nick"`
	Times        int    `json:"times"`
	Time         int    `json:"time"`
}

// 输入状态(notify)
type InputStatusEvent struct {
	WhyNoticeEvent
	UserID     int    `json:"user_id"`
	EventType  int    `json:"event_type"`
	StatusText string `json:"status_text"`
}

// 机器人离线
type BotOfflineEvent struct {
	NoticeEvent
	UserID  int    `json:"user_id"`
	Tag     string `json:"tag"`
	Message string `json:"message"`
}

// 元事件公共字段
type OB11BaseMetaEvent struct {
	MainEvent
	MetaEventType string `json:"meta_event_type"`
}

// 心跳
type OB11HeartbeatEvent struct {
	OB11BaseMetaEvent
	Status   HeartbeatStatus `json:"status"`
	Interval int             `json:"interval"`
}

type HeartbeatStatus struct {
	Online bool `json:"online"`
	Good   bool `json:"good"`
}

// 生命周期
type OB11LifeCycleEvent struct {
	OB11BaseMetaEvent
	SubType LifeCycleSubType `json:"sub_type"`
}

// 请求事件公共字段
type RequsetEvent struct {
	MainEvent
	RequestType string `json:"request_type"`
	UserID      int    `json:"user_id"`
	Flag        string `json:"flag"`
}

// 好友请求
type OB11FriendRequestEvent struct {
	RequsetEvent
	Comment string `json:"comment"`
}

// 群请求
type OB11GroupRequestEvent struct {
	RequsetEvent
	GroupID int    `json:"group_id"`
	Comment string `json:"comment"`
	SubType string `json:"sub_type"`
}
