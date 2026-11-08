// manager.go 实现会话队列与防抖（spec 第 6/9/10 节）：
// 以 (chat_type, chat_id) 为键，每会话一个懒创建的串行 worker；
// 消息先进 pending 缓冲，debounce 定时器到期后合并成一个 Turn 投递；
// worker 忙则排队，紧急消息（私聊/群@）可打断正在思考的 turn 立即重开（spec §5），
// panic 自兜底存活。handleTurn 为 P2 思考循环（spec §4）：LLM + 工具调用，finish/轮数上限终止。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"Q-Mi/app"
	"Q-Mi/bot"
	"Q-Mi/chain"
	"Q-Mi/storage"
	"Q-Mi/utils/config"
)

// IncomingMessage 事件接线层投给 Manager 的一条原始消息。
type IncomingMessage struct {
	MessageID  int64  // OneBot 消息 ID（图片锚定用）
	ChatType   string // "private" / "group"
	ChatID     int64  // 群号；私聊 = 对方 QQ
	SelfID     int64  // 机器人自身 QQ
	SenderID   int64
	SenderName string
	Text       string
	Images     []imageRef // 消息里的图片段（P2.7 多模态，wiring 提取）
	Time       time.Time
	Urgent     bool // 紧急（私聊/群@，wiring 判定）：思考中收到则打断当前 turn（spec §5）
}

// defaultDebounce 防抖窗口默认值（cfg.Debounce 非正时兜底）。
const defaultDebounce = 3 * time.Second

// defaultMaxToolRounds 思考轮数上限默认值（cfg.MaxToolRounds 非正时兜底，spec §4）。
const defaultMaxToolRounds = 10

// 默认思考状态文件路径（相对工作目录，已进 .gitignore）。
const (
	defaultNotesPath  = "agent/notes.json"
	defaultStatePath  = "agent/state.json"
	defaultTimersPath = "agent/timers.json" // P3.5 定时器记录
	defaultImagesDir  = "agent/images"      // 内容寻址图片落盘目录
)

// turnQueueCap 每个 worker 的 turn 队列缓冲；满了之后投递方阻塞等待。
const turnQueueCap = 64

// session 一个会话的队列状态：pending 缓冲 + 防抖 timer + 串行 worker 的 channel。
type session struct {
	ch      chan *Turn
	pending []TurnMessage
	timer   *time.Timer
	// currentCancel 正在思考的 turn 的取消函数（spec §5 插话打断）；
	// nil = worker 空闲。仅该会话的串行 worker 写入，Enqueue(urgent) 读取并调用。
	currentCancel context.CancelFunc
}

// Manager 会话队列：防抖合并、串行处理、思考循环（工具调用）、回复、debug 落盘。
type Manager struct {
	builder    ContextBuilder
	client     *Client
	cfg        config.AgentConfig
	store      *storage.Store
	timerStore *TimerStore // P3.5 定时提醒（schedule 工具的后端）
	selfID     int64       // 机器人自身 QQ（wiring 从 bot.qq_number 注入）；P3.6 定时轮询以它为会话 ID

	// debugDir/notesPath/statePath 做成私有字段便于测试注入 t.TempDir()。
	debugDir  string
	notesPath string
	statePath string

	// images 图片存储：优先复用 DefaultBuilder 自带的（历史多模态与工具
	// 共享同一份映射与磁盘缓存）；其他 builder（测试桩）没有缓存，manager
	// 自建一份。
	images *imageStore

	// registry 工具注册表（构造期固定板块，无热插拔）；tasks 后台任务管理器；
	// approvals 越界/危险操作审批管理器；browser 浏览器单实例（懒启动）。
	registry  *ToolRegistry
	tasks     *TaskManager
	approvals *ApprovalManager
	browser   *browserManager

	events *EventLog // W1 WebUI 思考链事件环形缓冲（spec §3.1）

	mu       sync.Mutex // 保护 sessions 及其中 pending/timer
	sessions map[string]*session
}

// NewManager 构造 Manager。store 可为 nil（无历史/查询工具报错）。
// selfID 为机器人自身 QQ（配置未填传 0：P3.6 定时轮询在 selfID<=0 时停用）。
// P3.5：构造后接上 TimerStore（fire 回调引用 m.Enqueue，须先有 manager
// 再造 store，避免循环依赖），随即恢复上次进程未触发的计时。
func NewManager(builder ContextBuilder, client *Client, cfg config.AgentConfig, store *storage.Store, selfID int64) *Manager {
	m := &Manager{
		builder:   builder,
		client:    client,
		cfg:       cfg,
		store:     store,
		selfID:    selfID,
		debugDir:  "agent/debug",
		notesPath: defaultNotesPath,
		statePath: defaultStatePath,
		sessions:  make(map[string]*session),
		events:    NewEventLog(),
	}
	// 图片缓存：优先复用 DefaultBuilder 自带的（历史多模态与工具共享
	// 正/负缓存）；其他 builder（测试桩）没有缓存，manager 自建一份。
	if db, ok := builder.(*DefaultBuilder); ok {
		m.images = db.Images()
	} else {
		m.images = newImageStore(defaultImagesDir, store)
	}
	m.registry = NewRegistry(enabledModules(cfg)...)
	m.tasks = NewTaskManager(m.deliverTaskResult)
	m.approvals = NewApprovalManager(m.notifyApproval, m.deliverApprovalResult)
	m.browser = newBrowserManager(cfg.BrowserExec, defaultBrowserProfileDir)
	m.timerStore = NewTimerStore(defaultTimersPath, m.fireTimer)
	m.timerStore.LoadPending() // 重启恢复：文件缺失/损坏时静默返回 0
	return m
}

// sessionKey 会话键。
func sessionKey(chatType string, chatID int64) string {
	return chatType + ":" + strconv.FormatInt(chatID, 10)
}

// Enqueue 追加一条消息进所属会话的 pending 缓冲，并重置防抖 timer。
// timer 到期后把 pending 快照合并成一个 Turn 投给该会话的串行 worker。
// 紧急消息（私聊/群@，spec §5）且该会话 worker 正在思考时打断：
// 取消当前 turn 的 LLM 请求，把 pending 全部内容立即合并成一个新 Turn 投递；
// worker 空闲时紧急消息仍走正常防抖（合并语义不变）。
func (m *Manager) Enqueue(msg IncomingMessage) {
	debounce := m.cfg.Debounce
	if debounce <= 0 {
		debounce = defaultDebounce
	}

	key := sessionKey(msg.ChatType, msg.ChatID)
	var flushTurn *Turn

	m.mu.Lock()
	s, ok := m.sessions[key]
	if !ok {
		s = &session{ch: make(chan *Turn, turnQueueCap)}
		m.sessions[key] = s
		go m.workerLoop(key, s.ch)
	}
	s.pending = append(s.pending, TurnMessage{
		MessageID:  msg.MessageID,
		SenderID:   msg.SenderID,
		SenderName: msg.SenderName,
		Text:       msg.Text,
		Images:     msg.Images,
		Time:       msg.Time,
	})

	if msg.Urgent && s.currentCancel != nil {
		// 插话打断：取消正在思考的 turn，pending 全部内容立即重开（spec §5）
		s.currentCancel()
		if s.timer != nil {
			s.timer.Stop()
		}
		for i := range s.pending {
			s.pending[i].ChatType = msg.ChatType
			s.pending[i].ChatID = msg.ChatID
		}
		flushTurn = &Turn{
			ChatType:    msg.ChatType,
			ChatID:      msg.ChatID,
			SelfID:      msg.SelfID,
			Now:         time.Now(),
			NewMessages: s.pending,
		}
		s.pending = nil
		s.timer = nil
	} else {
		if s.timer != nil {
			s.timer.Stop()
		}
		chatType, chatID, selfID := msg.ChatType, msg.ChatID, msg.SelfID
		s.timer = time.AfterFunc(debounce, func() {
			m.mu.Lock()
			if len(s.pending) == 0 {
				// pending 已被紧急 flush 提前取走（timer 来不及 Stop），本次触发作废
				s.timer = nil
				m.mu.Unlock()
				return
			}
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
	}
	m.mu.Unlock()

	if flushTurn != nil {
		s.ch <- flushTurn // 有缓冲;被打断的 turn 收尾后 worker 立即接手
	}
}

// deliverTaskResult 后台任务完成投递：格式化为合成消息入来源会话队列
// （复用防抖/打断/排队语义：worker 空闲立即开新 turn，思考中排队接上）。
// 已取消的任务不经过此回调（TaskManager 保证）。
func (m *Manager) deliverTaskResult(t BackgroundTask) {
	var line string
	switch t.Status {
	case TaskDone:
		line = fmt.Sprintf("[后台任务 #%d] %s 完成: %s", t.ID, t.Tool, t.Result)
	case TaskFailed:
		line = fmt.Sprintf("[后台任务 #%d] %s 失败: %s", t.ID, t.Tool, t.Err)
	default:
		return
	}
	m.Enqueue(IncomingMessage{
		ChatType:   t.ChatType,
		ChatID:     t.ChatID,
		SelfID:     t.SelfID,
		SenderID:   t.SelfID,
		SenderName: "后台任务",
		Text:       truncateRunes(line, maxEventText),
	})
}

// notifyApproval 审批请求送达管理员（私聊首位管理员 QQ）；未配置时只记日志。
func (m *Manager) notifyApproval(text string) {
	qq := m.primaryAdminQQ()
	if qq == 0 {
		slog.Warn("未配置管理员 QQ（bot.admin_number），审批请求无法送达", "text", text)
		return
	}
	if err := bot.Reply(context.Background(), "private", qq, chain.New().Text(text)); err != nil {
		slog.Warn("审批请求发送失败", "admin_qq", qq, "err", err)
	}
}

// primaryAdminQQ 首位管理员 QQ（审批通知目标）；未配置返回 0。
func (m *Manager) primaryAdminQQ() int64 {
	if a := app.GetApp(); a != nil && a.Config != nil && a.Config.Bot != nil && len(a.Config.Bot.Admin_Number) > 0 {
		return int64(a.Config.Bot.Admin_Number[0])
	}
	return 0
}

// deliverApprovalResult 审批结果投递回来源会话（复用会话队列语义）。
func (m *Manager) deliverApprovalResult(a Approval, result string, runErr error) {
	var line string
	switch {
	case a.Status == ApprovalRejected:
		line = fmt.Sprintf("[审批 #%d] %s 被管理员拒绝", a.ID, a.Tool)
	case runErr != nil:
		line = fmt.Sprintf("[审批 #%d] %s 失败: %v", a.ID, a.Tool, runErr)
	default:
		line = fmt.Sprintf("[审批 #%d] %s 完成: %s", a.ID, a.Tool, result)
	}
	m.Enqueue(IncomingMessage{
		ChatType:   a.ChatType,
		ChatID:     a.ChatID,
		SelfID:     a.SelfID,
		SenderID:   a.SelfID,
		SenderName: "审批",
		Text:       truncateRunes(line, maxEventText),
	})
}

// TryResolveApproval 拦截管理员的「同意/拒绝 <id>」回复：命中即裁决审批单
// （消息不进思考循环），并向管理员回执处理结果。管理员身份由调用方校验。
func (m *Manager) TryResolveApproval(text string) bool {
	id, approve, ok := parseApprovalReply(text)
	if !ok {
		return false
	}
	action := "同意"
	if !approve {
		action = "拒绝"
	}
	if err := m.approvals.Resolve(id, approve); err != nil {
		m.notifyApproval(fmt.Sprintf("%s 失败: %v", action, err))
		return true
	}
	m.notifyApproval(fmt.Sprintf("已%s审批 #%d，执行结果将送达发起会话", action, id))
	return true
}

// fireTimer P3.5 定时器到点回调：把提醒合成为一条 IncomingMessage 入队
// （发送者「定时」、SenderID=SelfID、文本 [定时提醒] 前缀）。走正常防抖
// 排队，不打断正在进行的思考；非紧急。
func (m *Manager) fireTimer(st ScheduledTimer) {
	m.Enqueue(IncomingMessage{
		ChatType:   st.ChatType,
		ChatID:     st.ChatID,
		SelfID:     st.SelfID,
		SenderID:   st.SelfID,
		SenderName: "定时",
		Text:       "[定时提醒] " + st.Note,
		Time:       time.Now(),
		Urgent:     false,
	})
}

// workerLoop 长驻消费循环：逐个处理 turn，panic 自兜底继续（不依赖外层 recover）。
func (m *Manager) workerLoop(key string, ch <-chan *Turn) {
	for turn := range ch {
		m.handleTurn(turn)
	}
}

// setCurrentCancel 记录/清除会话当前 turn 的取消函数（spec §5 插话打断）。
// 只有该会话的串行 worker（handleTurn）写入；Enqueue(urgent) 读取并调用。
func (m *Manager) setCurrentCancel(key string, cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[key]; ok {
		s.currentCancel = cancel
	}
}

// handleTurn 处理一个 turn（spec §4 思考循环）：
// 加载笔记/情绪 → 组装上下文 → 循环调 LLM：
//   - 响应含 tool_calls：逐个执行（finish 只标记不执行，同批其他工具先执行），
//     assistant + tool 消息回填后继续下一轮
//   - 纯文本响应：视为直接回复发出（兼容不会用工具的模型），结束
//   - finish：结束
//   - 轮数耗尽 / LLM 错误：强制结束，记日志
//   - ctx 被取消（spec §5 插话打断）：本轮不补处理、不回复，pending 已合并成新 turn
func (m *Manager) handleTurn(turn *Turn) {
	key := sessionKey(turn.ChatType, turn.ChatID)
	ctx, cancel := context.WithCancel(context.Background())
	m.setCurrentCancel(key, cancel)
	defer func() {
		m.setCurrentCancel(key, nil)
		cancel()
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

	// W1 WebUI 事件埋点：进入 turn（spec §3.1）
	m.events.Record(turn.ChatType, turn.ChatID, KindTurnStart, turnSummary(turn.NewMessages))

	msgs := m.builder.Build(turn)
	// P3-2 管理员门槛：本轮任一消息发送者是配置的管理员即视为管理员 turn，
	// 决定危险工具（踢人/退群/设管理员/逃生舱危险动作）是否放行。
	isAdmin := false
	for _, tm := range turn.NewMessages {
		if bot.IsAdmin(tm.SenderID) {
			isAdmin = true
			break
		}
	}
	toolCtx := &ToolContext{
		Store:     m.store,
		Turn:      turn,
		Timer:     m.timerStore,
		Images:    m.images,
		Tasks:     m.tasks,
		Approvals: m.approvals,
		Browser:   m.browser,
		Events:    m.events,
		Workspace: m.cfg.WorkspaceDir,
		NotesPath: m.notesPath,
		StatePath: m.statePath,
		Admin:     isAdmin,
	}
	tools := m.registry.Build(toolCtx)
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
		resp, chatErr := m.client.Chat(ctx, req)
		if chatErr != nil {
			interrupted := errors.Is(chatErr, context.Canceled)
			if m.cfg.Debug {
				m.dumpDebug(turn, req, round, nil, nil, chatErr, interrupted)
			}
			if interrupted {
				// 插话打断（spec §5）：不按普通错误刷屏，不补处理、不回复；
				// 消息都在 storage，新 turn 上下文自然包含
				m.events.Record(turn.ChatType, turn.ChatID, KindInterrupted, "本轮思考被紧急消息打断")
				slog.Info("本轮思考被紧急消息打断，等待新 turn 重开",
					"chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round)
				return
			}
			m.events.Record(turn.ChatType, turn.ChatID, KindError, chatErr.Error())
			slog.Warn("LLM 请求失败，本轮思考结束，不重试",
				"chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round, "err", chatErr)
			return
		}

		// 用量打点：缓存命中率掉坑 = 前缀被打断的信号（webui 概览有累计视图）。
		u := resp.Usage
		recordUsage(u)
		m.events.Record(turn.ChatType, turn.ChatID, KindUsage,
			fmt.Sprintf("hit=%d miss=%d out=%d", u.PromptCacheHitTokens, u.PromptCacheMissTokens, u.CompletionTokens))
		slog.Info("用量", "chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round,
			"cache_hit", u.PromptCacheHitTokens, "cache_miss", u.PromptCacheMissTokens,
			"prompt", u.PromptTokens, "output", u.CompletionTokens)

		// P2.5 思考链控制台输出：reasoning/content/工具调用/结束全打 Info，
		// 不截断（要看全文）、不加配置开关。
		if rc := resp.ReasoningContent(); rc != "" {
			slog.Info("思考", "chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round, "reasoning", rc)
			m.events.Record(turn.ChatType, turn.ChatID, KindReasoning, rc)
		}
		if content := resp.TextContent(); content != "" {
			slog.Info("输出", "chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round, "content", content)
			m.events.Record(turn.ChatType, turn.ChatID, KindOutput, content)
		}

		calls := resp.ToolCalls()
		if len(calls) == 0 {
			// 兼容路径：纯文本直接回复
			content := strings.TrimSpace(resp.TextContent())
			if m.cfg.Debug {
				m.dumpDebug(turn, req, round, resp, nil, nil, false)
			}
			m.replyText(turn, content)
			return
		}

		msgs = append(msgs, ChatMessage{Role: "assistant", Content: resp.TextContent(), ToolCalls: calls})
		finished := false
		type toolOutcome struct {
			executed bool
			id       string
			result   string
		}
		// 同轮多工具并发执行、按调用顺序回填（结果顺序=tool_calls 顺序）。
		outcomes := make([]toolOutcome, len(calls))
		var wg sync.WaitGroup
		for i, call := range calls {
			if call.Function.Name == "finish" {
				finished = true // 只标记，不执行；同批其他工具先执行
				continue
			}
			wg.Add(1)
			go func(i int, call ToolCall) {
				defer wg.Done()
				slog.Info("工具调用", "chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round,
					"tool", call.Function.Name, "args", call.Function.Arguments)
				m.events.Record(turn.ChatType, turn.ChatID, KindToolCall, call.Function.Name+" "+call.Function.Arguments)
				result, toolErr := m.execToolCall(toolCtx, tools, call)
				if toolErr != nil {
					slog.Info("工具执行失败", "chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round,
						"tool", call.Function.Name, "err", toolErr)
					m.events.Record(turn.ChatType, turn.ChatID, KindToolResult,
						fmt.Sprintf("%s: %s", call.Function.Name, toolErr))
					result = toolErrorJSON(toolErr)
				}
				outcomes[i] = toolOutcome{executed: true, id: call.ID, result: result}
			}(i, call)
		}
		wg.Wait()
		var results []string
		for _, oc := range outcomes {
			if !oc.executed {
				continue // finish 槽位
			}
			results = append(results, oc.result)
			msgs = append(msgs, ChatMessage{Role: "tool", Content: oc.result, ToolCallID: oc.id})
		}
		// P3-4 图片按需上传：本轮工具（view_message_images）申请查看的图片，
		// 在工具执行完当轮追加为一条 user 多模态消息（下一轮 LLM 请求即可见），
		// 随后清空。pendingImages 只在本会话的串行 worker（handleTurn）内
		// 读写——Enqueue 只写 TurnMessage、其他会话各有 worker，顺序执行
		// 无并发问题。
		if len(turn.pendingImages) > 0 {
			parts := make([]ContentPart, 0, len(turn.pendingImages)+1)
			parts = append(parts, ContentPart{Type: "text", Text: "以下图片来自上一轮工具结果（查询自动附带或你主动查看）"})
			for _, du := range turn.pendingImages {
				parts = append(parts, imagePart(du))
			}
			msgs = append(msgs, ChatMessage{Role: "user", Content: parts})
			turn.pendingImages = nil
		}
		if m.cfg.Debug {
			m.dumpDebug(turn, req, round, resp, results, nil, false)
		}
		if finished {
			m.events.Record(turn.ChatType, turn.ChatID, KindFinish, "结束思考")
			slog.Info("结束思考", "chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round)
			return
		}
	}

	m.events.Record(turn.ChatType, turn.ChatID, KindError, "轮数上限")
	slog.Warn("思考轮数达到上限，强制结束本轮",
		"chat_type", turn.ChatType, "chat_id", turn.ChatID, "max_tool_rounds", maxRounds)
}

// replyText 兼容路径发送纯文本回复；空内容不发送。
func (m *Manager) replyText(turn *Turn, content string) {
	if content == "" {
		m.events.Record(turn.ChatType, turn.ChatID, KindError, "LLM 返回空回复，不发送")
		slog.Warn("LLM 返回空回复，不发送",
			"chat_type", turn.ChatType, "chat_id", turn.ChatID)
		return
	}
	if err := bot.Reply(context.Background(), turn.ChatType, turn.ChatID, chain.New().Text(content)); err != nil {
		m.events.Record(turn.ChatType, turn.ChatID, KindError, "回复发送失败: "+err.Error())
		slog.Warn("回复发送失败",
			"chat_type", turn.ChatType, "chat_id", turn.ChatID, "err", err)
		return
	}
	m.events.Record(turn.ChatType, turn.ChatID, KindReply, content)
}

// execToolCall 执行一次工具调用：普通调用同步执行返回结果；Asyncable 工具
// 带 {"_async":true,...} 时转入 TaskManager 后台执行，立即返回任务回执
// （结果完成经 deliverTaskResult 唤醒投递，不阻塞本 turn，spec §2.5 方案 B）。
// 未知工具 / Run 缺失 / 执行失败返回 error，由调用方包装成 {"error": ...} 回填。
func (m *Manager) execToolCall(ctx *ToolContext, tools []Tool, call ToolCall) (string, error) {
	var tool *Tool
	for i := range tools {
		if tools[i].Name == call.Function.Name {
			tool = &tools[i]
			break
		}
	}
	if tool == nil {
		return "", fmt.Errorf("未知工具 %q", call.Function.Name)
	}
	if tool.Run == nil {
		return "", fmt.Errorf("工具 %q 不可执行", call.Function.Name)
	}
	if asyncRequested(json.RawMessage(call.Function.Arguments)) {
		if !tool.Asyncable {
			return "", fmt.Errorf("工具 %q 不支持 _async 后台执行", tool.Name)
		}
		if ctx.Turn == nil {
			return "", fmt.Errorf("无会话上下文，无法后台执行 %q", tool.Name)
		}
		args := stripAsyncKey(json.RawMessage(call.Function.Arguments))
		id := m.tasks.Start(BackgroundTask{
			Tool:      tool.Name,
			ArgsBrief: truncateRunes(args, 200),
			ChatType:  ctx.Turn.ChatType,
			ChatID:    ctx.Turn.ChatID,
			SelfID:    ctx.Turn.SelfID,
		}, func(context.Context) (string, error) {
			return tool.Run(json.RawMessage(args))
		})
		return marshalCompact(map[string]any{
			"task_id": id,
			"status":  "started",
			"note":    "后台执行中，完成后自动送达结果；task_list/task_get 可随时查看，task_cancel 可停止",
		})
	}
	result, toolErr := tool.Run(json.RawMessage(call.Function.Arguments))
	var need approvalNeeded
	if errors.As(toolErr, &need) {
		// 越界/危险操作 → 管理员审批（spec 权限模型）：登记审批单、QQ 通知
		// 管理员，当轮返回回执；管理员「同意 <id>」后以 Approved ctx 重跑并
		// 投递结果，「拒绝」投递拒绝回执。不阻塞本 turn。
		if ctx.Approvals == nil {
			return "", fmt.Errorf("%s（审批通道不可用）", need.Error())
		}
		if ctx.Turn == nil {
			return "", fmt.Errorf("无会话上下文，无法发起审批")
		}
		id, reqErr := ctx.Approvals.Request(Approval{
			Tool:     tool.Name,
			Args:     call.Function.Arguments,
			Reason:   need.reason,
			ChatType: ctx.Turn.ChatType,
			ChatID:   ctx.Turn.ChatID,
			SelfID:   ctx.Turn.SelfID,
			run: func(bool) (string, error) {
				c2 := *ctx
				c2.Approved = true
				return tool.Run(json.RawMessage(call.Function.Arguments))
			},
		})
		if reqErr != nil {
			return "", fmt.Errorf("发起审批失败: %w", reqErr)
		}
		return marshalCompact(map[string]any{
			"approval_id": id,
			"status":      "pending",
			"note":        "已请求管理员确认，管理员回复「同意 <id>」后自动执行并送达结果；期间你可以继续其他工作",
		})
	}
	return result, toolErr
}

// asyncRequested 参数顶层是否带 "_async": true（保留键，仅在顶层生效）。
func asyncRequested(args json.RawMessage) bool {
	if len(args) == 0 {
		return false
	}
	var peek struct {
		Async bool `json:"_async"`
	}
	if json.Unmarshal(args, &peek) != nil {
		return false
	}
	return peek.Async
}

// stripAsyncKey 剥离顶层 _async 键；后台任务的 Run 不应看到该保留键。
func stripAsyncKey(args json.RawMessage) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err != nil || m == nil {
		return string(args)
	}
	delete(m, "_async")
	out, err := json.Marshal(m)
	if err != nil {
		return string(args)
	}
	return string(out)
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
		rec.Content = resp.TextContent()
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
