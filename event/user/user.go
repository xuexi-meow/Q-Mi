package user


type FriendSender struct {
	User_id int `json:"user_id"`
	Nickname string `json:"nickname"`
	Sex string `json:"sex"`
}

type GroupSender struct {
	User_id int `json:"user_id"`
	Nickname string `json:"nickname"`
	Sex string `json:"sex"`
	Card string `json:"card"`
	Role string `json:"role"`
}