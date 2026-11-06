// agent/context.go 实现上下文组装（spec 第 7/9 节）：缓存分层
// system(纯静态) → 历史(只追加) → 本轮新消息(末尾,含当前时间)。
// ContextBuilder 是可替换接缝，DefaultBuilder 为默认实现。
package agent

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"Q-Mi/storage"
	"Q-Mi/utils/config"
)

// fallbackPersona persona 文件缺失时的内置兜底提示词。
const fallbackPersona = "你是一个QQ群聊/私聊里的AI成员，用中文自然交流。"

// Turn 进 builder 的一轮输入：chat 上下文、本轮新消息、当前时间。
// 携带全部原料，任何自定义 builder 都能拿到。
type Turn struct {
	ChatType    string // "private" / "group"
	ChatID      int64  // 群号；私聊 = 对方 QQ
	SelfID      int64  // 机器人自身 QQ
	Now         time.Time
	NewMessages []TurnMessage
}

// TurnMessage 本轮防抖窗口内的一条新消息。
type TurnMessage struct {
	SenderID   int64
	SenderName string
	Text       string
	IsSelf     bool
	Time       time.Time
}

// ContextBuilder 上下文组装接口。agent 流程只依赖该接口，
// 替换实现 = 换一个构造参数，不动队列/触发/发送逻辑。
type ContextBuilder interface {
	Build(turn *Turn) []ChatMessage
}

// DefaultBuilder 默认实现：persona + 块裁剪历史 + 末尾新消息。
type DefaultBuilder struct {
	store   *storage.Store
	max     int
	trim    int
	persona string
}

// 编译期保证 DefaultBuilder 满足接口。
var _ ContextBuilder = (*DefaultBuilder)(nil)

// NewDefaultBuilder 构造默认 builder。构造时读取 cfg.PersonaFile，
// 缺失则告警一次并使用内置兜底提示词，不阻断运行。
func NewDefaultBuilder(store *storage.Store, cfg config.AgentConfig) *DefaultBuilder {
	b := &DefaultBuilder{
		store: store,
		max:   cfg.History.Max,
		trim:  cfg.History.Trim,
	}
	data, err := os.ReadFile(cfg.PersonaFile)
	if err != nil {
		slog.Warn("读取 persona 文件失败，使用内置兜底提示词", "path", cfg.PersonaFile, "err", err)
		b.persona = fallbackPersona
	} else {
		b.persona = strings.TrimSpace(string(data))
	}
	return b
}

// 历史消息行时间格式：[MM-DD HH:MM]
const timeLayout = "01-02 15:04"

// 新消息时间头格式：完整日期 + 时间
const nowLayout = "2006-01-02 15:04"

const (
	// maxContentRunes 单条消息内容字符数上限，超出截断（spec 第 10 节）。
	maxContentRunes = 20000
	// truncationMark 截断标注。
	truncationMark = "...(截断)"
)

// truncateContent 单条内容超过 maxContentRunes 字符时截断并标注。
func truncateContent(s string) string {
	runes := []rune(s)
	if len(runes) <= maxContentRunes {
		return s
	}
	return string(runes[:maxContentRunes]) + truncationMark
}

// formatChatLine 统一的消息行格式：[MM-DD HH:MM] <QQ号或'我'> 内容
func formatChatLine(ts time.Time, label, content string) string {
	return "[" + ts.Format(timeLayout) + "] <" + label + "> " + content
}

// Build 组装消息序列：
//
//	system:  persona + 机器人自身QQ号          ← 完全静态，缓存锚点
//	历史:    user/assistant 按时间正序          ← 只追加
//	user:   本轮新消息逐条 + 当前时间(首条头部)  ← 每次变，放末尾
func (b *DefaultBuilder) Build(turn *Turn) []ChatMessage {
	system := ChatMessage{
		Role:    "system",
		Content: b.persona + "\n当前账号QQ: " + strconv.FormatInt(turn.SelfID, 10),
	}
	msgs := []ChatMessage{system}
	b.appendHistory(&msgs, turn)
	msgs = appendNewMessages(msgs, turn)
	return msgs
}

// appendHistory 查询并追加历史消息，按时间正序（旧→新）。
// 默认排除已撤回；查询失败时告警，本轮不带历史继续对话。
func (b *DefaultBuilder) appendHistory(msgs *[]ChatMessage, turn *Turn) {
	if b.store == nil {
		return
	}
	records, err := b.store.QueryMessages(storage.MessageFilter{
		ChatType: turn.ChatType,
		ChatID:   turn.ChatID,
		Limit:    b.max + b.trim,
	})
	if err != nil {
		slog.Warn("查询历史消息失败，本轮不带历史上下文", "err", err)
		return
	}
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 { // 倒序 → 正序
		records[i], records[j] = records[j], records[i]
	}
	// 超过 max 时按 trim 从头整块砍掉（查询 limit 为 max+trim，故最多砍一块；
	// 整块跳删保证两次调用间历史前缀字节级一致 → 命中 prompt cache）
	if n := len(records); b.max > 0 && b.trim > 0 && n > b.max {
		drop := (n - b.max + b.trim - 1) / b.trim * b.trim
		if drop > n {
			drop = n
		}
		records = records[drop:]
	}
	for _, r := range records {
		label := strconv.FormatInt(r.UserID, 10)
		role := "user"
		if r.Direction == "sent" {
			label = "我"
			role = "assistant"
		}
		*msgs = append(*msgs, ChatMessage{
			Role:    role,
			Content: formatChatLine(time.Unix(int64(r.Time), 0), label, truncateContent(r.RawMessage)),
		})
	}
}

// appendNewMessages 把本轮新消息以 user 角色逐条追加在末尾（保留原始节奏，
// 不做文本拼接）；当前时间只出现在首条新消息的头部，随消息放末尾，
// 不进 system——否则缓存前缀随时间失效。
func appendNewMessages(msgs []ChatMessage, turn *Turn) []ChatMessage {
	for i, m := range turn.NewMessages {
		label := strconv.FormatInt(m.SenderID, 10)
		if m.IsSelf {
			label = "我"
		}
		content := formatChatLine(m.Time, label, truncateContent(m.Text))
		if i == 0 {
			content = "当前时间: " + turn.Now.Format(nowLayout) + "\n" + content
		}
		msgs = append(msgs, ChatMessage{Role: "user", Content: content})
	}
	return msgs
}
