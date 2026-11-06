package api

// 核心接口(来源: https://napcat.apifox.cn/llms.txt 各动作详情页)

// SetGroupTodo 设置群待办 POST /set_group_todo
type SetGroupTodo struct {
	GroupID    int64 `json:"group_id"`
	MessageID  int64 `json:"message_id,omitempty"`
	MessageSeq int64 `json:"message_seq,omitempty"`
}

func (r *SetGroupTodo) Path() string { return "/set_group_todo" }

// CompleteGroupTodo 完成群待办 POST /complete_group_todo
type CompleteGroupTodo struct {
	GroupID    int64 `json:"group_id"`
	MessageID  int64 `json:"message_id,omitempty"`
	MessageSeq int64 `json:"message_seq,omitempty"`
}

func (r *CompleteGroupTodo) Path() string { return "/complete_group_todo" }

// CancelGroupTodo 取消群待办 POST /cancel_group_todo
type CancelGroupTodo struct {
	GroupID    int64 `json:"group_id"`
	MessageID  int64 `json:"message_id,omitempty"`
	MessageSeq int64 `json:"message_seq,omitempty"`
}

func (r *CancelGroupTodo) Path() string { return "/cancel_group_todo" }

// SendGroupPoke 群聊戳一戳 POST /group_poke
type SendGroupPoke struct {
	GroupID  *int64 `json:"group_id,omitempty"`
	UserID   int64  `json:"user_id"`
	TargetID int64  `json:"target_id,omitempty"`
}

func (r *SendGroupPoke) Path() string { return "/group_poke" }

// SendFriendPoke 私聊/好友戳一戳 POST /friend_poke
type SendFriendPoke struct {
	GroupID  *int64 `json:"group_id,omitempty"`
	UserID   int64  `json:"user_id"`
	TargetID int64  `json:"target_id,omitempty"`
}

func (r *SendFriendPoke) Path() string { return "/friend_poke" }

// SendPoke 发送戳一戳(通用) POST /send_poke
type SendPoke struct {
	GroupID  *int64 `json:"group_id,omitempty"`
	UserID   int64  `json:"user_id"`
	TargetID int64  `json:"target_id,omitempty"`
}

func (r *SendPoke) Path() string { return "/send_poke" }
