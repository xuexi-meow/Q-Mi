package message

type Message struct {
	Type string `json:"type"`
	Data any `json:"data"`
}

type MsgEmojiLike struct{
	Emoji_id string `json:"emoji_id"`
	Count int `json:"count"`
}
