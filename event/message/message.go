package message

import (
	"encoding/json"
	"fmt"
	"strconv"

	"Q-Mi/api/message"
)

var messageTypeProcessors = make(map[message.MessageType]MessageTypeProcessor)

func init() {
	messageTypeProcessors[message.TypeText] = &Text{}
	messageTypeProcessors[message.TypeAt] = &At{}
	messageTypeProcessors[message.TypeReply] = &Reply{}
	messageTypeProcessors[message.TypeFace] = &Face{}
	messageTypeProcessors[message.TypeMFace] = &MFace{}
	messageTypeProcessors[message.TypeDice] = &Dice{}
	messageTypeProcessors[message.TypeRPS] = &RPS{}
	messageTypeProcessors[message.TypePoke] = &Poke{}
	messageTypeProcessors[message.TypeImage] = &Image{}
	messageTypeProcessors[message.TypeRecord] = &Record{}
	messageTypeProcessors[message.TypeVideo] = &Video{}
	messageTypeProcessors[message.TypeFile] = &File{}
	messageTypeProcessors[message.TypeJson] = &Json{}
	messageTypeProcessors[message.TypeMusic] = &Music{}
	messageTypeProcessors[message.TypeForward] = &Forward{}
}

type MessageTypeProcessor interface {
	TypeMake(data json.RawMessage) (any, error)
}

type Message struct {
	Type   string          `json:"type"`
	Parsed json.RawMessage `json:"data"`
	Data   any             `json:"-"`
}

func (m *Message) TypeHandler() (any, error) {
	processor, ok := messageTypeProcessors[message.MessageType(m.Type)]
	if !ok {
		return nil, nil
	}

	parsed, err := processor.TypeMake(m.Parsed)
	if err != nil {
		return nil, err
	}

	m.Data = parsed
	return parsed, nil
}

type MsgEmojiLike struct {
	Emoji_id string `json:"emoji_id"`
	Count    int    `json:"count"`
}

type Text struct {
	Text string `json:"text"`
}

func (t *Text) TypeMake(data json.RawMessage) (any, error) {
	var v Text
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v, nil
}

func (t Text) Stringf() string {
	return t.Text
}

type At struct {
	QQ string `json:"qq"`
}

func (a *At) TypeMake(data json.RawMessage) (any, error) {
	var v struct {
		QQ FlexString `json:"qq"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return At{QQ: string(v.QQ)}, nil
}

func (a At) Stringf() string {
	return fmt.Sprintf("%s", "@"+a.QQ)
}

type Reply struct {
	ID string `json:"id"`
}

func (r *Reply) TypeMake(data json.RawMessage) (any, error) {
	var v struct {
		ID FlexString `json:"id"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return Reply{ID: string(v.ID)}, nil
}

func (r Reply) Stringf() string {
	return fmt.Sprintf("回复消息[%s]", r.ID)
}

type Face struct {
	ID         string  `json:"id"`
	Raw        *any    `json:"raw,omitempty"`
	Result     *string `json:"result,omitempty"`
	ChainCount *int    `json:"chain_count,omitempty"`
}

func (f *Face) TypeMake(data json.RawMessage) (any, error) {
	var v Face
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v, nil
}

func (f Face) Stringf() string {
	return fmt.Sprintf("表情[%v]", f.ID)
}

type MFace struct {
	EmojiID        FlexString `json:"emoji_id"`
	EmojiPackageID FlexString `json:"emoji_package_id"`
	Key            *string    `json:"key,omitempty"`
}

func (m *MFace) TypeMake(data json.RawMessage) (any, error) {
	var v MFace
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v, nil
}

type Dice struct {
	Result string `json:"result"`
}

func (d *Dice) TypeMake(data json.RawMessage) (any, error) {
	var v Dice
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v, nil
}

type RPS struct {
	Result string `json:"result"`
}

func (r *RPS) TypeMake(data json.RawMessage) (any, error) {
	var v RPS
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v, nil
}

type Poke struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

func (p *Poke) TypeMake(data json.RawMessage) (any, error) {
	var v Poke
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v, nil
}

// FlexString 兼容 NapCat 上报中同一字段可能是字符串或数字的情况
// (实测 image.sub_type 为数字、file_size 为字符串,且版本间可能互换)。
type FlexString string

func (s *FlexString) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var v string
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		*s = FlexString(v)
		return nil
	}
	*s = FlexString(data)
	return nil
}

// FlexInt 同理兼容数字与字符串形态的数值字段。
type FlexInt int

func (i *FlexInt) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		var v string
		if err := json.Unmarshal(data, &v); err != nil {
			return err
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return err
		}
		*i = FlexInt(n)
		return nil
	}
	return json.Unmarshal(data, (*int)(i))
}

type Image struct {
	File           string      `json:"file"`
	Url            *string     `json:"url,omitempty"`
	Summary        *string     `json:"summary,omitempty"`
	SubType        *FlexString `json:"sub_type,omitempty"`
	FileSize       *FlexInt    `json:"file_size,omitempty"`
	Key            *string     `json:"key,omitempty"`
	EmojiID        *FlexString `json:"emoji_id,omitempty"`
	EmojiPackageID *FlexString `json:"emoji_package_id,omitempty"`
}

func (img *Image) TypeMake(data json.RawMessage) (any, error) {
	var v Image
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v, nil
}

type Record struct {
	File     string  `json:"file"`
	FileSize *int    `json:"file_size,omitempty"`
	Path     *string `json:"path,omitempty"`
}

func (r *Record) TypeMake(data json.RawMessage) (any, error) {
	var v Record
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, err
	}
	return v, nil
}

type Video struct {
	File     string  `json:"file"`
	Url      *string `json:"url,omitempty"`
	FileSize *int    `json:"file_size,omitempty"`
	Thumb    *string `json:"thumb,omitempty"`
}

func (v *Video) TypeMake(data json.RawMessage) (any, error) {
	var x Video
	if err := json.Unmarshal(data, &x); err != nil {
		return nil, err
	}
	return x, nil
}

type File struct {
	File     string  `json:"file"`
	FileId   *string `json:"file_id,omitempty"`
	FileSize *int    `json:"file_size,omitempty"`
}

func (f *File) TypeMake(data json.RawMessage) (any, error) {
	var x File
	if err := json.Unmarshal(data, &x); err != nil {
		return nil, err
	}
	return x, nil
}

type Json struct {
	Data any `json:"data"`
}

func (j *Json) TypeMake(data json.RawMessage) (any, error) {
	var x Json
	if err := json.Unmarshal(data, &x); err != nil {
		return nil, err
	}
	return x, nil
}

type Music struct {
	Type    string  `json:"type"`            // 可选值: "qq", "163", "kugou", "kuwo", "migu", "custom"
	ID      *string `json:"id,omitempty"`    //非自定义类型时必填
	Url     *string `json:"url,omitempty"`   //自定义类型时必填
	Image   *string `json:"image,omitempty"` //自定义类型时必填
	Singer  *string `json:"singer,omitempty"`
	Title   *string `json:"title,omitempty"`
	Content *string `json:"content,omitempty"`
}

func (m *Music) TypeMake(data json.RawMessage) (any, error) {
	var x Music
	if err := json.Unmarshal(data, &x); err != nil {
		return nil, err
	}
	return x, nil
}

type Forward struct {
	ID      string     `json:"id"`
	Content *[]Message `json:"content,omitempty"`
}

func (f *Forward) TypeMake(data json.RawMessage) (any, error) {
	var x Forward
	if err := json.Unmarshal(data, &x); err != nil {
		return nil, err
	}
	return x, nil
}
