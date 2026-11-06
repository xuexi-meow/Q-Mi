package api

import (
	apimsg "Q-Mi/api/message"
	eventuser "Q-Mi/event/user"
)

// 本文件定义发送给 NapCat 的 API 请求与响应结构。
// 请求结构体实现 Request 接口的 Path(),即可自动对应到 NapCat 的 API 地址。
// 消息段用 Q-Mi/api/message 里的构造函数生成(apimsg.Text / apimsg.At 等)。

// Request 所有 NapCat API 请求的统一接口
type Request interface {
	Path() string
}

// Response NapCat 统一响应信封(retcode 为 0 表示成功)。
// Data 的类型随 API 不同而不同,用泛型参数指定。
type Response[T any] struct {
	Status  string `json:"status"`
	Retcode int    `json:"retcode"`
	Data    T      `json:"data"`
	Message string `json:"message"`
	Wording string `json:"wording"`
	Stream  string `json:"stream"`
}

// EmptyData 部分 API 的 data 为空对象(如撤回消息)
type EmptyData struct{}

// ---------------- 消息接口 ----------------

// SendGroupMsg 发送群消息 POST /send_group_msg
type SendGroupMsg struct {
	GroupID    int64             `json:"group_id"`
	Message    []*apimsg.Message `json:"message"`
	AutoEscape bool              `json:"auto_escape,omitempty"`
}

func (r *SendGroupMsg) Path() string { return "/send_group_msg" }

// SendPrivateMsg 发送私聊消息 POST /send_private_msg
type SendPrivateMsg struct {
	UserID     int64             `json:"user_id"`
	Message    []*apimsg.Message `json:"message"`
	AutoEscape bool              `json:"auto_escape,omitempty"`
}

func (r *SendPrivateMsg) Path() string { return "/send_private_msg" }

// SendMsgData 发送消息接口返回的业务数据
type SendMsgData struct {
	MessageID int64   `json:"message_id"`
	ResID     *string `json:"res_id,omitempty"`
	ForwardID *string `json:"forward_id,omitempty"`
}

// GetMsg 获取消息 POST /get_msg
type GetMsg struct {
	MessageID int64 `json:"message_id"`
}

func (r *GetMsg) Path() string { return "/get_msg" }

// GetMsgData 获取消息接口返回的业务数据
type GetMsgData struct {
	Time        float64               `json:"time"`
	MessageType string                `json:"message_type"`
	MessageID   int64                 `json:"message_id"`
	RealID      int                   `json:"real_id"`
	MessageSeq  int                   `json:"message_seq"`
	Sender      eventuser.GroupSender `json:"sender"`
	Message     any                   `json:"message"`
	RawMessage  string                `json:"raw_message"`
	Font        int                   `json:"font"`
	GroupID     *int64                `json:"group_id,omitempty"`
	UserID      int64                 `json:"user_id"`
}

// DeleteMsg 撤回消息 POST /delete_msg
type DeleteMsg struct {
	MessageID int64 `json:"message_id"`
}

func (r *DeleteMsg) Path() string { return "/delete_msg" }
