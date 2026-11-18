package context

import (
	"log/slog"
	"strings"
	"time"
)

// ProviderInput 不携带可变的消息、工具或历史，提供器只贡献参考资料。
type ProviderInput struct {
	Scope ProviderScope
	Now   time.Time
}

type ProviderScope struct {
	SelfID   int64  `json:"self_id"`
	ChatType string `json:"chat_type"`
	ChatID   int64  `json:"chat_id"`
}

type StateFragment struct {
	Scope ProviderScope
	Text  string
}

type StateProvider interface {
	Name() string
	State(ProviderInput) (StateFragment, error)
}

type stateReference struct {
	Provider string        `json:"provider"`
	Scope    ProviderScope `json:"scope"`
	Text     string        `json:"text"`
}

// SetStateProviders 在组装时按固定顺序注册，不增加用户配置或动态加载。
func (b *DefaultBuilder) SetStateProviders(providers ...StateProvider) {
	b.mu.Lock()
	b.providers = append([]StateProvider(nil), providers...)
	b.mu.Unlock()
}

func (b *DefaultBuilder) stateReferences(turn *Turn) []stateReference {
	scope := ProviderScope{SelfID: turn.SelfID, ChatType: turn.ChatType, ChatID: turn.ChatID}
	// 混合会话批次不能隐式选择某一个群，也不能省略账号身份。
	if scope.SelfID == 0 || scope.ChatID == 0 || (scope.ChatType != "group" && scope.ChatType != "private") {
		return nil
	}
	b.mu.Lock()
	providers := append([]StateProvider(nil), b.providers...)
	b.mu.Unlock()
	var references []stateReference
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		name := strings.TrimSpace(provider.Name())
		if name == "" {
			slog.Warn("上下文提供器缺少名称，已跳过")
			continue
		}
		fragment, err := provider.State(ProviderInput{Scope: scope, Now: turn.Now})
		if err != nil {
			slog.Warn("可选上下文提供器失败，已跳过", "provider", name, "err", err)
			continue
		}
		if fragment.Scope != scope {
			slog.Warn("上下文提供器返回不同会话的片段，已跳过", "provider", name)
			continue
		}
		text := strings.TrimSpace(fragment.Text)
		if text == "" {
			continue
		}
		if runes := []rune(text); len(runes) > builderMaxStyleRunes {
			text = string(runes[:builderMaxStyleRunes]) + "...(截断)"
		}
		references = append(references, stateReference{Provider: name, Scope: scope, Text: text})
	}
	return references
}
