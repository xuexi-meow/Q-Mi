package agent

import (
	agentcontext "Q-Mi/agent/context"
	"Q-Mi/storage"
	"Q-Mi/utils/config"
)

// contextBuilderAdapter 在迁移期间保留 agent 包的旧接口。
type contextBuilderAdapter struct {
	builder *agentcontext.DefaultBuilder
	images  *imageStore
}

func newDefaultContextBuilder(store *storage.Store, cfg config.AgentConfig) *contextBuilderAdapter {
	builder := agentcontext.NewDefaultBuilderWithFallbacks(store, cfg, fallbackPersona, agentGuideText)
	builder.SetHistoryAttention(recentAttention)
	return newContextBuilderAdapter(builder)
}

func newContextBuilderAdapter(builder *agentcontext.DefaultBuilder) *contextBuilderAdapter {
	var images *imageStore
	if builder != nil && builder.Images() != nil {
		store := builder.Images()
		images = &imageStore{inner: store, dir: store.Directory()}
	}
	return &contextBuilderAdapter{builder: builder, images: images}
}

func (a *contextBuilderAdapter) Build(turn *Turn) []ChatMessage {
	if a == nil || a.builder == nil {
		return nil
	}
	return agentcontext.ToWireMessages(a.builder.BuildMessages(turn))
}

func (a *contextBuilderAdapter) BuildWorking(turn *Turn) []ChatMessage {
	if a == nil || a.builder == nil {
		return nil
	}
	return agentcontext.ToWireMessages(a.builder.BuildWorkingMessages(turn))
}

func (a *contextBuilderAdapter) BuildWorkingMessages(turn *Turn) []agentcontext.Message {
	if a == nil || a.builder == nil {
		return nil
	}
	return a.builder.BuildWorkingMessages(turn)
}

func (a *contextBuilderAdapter) BuildContinuation(turn *Turn) []ChatMessage {
	if a == nil || a.builder == nil {
		return nil
	}
	return agentcontext.ToWireMessages(a.builder.BuildContinuationMessages(turn))
}

func (a *contextBuilderAdapter) StateText(turn *Turn) string {
	if a == nil || a.builder == nil {
		return ""
	}
	return a.builder.StateText(turn)
}

func (a *contextBuilderAdapter) SystemMessage(turn *Turn) ChatMessage {
	if a == nil || a.builder == nil {
		return ChatMessage{}
	}
	return a.builder.SystemMessage(turn).Wire
}

func (a *contextBuilderAdapter) SetToolDocs(docs string) {
	if a != nil && a.builder != nil {
		a.builder.SetToolDocs(docs)
	}
}

func (a *contextBuilderAdapter) SetStateProviders(providers ...agentcontext.StateProvider) {
	if a != nil && a.builder != nil {
		a.builder.SetStateProviders(providers...)
	}
}

func (a *contextBuilderAdapter) SetHistoryAttention(attention agentcontext.HistoryAttention) {
	if a != nil && a.builder != nil {
		a.builder.SetHistoryAttention(attention)
	}
}

func (a *contextBuilderAdapter) SetImageResolver(resolve func(messageID int64, index int, url string) string) {
	if a != nil && a.builder != nil {
		a.builder.SetImageResolver(resolve)
	}
}

func (a *contextBuilderAdapter) Apply(cfg config.AgentConfig) {
	if a != nil && a.builder != nil {
		a.builder.Apply(cfg)
	}
}

func (a *contextBuilderAdapter) Images() *imageStore {
	if a == nil {
		return nil
	}
	return a.images
}

var _ ContextBuilder = (*contextBuilderAdapter)(nil)
