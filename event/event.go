package event

import (
	"Q-Mi/event/file"
	"Q-Mi/event/message"
	"Q-Mi/event/user"
	"encoding/json"
)

var messageEventProcess = make(map[MessageType]EventProcess)
var noticeEventProcess = make(map[NoticeType]EventProcess)
var eventProcess = make(map[EventType]EventProcess)

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

func init() {
	eventProcess[EventTypeMessage] = &MessageEvent{}
	eventProcess[EventTypeMessageSent] = &MessageEvent{}
	eventProcess[EventTypeNotice] = &NoticeEvent{}
	eventProcess[EventTypeRequest] = &RequsetEvent{}
	eventProcess[EventTypeMeta] = &OB11BaseMetaEvent{}

	messageEventProcess[PrivateMessage] = &PrivateMessageEvent{}
	messageEventProcess[GroupMessage] = &GroupMessageEvent{}

	noticeEventProcess[GroupUpload] = &GroupUploadEvent{}
	noticeEventProcess[GroupAdmin] = &GroupAdminEvent{}
	noticeEventProcess[GroupDecrease] = &GroupDecreaseEvent{}
	noticeEventProcess[GroupIncrease] = &GroupIncreaseEvent{}
	noticeEventProcess[GroupBan] = &GroupBanEvent{}
	noticeEventProcess[FriendAdd] = &FriendAddEvent{}
	noticeEventProcess[GroupRecall] = &GroupRecallEvent{}
	noticeEventProcess[FriendRecall] = &FriendRecallEvent{}
	noticeEventProcess[GroupCard] = &GroupCardEvent{}
	noticeEventProcess[GroupMsgEmojiLike] = &GroupMsgEmojiLikeEvent{}
	noticeEventProcess[GroupEssence] = &GroupEssenceEvent{}
	noticeEventProcess[BotOffline] = &BotOfflineEvent{}
	noticeEventProcess[Notify] = &WhyNoticeEvent{}
}

type BaseEvent struct {
	RawInfo json.RawMessage `json:"Raw_info"`
}

type MainEvent struct {
	Time     float64   `json:"time"`
	PostType EventType `json:"post_type"`
	SelfID   int       `json:"self_id"`
}

type EventProcess interface {
	EventHandle(data BaseEvent)
}

func (e *MainEvent) EventHandle(data BaseEvent) {
	json.Unmarshal(data.RawInfo, &e)

	if handler, ok := eventProcess[e.PostType]; ok {
		handler.EventHandle(data)
	}
}

type MessageEvent struct {
	MainEvent
	MessageType MessageType `json:"message_type"`
}

func (e *MessageEvent) EventHandle(data BaseEvent) {
	if handler, ok := messageEventProcess[e.MessageType]; ok {
		handler.EventHandle(data)
	}
}

type PrivateMessageEvent struct {
	MessageEvent
	SubType    string             `json:"sub_type"`
	MessageID  int                `json:"message_id"`
	UserID     int                `json:"user_id"`
	Message    *message.Message   `json:"message"`
	RawMessage string             `json:"raw_message"`
	Font       int                `json:"font"`
	Sender     *user.FriendSender `json:"sender"`
	SelfID     int                `json:"self_id"`
}

func (e *PrivateMessageEvent) EventHandle(data BaseEvent) {

}

type GroupMessageEvent struct {
	MessageEvent
	SubType    string            `json:"sub_type"`
	MessageID  int               `json:"message_id"`
	GroupID    int               `json:"group_id"`
	UserID     int               `json:"user_id"`
	Message    *message.Message  `json:"message"`
	RawMessage string            `json:"raw_message"`
	Font       int               `json:"font"`
	Sender     *user.GroupSender `json:"sender"`
	SelfID     int               `json:"self_id"`
}

func (e *GroupMessageEvent) EventHandle(data BaseEvent) {

}

type NoticeEvent struct {
	MainEvent
	NoticeType string `json:"notice_type"`
}

func (e *NoticeEvent) EventHandle(data BaseEvent) {
	if handler, ok := noticeEventProcess[NoticeType(e.NoticeType)]; ok {
		handler.EventHandle(data)
	}
}

type WhyNoticeEvent struct {
	NoticeEvent
	SubType string `json:"sub_type"`
}

func (e *WhyNoticeEvent) EventHandle(data BaseEvent) {
	if handler, ok := noticeEventProcess[NoticeType(e.SubType)]; ok {
		handler.EventHandle(data)
	}
}

type GroupUploadEvent struct {
	NoticeEvent
	GroupID int       `json:"group_id"`
	UserID  int       `json:"user_id"`
	File    file.File `json:"file"`
}

func (e *GroupUploadEvent) EventHandle(data BaseEvent) {

}

type GroupAdminEvent struct {
	NoticeEvent
	SubType string `json:"sub_type"`
	GroupID int    `json:"group_id"`
	UserID  int    `json:"user_id"`
}

func (e *GroupAdminEvent) EventHandle(data BaseEvent) {

}

type GroupDecreaseEvent struct {
	NoticeEvent
	SubType    string `json:"sub_type"`
	GroupID    int    `json:"group_id"`
	OperatorID int    `json:"operator_id"`
	UserID     int    `json:"user_id"`
}

func (e *GroupDecreaseEvent) EventHandle(data BaseEvent) {

}

type GroupIncreaseEvent struct {
	NoticeEvent
	SubType    string `json:"sub_type"`
	GroupID    int    `json:"group_id"`
	OperatorID int    `json:"operator_id"`
	UserID     int    `json:"user_id"`
}

func (e *GroupIncreaseEvent) EventHandle(data BaseEvent) {

}

type GroupBanEvent struct {
	NoticeEvent
	SubType    string `json:"sub_type"`
	GroupID    int    `json:"group_id"`
	OperatorID int    `json:"operator_id"`
	UserID     int    `json:"user_id"`
	Duration   int    `json:"duration"`
}

func (e *GroupBanEvent) EventHandle(data BaseEvent) {

}

type FriendAddEvent struct {
	NoticeEvent
	UserID int `json:"user_id"`
}

func (e *FriendAddEvent) EventHandle(data BaseEvent) {

}

type GroupRecallEvent struct {
	NoticeEvent
	GroupID    int `json:"group_id"`
	UserID     int `json:"user_id"`
	OperatorID int `json:"operator_id"`
	MessageID  int `json:"message_id"`
}

func (e *GroupRecallEvent) EventHandle(data BaseEvent) {

}

type FriendRecallEvent struct {
	NoticeEvent
	UserID    int `json:"user_id"`
	MessageID int `json:"message_id"`
}

func (e *FriendRecallEvent) EventHandle(data BaseEvent) {

}

type GroupCardEvent struct {
	NoticeEvent
	GroupID int    `json:"group_id"`
	UserID  int    `json:"user_id"`
	CardNew string `json:"card_new"`
	CardOld string `json:"card_old"`
}

func (e *GroupCardEvent) EventHandle(data BaseEvent) {

}

type GroupNameEvent struct {
	WhyNoticeEvent
	GroupID int    `json:"group_id"`
	UserID  int    `json:"user_id"`
	NameNew string `json:"name_new"`
}

func (e *GroupNameEvent) EventHandle(data BaseEvent) {

}

type GroupTitleEvent struct {
	WhyNoticeEvent
	GroupID int    `json:"group_id"`
	UserID  int    `json:"user_id"`
	Title   string `json:"title"`
}

func (e *GroupTitleEvent) EventHandle(data BaseEvent) {

}

type GroupEssenceEvent struct {
	NoticeEvent
	MessageID  int    `json:"message_id"`
	GroupID    int    `json:"group_id"`
	UserID     int    `json:"user_id"`
	OperatorID int    `json:"operator_id"`
	SubType    string `json:"sub_type"`
	SenderID   int    `json:"sender_id"`
}

func (e *GroupEssenceEvent) EventHandle(data BaseEvent) {

}

type GroupGrayTipEvent struct {
	WhyNoticeEvent
	GroupID   int    `json:"group_id"`
	UserID    int    `json:"user_id"`
	MessageID int    `json:"message_id"`
	BusiID    string `json:"busi_id"`
	Content   string `json:"content"`
	RawInfo   any    `json:"raw_info"`
}

func (e *GroupGrayTipEvent) EventHandle(data BaseEvent) {

}

type GroupMsgEmojiLikeEvent struct {
	NoticeEvent
	GroupID   int                    `json:"group_id"`
	UserID    int                    `json:"user_id"`
	MessageID int                    `json:"message_id"`
	Likes     []message.MsgEmojiLike `json:"likes"`
}

func (e *GroupMsgEmojiLikeEvent) EventHandle(data BaseEvent) {

}

type PokeEvent struct {
	WhyNoticeEvent
	TargetID int `json:"target_id"`
	UserID   int `json:"user_id"`
}

func (e *PokeEvent) EventHandle(data BaseEvent) {

}

type FriendPokeEvent struct {
	PokeEvent
	SenderID int `json:"sender_id"`
	RawInfo  any `json:"raw_info"`
}

func (e *FriendPokeEvent) EventHandle(data BaseEvent) {

}

type GroupPokeEvent struct {
	PokeEvent
	GroupID int `json:"group_id"`
	RawInfo any `json:"raw_info"`
}

func (e *GroupPokeEvent) EventHandle(data BaseEvent) {

}

type ProfileLikeEvent struct {
	WhyNoticeEvent
	OperatorID   int    `json:"operator_id"`
	OperatorNick string `json:"operator_nick"`
	Times        int    `json:"times"`
	Time         int    `json:"time"`
}

func (e *ProfileLikeEvent) EventHandle(data BaseEvent) {

}

type InputStatusEvent struct {
	WhyNoticeEvent
	UserID    int    `json:"user_id"`
	EventType string `json:"event_type"`
	Status    string `json:"status"`
}

func (e *InputStatusEvent) EventHandle(data BaseEvent) {

}

type BotOfflineEvent struct {
	NoticeEvent
	UserID  int    `json:"user_id"`
	Tag     string `json:"tag"`
	Message string `json:"message"`
}

func (e *BotOfflineEvent) EventHandle(data BaseEvent) {

}

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

type HeartbeatStatus struct {
	Online bool `json:"online"`
	Good   bool `json:"good"`
}

type OB11BaseMetaEvent struct {
	MainEvent
	MetaEventType string `json:"meta_event_type"`
}

type OB11HeartbeatEvent struct {
	OB11BaseMetaEvent
	Status   HeartbeatStatus `json:"status"`
	Interval int             `json:"interval"`
}

func (e *OB11HeartbeatEvent) EventHandle(data BaseEvent) {

}

type OB11LifeCycleEvent struct {
	OB11BaseMetaEvent
	SubType LifeCycleSubType `json:"sub_type"`
}

func (e *OB11LifeCycleEvent) EventHandle(data BaseEvent) {

}

type RequsetEvent struct {
	MainEvent
	RequestType string `json:"request_type"`
	UserID      int    `json:"user_id"`
	Flag        string `json:"flag"`
}

func (r *RequsetEvent) EventHandle(data BaseEvent) {

}

type OB11FriendRequestEvent struct {
	RequsetEvent
	Comment string `json:"comment"`
}

func (f *OB11FriendRequestEvent) EventHandle(data BaseEvent) {

}

type OB11GroupRequestEvent struct {
	RequsetEvent
	GroupID int    `json:"group_id"`
	Comment string `json:"comment"`
	SubType string `json:"sub_type"`
}

func (g *OB11GroupRequestEvent) EventHandle(data BaseEvent) {

}
