// agent/context.go 实现上下文组装（spec P2 §2.2/§3.5）：缓存分层
// system(纯静态) → 全会话混合历史(只追加,块裁剪) → [思考笔记]+本轮新消息(末尾,含当前时间)。
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

// agentGuideText guide 文件缺失时的内置兜底格式说明（正式版在 agent/guide.md）。
const agentGuideText = `[Agent 工作方式]
你在思考循环中：可先调用工具查资料、记笔记、调整状态，再决定是否回复。
- 要说话必须调用 send_message（可多次调用，分条发送）；不调用即本轮不回复。
- think(note) 记录思考笔记，下一轮对话可见；update_mood(mood) 更新你的情绪。
- 查历史用 get_recent_messages，搜历史用 search_messages；用法不确定先调 help。
- 想结束本轮思考就调用 finish；finish 前已发出的消息均已生效。`

// defaultGuidePath guide_file 未配置时的默认路径。
const defaultGuidePath = "agent/guide.md"

// Turn 进 builder 的一轮输入：chat 上下文、思考笔记、本轮新消息、当前时间。
// 携带全部原料，任何自定义 builder 都能拿到。
type Turn struct {
	ChatType    string // 本轮新消息所属会话类型，"private" / "group"（SendLLM 回复用）
	ChatID      int64  // 本轮新消息所属会话：群号；私聊 = 对方 QQ
	SelfID      int64  // 机器人自身 QQ
	Now         time.Time
	Mood        string        // 当前情绪状态（manager 从 state.json 读出），注入 system 尾部
	Notes       []Note        // 思考笔记（manager 从 notes.json 读出），注入末尾 user 块头部
	NewMessages []TurnMessage // 每条自带所属会话（历史已跨会话混合）
}

// TurnMessage 本轮防抖窗口内的一条新消息。
type TurnMessage struct {
	ChatType   string // 该消息所属会话类型 "private" / "group"
	ChatID     int64  // 该消息所属会话 ID：群号；私聊 = 对方 QQ
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

// DefaultBuilder 默认实现：persona + agent 格式说明 + 块裁剪历史 + 末尾新消息。
type DefaultBuilder struct {
	store   *storage.Store
	max     int
	trim    int
	persona string
	guide   string
}

// 编译期保证 DefaultBuilder 满足接口。
var _ ContextBuilder = (*DefaultBuilder)(nil)

// NewDefaultBuilder 构造默认 builder。persona/guide 文件缺失则告警一次
// 并使用内置兜底文本，不阻断运行。
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
	guidePath := cfg.GuideFile
	if guidePath == "" {
		guidePath = defaultGuidePath
	}
	if data, err := os.ReadFile(guidePath); err != nil {
		slog.Warn("读取 agent 格式说明文件失败，使用内置兜底文本", "path", guidePath, "err", err)
		b.guide = agentGuideText
	} else {
		b.guide = strings.TrimSpace(string(data))
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

// formatChatLine 统一消息行格式：[MM-DD HH:MM] [来源] <发送者> 内容
func formatChatLine(ts time.Time, source, label, content string) string {
	return "[" + ts.Format(timeLayout) + "] " + source + " <" + label + "> " + content
}

// chatSource 来源标签：[群:928952016] / [私聊:3394859101]；未知类型原样展示。
func chatSource(chatType string, chatID int64) string {
	name := chatType
	switch chatType {
	case "group":
		name = "群"
	case "private":
		name = "私聊"
	}
	return "[" + name + ":" + strconv.FormatInt(chatID, 10) + "]"
}

// senderLabel 发送者标签：自己 → "我"；有昵称 → "昵称(QQ号)"；否则 QQ号。
func senderLabel(senderID int64, senderName string, isSelf bool) string {
	if isSelf {
		return "我"
	}
	id := strconv.FormatInt(senderID, 10)
	if senderName == "" {
		return id
	}
	return senderName + "(" + id + ")"
}

// Build 组装消息序列（spec P2 §2.2）：
//
//	system:  persona + agent格式说明 + 机器人自身QQ号 (+当前情绪状态) ← 静态,缓存锚点
//	历史:    全会话混合的 user/assistant 按时间正序    ← 只追加
//	user:   [思考笔记] + 本轮新消息逐条 + 当前时间(首条头部) ← 每次变，放末尾
func (b *DefaultBuilder) Build(turn *Turn) []ChatMessage {
	system := b.persona + "\n" + b.guide + "\n当前账号QQ: " + strconv.FormatInt(turn.SelfID, 10)
	if turn.Mood != "" {
		system += "\n当前情绪状态: " + turn.Mood
	}
	msgs := []ChatMessage{{Role: "system", Content: system}}
	b.appendHistory(&msgs, turn)
	msgs = appendNewMessages(msgs, turn)
	return msgs
}

// appendHistory 查询并追加跨会话混合的历史消息（不按 chat 过滤，全局按时间
// 取最近 N 条），按时间正序（旧→新）。默认排除已撤回；查询失败时告警，
// 本轮不带历史继续对话。
func (b *DefaultBuilder) appendHistory(msgs *[]ChatMessage, turn *Turn) {
	if b.store == nil {
		return
	}
	records, err := b.store.QueryMessages(storage.MessageFilter{
		Limit: b.max + b.trim,
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
		isSelf := r.Direction == "sent"
		role := "user"
		if isSelf {
			role = "assistant"
		}
		*msgs = append(*msgs, ChatMessage{
			Role: role,
			Content: formatChatLine(time.Unix(int64(r.Time), 0), chatSource(r.ChatType, r.ChatID),
				senderLabel(r.UserID, r.SenderName, isSelf), truncateContent(r.RawMessage)),
		})
	}
}

// appendNewMessages 把本轮新消息以 user 角色逐条追加在末尾（保留原始节奏，
// 不做文本拼接），行格式与历史一致（含来源标签）；当前时间只出现在首条新
// 消息的头部，思考笔记若有则以 "[思考笔记]" 块作为首条新消息的前缀（在当前
// 时间之前）——不进 system，否则缓存前缀随笔记/时间失效。
func appendNewMessages(msgs []ChatMessage, turn *Turn) []ChatMessage {
	for i, m := range turn.NewMessages {
		content := formatChatLine(m.Time, chatSource(m.ChatType, m.ChatID),
			senderLabel(m.SenderID, m.SenderName, m.IsSelf), truncateContent(m.Text))
		if i == 0 {
			content = "当前时间: " + turn.Now.Format(nowLayout) + "\n" + content
			if len(turn.Notes) > 0 {
				content = FormatNotes(turn.Notes) + "\n\n" + content
			}
		}
		msgs = append(msgs, ChatMessage{Role: "user", Content: content})
	}
	return msgs
}
