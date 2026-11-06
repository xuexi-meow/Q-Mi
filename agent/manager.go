// manager.go 实现会话队列与防抖（spec 第 6/9/10 节）：
// 以 (chat_type, chat_id) 为键，每会话一个懒创建的串行 worker；
// 消息先进 pending 缓冲，debounce 定时器到期后合并成一个 Turn 投递；
// worker 忙则排队不打断（P1 无 abort），panic 自兜底存活。
package agent

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"Q-Mi/bot"
	"Q-Mi/chain"
	"Q-Mi/utils/config"
)

// IncomingMessage 事件接线层投给 Manager 的一条原始消息。
type IncomingMessage struct {
	ChatType   string // "private" / "group"
	ChatID     int64  // 群号；私聊 = 对方 QQ
	SelfID     int64  // 机器人自身 QQ
	SenderID   int64
	SenderName string
	Text       string
	Time       time.Time
}

// defaultDebounce 防抖窗口默认值（cfg.Debounce 非正时兜底）。
const defaultDebounce = 3 * time.Second

// turnQueueCap 每个 worker 的 turn 队列缓冲；满了之后投递方阻塞等待。
const turnQueueCap = 64

// session 一个会话的队列状态：pending 缓冲 + 防抖 timer + 串行 worker 的 channel。
type session struct {
	ch      chan *Turn
	pending []TurnMessage
	timer   *time.Timer
}

// Manager 会话队列：防抖合并、串行处理、调 LLM、回复、debug 落盘。
type Manager struct {
	builder ContextBuilder
	client  *Client
	cfg     config.AgentConfig

	// debugDir debug 落盘根目录，默认 "agent/debug"；
	// 做成私有字段便于测试注入 t.TempDir()。
	debugDir string

	mu       sync.Mutex // 保护 sessions 及其中 pending/timer
	sessions map[string]*session
}

// NewManager 构造 Manager。
func NewManager(builder ContextBuilder, client *Client, cfg config.AgentConfig) *Manager {
	return &Manager{
		builder:  builder,
		client:   client,
		cfg:      cfg,
		debugDir: "agent/debug",
		sessions: make(map[string]*session),
	}
}

// sessionKey 会话键。
func sessionKey(chatType string, chatID int64) string {
	return chatType + ":" + strconv.FormatInt(chatID, 10)
}

// Enqueue 追加一条消息进所属会话的 pending 缓冲，并重置防抖 timer。
// timer 到期后把 pending 快照合并成一个 Turn 投给该会话的串行 worker。
func (m *Manager) Enqueue(msg IncomingMessage) {
	debounce := m.cfg.Debounce
	if debounce <= 0 {
		debounce = defaultDebounce
	}

	key := sessionKey(msg.ChatType, msg.ChatID)
	m.mu.Lock()
	s, ok := m.sessions[key]
	if !ok {
		s = &session{ch: make(chan *Turn, turnQueueCap)}
		m.sessions[key] = s
		go m.workerLoop(key, s.ch)
	}
	s.pending = append(s.pending, TurnMessage{
		SenderID:   msg.SenderID,
		SenderName: msg.SenderName,
		Text:       msg.Text,
		Time:       msg.Time,
	})
	if s.timer != nil {
		s.timer.Stop()
	}
	chatType, chatID, selfID := msg.ChatType, msg.ChatID, msg.SelfID
	s.timer = time.AfterFunc(debounce, func() {
		m.mu.Lock()
		turn := &Turn{
			ChatType:    chatType,
			ChatID:      chatID,
			SelfID:      selfID,
			Now:         time.Now(),
			NewMessages: s.pending,
		}
		s.pending = nil
		s.timer = nil
		m.mu.Unlock()
		s.ch <- turn // 有缓冲;worker 忙则排队,不打断正在进行的生成
	})
	m.mu.Unlock()
}

// workerLoop 长驻消费循环：逐个处理 turn，panic 自兜底继续（不依赖外层 recover）。
func (m *Manager) workerLoop(key string, ch <-chan *Turn) {
	for turn := range ch {
		m.handleTurn(turn)
	}
}

// handleTurn 处理一个 turn：组装上下文 → 调 LLM（重试 0 次）→ 回复。
func (m *Manager) handleTurn(turn *Turn) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("agent worker panic，已恢复继续服务",
				"chat_type", turn.ChatType, "chat_id", turn.ChatID, "panic", r)
		}
	}()

	msgs := m.builder.Build(turn)
	req := ChatRequest{
		Model:       m.cfg.Model,
		Messages:    msgs,
		Temperature: m.cfg.Temperature,
	}

	resp, err := m.client.Chat(context.Background(), req)
	content := ""
	if err != nil {
		slog.Warn("LLM 请求失败，重试 0 次即止，本轮不回复",
			"chat_type", turn.ChatType, "chat_id", turn.ChatID, "err", err)
	} else {
		content = strings.TrimSpace(resp.Content())
	}

	if m.cfg.Debug {
		m.dumpDebug(turn, req, content, err)
	}

	if err != nil {
		return
	}
	if content == "" {
		slog.Warn("LLM 返回空回复，不发送",
			"chat_type", turn.ChatType, "chat_id", turn.ChatID)
		return
	}
	if err := bot.Reply(context.Background(), turn.ChatType, turn.ChatID, chain.New().Text(content)); err != nil {
		slog.Warn("回复发送失败",
			"chat_type", turn.ChatType, "chat_id", turn.ChatID, "err", err)
	}
}

// debugRecord 落盘的单个 turn 记录：完整请求 JSON + 响应内容，直接可回放对比。
type debugRecord struct {
	Time     int64           `json:"time"`
	ChatType string          `json:"chat_type"`
	ChatID   int64           `json:"chat_id"`
	Request  json.RawMessage `json:"request"`
	Content  string          `json:"response_content,omitempty"`
	Err      string          `json:"error,omitempty"`
}

// dumpDebug 把请求与响应写入 <debugDir>/<chat_type>-<chat_id>/<unix_nano>.json。
// 写失败只告警，不影响正常流程。
func (m *Manager) dumpDebug(turn *Turn, req ChatRequest, content string, chatErr error) {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		slog.Warn("debug 落盘：序列化请求失败", "err", err)
		return
	}
	rec := debugRecord{
		Time:     time.Now().UnixNano(),
		ChatType: turn.ChatType,
		ChatID:   turn.ChatID,
		Request:  reqJSON,
		Content:  content,
	}
	if chatErr != nil {
		rec.Err = chatErr.Error()
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		slog.Warn("debug 落盘：序列化记录失败", "err", err)
		return
	}
	dir := filepath.Join(m.debugDir, turn.ChatType+"-"+strconv.FormatInt(turn.ChatID, 10))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("debug 落盘：创建目录失败", "dir", dir, "err", err)
		return
	}
	path := filepath.Join(dir, strconv.FormatInt(rec.Time, 10)+".json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		slog.Warn("debug 落盘失败", "path", path, "err", err)
	}
}
