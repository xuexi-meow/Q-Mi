package message

type MessageType string

const (
	TypeText    MessageType = "text"
	TypeAt      MessageType = "at"
	TypeReply   MessageType = "reply"
	TypeFace    MessageType = "face"
	TypeMFace   MessageType = "mface"
	TypeDice    MessageType = "dice"
	TypeRPS     MessageType = "rps"
	TypePoke    MessageType = "poke"
	TypeImage   MessageType = "image"
	TypeRecord  MessageType = "record"
	TypeVideo   MessageType = "video"
	TypeFile    MessageType = "file"
	TypeJson    MessageType = "json"
	TypeMusic   MessageType = "music"
	TypeForward MessageType = "forward"
)

func (mt MessageType) String() string {
	return string(mt)
}

func Text(text string) *Message {
	msg := &Message{
		Type: TypeText,
		Data: MakeText{
			Text: text,
		},
	}
	return msg
}

func At(qq string) *Message {
	msg := &Message{
		Type: TypeAt,
		Data: MakeAt{
			QQ: qq,
		},
	}
	return msg
}

func Reply(id string) *Message {
	msg := &Message{
		Type: TypeReply,
		Data: MakeReply{
			ID: id,
		},
	}
	return msg
}

func Face(id string) *Message {
	msg := &Message{
		Type: TypeFace,
		Data: MakeFace{
			ID: id,
		},
	}
	return msg
}

func MFace(emojiID, emojiPackageID string, key *string, summary *string) *Message {
	msg := &Message{
		Type: TypeMFace,
		Data: MakeMFace{
			EmojiID:        emojiID,
			EmojiPackageID: emojiPackageID,
			Key:            key,
			Summary:        summary,
		},
	}
	return msg
}

func Dice(result string) *Message {
	msg := &Message{
		Type: TypeDice,
		Data: MakeDice{Result: result},
	}
	return msg
}

func RPS(result string) *Message {
	msg := &Message{
		Type: TypeRPS,
		Data: MakeRPS{Result: result},
	}
	return msg
}

func Poke(typ string, id string) *Message {
	msg := &Message{
		Type: TypePoke,
		Data: MakePoke{Type: typ, ID: id},
	}
	return msg
}

func Image(file string, summary *string, subType *string) *Message {
	msg := &Message{
		Type: TypeImage,
		Data: MakeImage{File: file, Summary: summary, SubType: subType},
	}
	return msg
}

func Record(file string) *Message {
	msg := &Message{
		Type: TypeRecord,
		Data: MakeRecord{File: file},
	}
	return msg
}

func Video(file string, thumb *string) *Message {
	msg := &Message{
		Type: TypeVideo,
		Data: MakeVideo{File: file, Thumb: thumb},
	}
	return msg
}

func FileMsg(file string, name *string) *Message {
	msg := &Message{
		Type: TypeFile,
		Data: MakeFile{File: file, Name: name},
	}
	return msg
}

func Json(data any) *Message {
	msg := &Message{
		Type: TypeJson,
		Data: MakeJson{Data: data},
	}
	return msg
}

func Music(typ string, id *string, url *string, image *string, singer *string, title *string, content *string) *Message {
	msg := &Message{
		Type: TypeMusic,
		Data: MakeMusic{Type: typ, ID: id, Url: url, Image: image, Singer: singer, Title: title, Content: content},
	}
	return msg
}

func Forward(id string, content *[]Message) *Message {
	msg := &Message{
		Type: TypeForward,
		Data: MakeForward{ID: id, Content: content},
	}
	return msg
}

func StringPtr(v string) *string {
	return &v
}

func IntPtr(v int) *int {
	return &v
}

func AnyPtr(v any) *any {
	return &v
}

type Message struct {
	Type MessageType `json:"type"`
	Data any         `json:"data"`
}

type MakeText struct {
	Text string `json:"text"`
}

type MakeAt struct {
	QQ string `json:"qq"`
}

type MakeReply struct {
	ID string `json:"id"`
}

type MakeFace struct {
	ID string `json:"id"`
}

type MakeMFace struct {
	EmojiID        string  `json:"emoji_id"`
	EmojiPackageID string  `json:"emoji_package_id"`
	Key            *string `json:"key,omitempty"`
	Summary        *string `json:"summary,omitempty"`
}

type MakeDice struct {
	Result string `json:"result"`
}

type MakeRPS struct {
	Result string `json:"result"`
}

type MakePoke struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

type MakeImage struct {
	File    string  `json:"file"` //可填url/Base64编码
	Summary *string `json:"summary,omitempty"`
	SubType *string `json:"sub_type,omitempty"`
}

type MakeRecord struct {
	File string `json:"file"` //可填url/Base64编码
}

type MakeVideo struct {
	File  string  `json:"file"` //可填url/Base64编码
	Thumb *string `json:"thumb,omitempty"`
}

type MakeFile struct {
	File string  `json:"file"` //可填url/Base64编码
	Name *string `json:"name,omitempty"`
}

type MakeJson struct {
	Data any `json:"data"`
}

type MakeMusic struct {
	Type    string  `json:"type"`            // 可选值: "qq", "163", "kugou", "kuwo", "migu", "custom"
	ID      *string `json:"id,omitempty"`    //非自定义类型时必填
	Url     *string `json:"url,omitempty"`   //自定义类型时必填
	Image   *string `json:"image,omitempty"` //自定义类型时必填
	Singer  *string `json:"singer,omitempty"`
	Title   *string `json:"title,omitempty"`
	Content *string `json:"content,omitempty"`
}

type MakeForward struct {
	ID      string     `json:"id"`
	Content *[]Message `json:"content,omitempty"`
}
