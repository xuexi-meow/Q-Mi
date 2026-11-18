// Package context 保存工作上下文及其内部元数据，模型协议由 model 包负责。
package context

import "Q-Mi/agent/model"

type MessageKind uint8

const (
	KindWork MessageKind = iota
	KindState
	KindSummary
	KindActiveInput
)

// Message 将压缩分类与模型消息分开；发送请求、估算 tokens 时只读取 Wire。
type Message struct {
	Wire model.ChatMessage
	Kind MessageKind `json:"-"`
}

func ToWire(message Message) model.ChatMessage {
	return message.Wire
}

func FromWire(message model.ChatMessage, kind MessageKind) Message {
	return Message{Wire: message, Kind: kind}
}

// ToWireMessages 复制消息序列；嵌套的多模态内容和工具调用按只读数据处理。
func ToWireMessages(messages []Message) []model.ChatMessage {
	if messages == nil {
		return nil
	}
	out := make([]model.ChatMessage, len(messages))
	for i, message := range messages {
		out[i] = ToWire(message)
	}
	return out
}

func FromWireMessages(messages []model.ChatMessage) []Message {
	if messages == nil {
		return nil
	}
	out := make([]Message, len(messages))
	for i, message := range messages {
		out[i] = FromWire(message, KindWork)
	}
	return out
}
