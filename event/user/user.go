package user

type FriendSender struct {
	UserId   int    `json:"user_id"`
	Nickname string `json:"nickname"`
	Sex      string `json:"sex"`
}

type GroupSender struct {
	UserId   int    `json:"user_id"`
	Nickname string `json:"nickname"`
	Sex      string `json:"sex"`
	Card     string `json:"card"`
	Role     string `json:"role"`
}
