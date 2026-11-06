// manager.go 实现会话队列与防抖（spec 第 6/9/10 节）：
// 以 (chat_type, chat_id) 为键，每会话一个懒创建的串行 worker；
// 消息先进 pending 缓冲，debounce 定时器到期后合并成一个 Turn 投递；
// worker 忙则排队不打断，panic 自兜底存活。
// handleTurn 为 P2 思考循环（spec §4）：LLM + 工具调用，finish/轮数上限终止。
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"Q-Mi/bot"
	"Q-Mi/chain"
	"Q-Mi/storage"
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

// defaultMaxToolRounds 思考轮数上限默认值（cfg.MaxToolRounds 非正时兜底，spec §4）。
const defaultMaxToolRounds = 10

// 默认思考状态文件路径（相对工作目录，已进 .gitignore）。
const (
	defaultNotesPath = "agent/notes.json"
	defaultStatePath = "agent/state.json"
)

// turnQueueCap 每个 worker 的 turn 队列缓冲；满了之后投递方阻塞等待。
const turnQueueCap = 64

// session 一个会话的队列状态：pending 缓冲 + 防抖 timer + 串行 worker 的 channel。
type session struct {
	ch      chan *Turn
	pending []TurnMessage
	timer   *time.Timer
}

// Manager 会话队列：防抖合并、串行处理、思考循环（工具调用）、回复、debug 落盘。
type Manager struct {
	builder ContextBuilder
	client  *Client
	cfg     config.AgentConfig
	store   *storage.Store

	// debugDir/notesPath/statePath 做成私有字段便于测试注入 t.TempDir()。
	debugDir  string
	notesPath string
	statePath string

	mu       sync.Mutex // 保护 sessions 及其中 pending/timer
	sessions map[string]*session
}

// NewManager 构造 Manager。store 可为 nil（无历史/查询工具报错）。
func NewManager(builder ContextBuilder, client *Client, cfg config.AgentConfig, store *storage.Store) *Manager {
	return &Manager{
		builder:   builder,
		client:    client,
		cfg:       cfg,
		store:     store,
		debugDir:  "agent/debug",
		notesPath: defaultNotesPath,
		statePath: defaultStatePath,
		sessions:  make(map[string]*session),
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
		// 每条 TurnMessage 自带所属会话（session 键即会话，二者恒一致）
		for i := range s.pending {
			s.pending[i].ChatType = chatType
			s.pending[i].ChatID = chatID
		}
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

// handleTurn 处理一个 turn（spec §4 思考循环）：
// 加载笔记/情绪 → 组装上下文 → 循环调 LLM：
//   - 响应含 tool_calls：逐个执行（finish 只标记不执行，同批其他工具先执行），
//     assistant + tool 消息回填后继续下一轮
//   - 纯文本响应：视为直接回复发出（兼容不会用工具的模型），结束
//   - finish：结束
//   - 轮数耗尽 / LLM 错误：强制结束，记日志
func (m *Manager) handleTurn(turn *Turn) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("agent worker panic，已恢复继续服务",
				"chat_type", turn.ChatType, "chat_id", turn.ChatID, "panic", r)
		}
	}()

	notes, err := LoadNotes(m.notesPath)
	if err != nil {
		slog.Warn("读取思考笔记失败，本轮不带笔记", "path", m.notesPath, "err", err)
		notes = nil
	}
	turn.Notes = notes
	turn.Mood = m.loadMood()

	msgs := m.builder.Build(turn)
	tools := newToolSet(m.store, m.notesPath, m.statePath)
	defs := toolDefs(tools)
	maxRounds := m.cfg.MaxToolRounds
	if maxRounds <= 0 {
		maxRounds = defaultMaxToolRounds
	}

	for round := 1; round <= maxRounds; round++ {
		req := ChatRequest{
			Model:       m.cfg.Model,
			Messages:    msgs,
			Temperature: m.cfg.Temperature,
			Tools:       defs,
		}
		resp, chatErr := m.client.Chat(context.Background(), req)
		if chatErr != nil {
			if m.cfg.Debug {
				m.dumpDebug(turn, req, round, nil, nil, chatErr, false)
			}
			slog.Warn("LLM 请求失败，本轮思考结束，不重试",
				"chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round, "err", chatErr)
			return
		}

		calls := resp.ToolCalls()
		if len(calls) == 0 {
			// 兼容路径：纯文本直接回复
			content := strings.TrimSpace(resp.Content())
			if m.cfg.Debug {
				m.dumpDebug(turn, req, round, resp, nil, nil, false)
			}
			m.replyText(turn, content)
			return
		}

		msgs = append(msgs, ChatMessage{Role: "assistant", Content: resp.Content(), ToolCalls: calls})
		finished := false
		var results []string
		for _, call := range calls {
			if call.Function.Name == "finish" {
				finished = true // 只标记，不执行；同批其他工具先执行
				continue
			}
			result := execTool(tools, call)
			results = append(results, result)
			msgs = append(msgs, ChatMessage{Role: "tool", Content: result, ToolCallID: call.ID})
		}
		if m.cfg.Debug {
			m.dumpDebug(turn, req, round, resp, results, nil, false)
		}
		if finished {
			return
		}
	}

	slog.Warn("思考轮数达到上限，强制结束本轮",
		"chat_type", turn.ChatType, "chat_id", turn.ChatID, "max_tool_rounds", maxRounds)
}

// replyText 兼容路径发送纯文本回复；空内容不发送。
func (m *Manager) replyText(turn *Turn, content string) {
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

// execTool 按名字执行工具；未知工具 / Run 缺失 / 执行失败统一包成
// {"error": ...} 返回，思维链继续（模型自行调整）。
func execTool(tools []Tool, call ToolCall) string {
	for _, t := range tools {
		if t.Name != call.Function.Name {
			continue
		}
		if t.Run == nil {
			break
		}
		out, err := t.Run(json.RawMessage(call.Function.Arguments))
		if err != nil {
			return toolErrorJSON(err)
		}
		return out
	}
	return toolErrorJSON(fmt.Errorf("未知工具 %q", call.Function.Name))
}

// toolErrorJSON 工具错误的标准回填形态。
func toolErrorJSON(err error) string {
	data, mErr := json.Marshal(map[string]string{"error": err.Error()})
	if mErr != nil {
		return `{"error":"工具执行失败"}`
	}
	return string(data)
}

// loadMood 读取情绪状态；文件缺失/损坏视为无情绪。
func (m *Manager) loadMood() string {
	state, err := readStateFrom(m.statePath)
	if err != nil {
		slog.Warn("读取情绪状态失败", "path", m.statePath, "err", err)
		return ""
	}
	return state.Mood
}

// debugRecord 落盘的单轮记录：完整请求 JSON + 响应内容 + 工具调用与结果摘要，
// 直接可回放对比。Interrupted 为 P2 插话打断预留字段。
type debugRecord struct {
	Time        int64           `json:"time"`
	ChatType    string          `json:"chat_type"`
	ChatID      int64           `json:"chat_id"`
	Round       int             `json:"round"`
	Interrupted bool            `json:"interrupted"`
	Request     json.RawMessage `json:"request"`
	Content     string          `json:"response_content,omitempty"`
	Reasoning   string          `json:"reasoning_content,omitempty"`
	ToolCalls   []debugToolCall `json:"tool_calls,omitempty"`
	ToolResults []string        `json:"tool_results,omitempty"`
	Err         string          `json:"error,omitempty"`
}

// debugToolCall 单次工具调用摘要。
type debugToolCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments,omitempty"`
}

// dumpDebug 把单轮请求与响应写入 <debugDir>/<chat_type>-<chat_id>/<unix_nano>.json。
// 写失败只告警，不影响正常流程。
func (m *Manager) dumpDebug(turn *Turn, req ChatRequest, round int, resp *ChatResponse, toolResults []string, chatErr error, interrupted bool) {
	reqJSON, err := json.Marshal(req)
	if err != nil {
		slog.Warn("debug 落盘：序列化请求失败", "err", err)
		return
	}
	rec := debugRecord{
		Time:        time.Now().UnixNano(),
		ChatType:    turn.ChatType,
		ChatID:      turn.ChatID,
		Round:       round,
		Interrupted: interrupted,
		Request:     reqJSON,
		ToolResults: toolResults,
	}
	if resp != nil {
		rec.Content = resp.Content()
		rec.Reasoning = resp.ReasoningContent()
		for _, c := range resp.ToolCalls() {
			rec.ToolCalls = append(rec.ToolCalls, debugToolCall{Name: c.Function.Name, Arguments: c.Function.Arguments})
		}
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
