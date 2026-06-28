package event

import (
	"Q-Mi/event/message"
	"Q-Mi/event/user"
)

type MainEvent struct{
	Time float64 `json:"time"`
	Post_type string `json:"post_type"`
	Self_id int `json:"self_id"`
	Message_type string `json:"message_type"`
}

func (e *MainEvent)EventHandle() {

}

type PrivateMessageEvent struct{
	MainEvent
	Sub_type string `json:"sub_type"`
	Message_id int `json:"message_id"`
	User_id int `json:"user_id"`
	Message *message.Message `json:"message"`
	Raw_message string `json:"raw_message"`
	Font int `json:"font"`
	Sender *user.FriendSender `json:"sender"`
	Self_id int `json:"self_id"`
}

type GroupMessageEvent struct{
	MainEvent
	Sub_type string `json:"sub_type"`
	Message_id int `json:"message_id"`
	Group_id int `json:"group_id"`
	User_id int `json:"user_id"`
	Message *message.Message `json:"message"`
	Raw_message string `json:"raw_message"`
	Font int `json:"font"`
	Sender *user.GroupSender `json:"sender"`
	Self_id int `json:"self_id"`
}