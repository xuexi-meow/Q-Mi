// manager.go 实现账号级串行活动循环；新消息在请求边界追加，finish 暂停活动。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
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
	Wake       string // 唤醒类型：WakeChat / WakeBackground（空 = chat）
	ChatType   string // "private" / "group"
	ChatID     int64  // 群号；私聊 = 对方 QQ
	SelfID     int64  // 机器人自身 QQ
	SenderID   int64
	SenderName string
	Text       string
	Images     []imageRef // 消息里的图片段（P2.7 多模态，wiring 提取）
	Time       time.Time
	Urgent     bool // 私聊/群@标记；不再取消正在进行的请求
	Mentioned  bool // 真实 @自己/@全体消息段，勿扰例外
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

	// registry 工具注册表（模块启停会整体重建）；tasks 后台任务管理器；
	// approvals 越界/危险操作审批管理器；browser 浏览器单实例（懒启动）；
	// pool 模型候选池（chat 必有；background/video 按配置）。
	registry     *ToolRegistry
	tasks        *TaskManager
	approvals    *ApprovalManager
	browser      *browserManager
	legacyClient *Client

	events *EventLog // W1 WebUI 思考链事件环形缓冲（spec §3.1）

	cfgMu        sync.RWMutex // 保护 cfg（热重载整体替换）
	registryMu   sync.RWMutex // 保护 registry（模块启停重建）
	poolMu       sync.RWMutex // 保护 pool（候选选择/failover）
	pool         *modelPool
	usageMu      sync.Mutex // 保护按候选用量统计
	usageByModel map[string]*UsageSnapshot

	mu           sync.Mutex // 保护账号收件箱
	inbox        *accountInbox
	runMu        sync.Mutex // 串行化活动与工作上下文
	wakeMu       sync.Mutex // 保护轮询 goroutine（热重载换间隔时重启）
	wakeStop     chan struct{}
	conversation workingConversation
	usage        UsageSnapshot
}

// NewManager 构造 Manager。store 可为 nil（无历史/查询工具报错）。
// selfID 为机器人自身 QQ（配置未填传 0：P3.6 定时轮询在 selfID<=0 时停用）。
// P3.5：构造后接上 TimerStore（fire 回调引用 m.Enqueue，须先有 manager
// 再造 store，避免循环依赖），随即恢复上次进程未触发的计时。
func NewManager(builder ContextBuilder, client *Client, cfg config.AgentConfig, store *storage.Store, selfID int64) *Manager {
	m := &Manager{
		builder:      builder,
		legacyClient: client,
		cfg:          cfg,
		store:        store,
		selfID:       selfID,
		debugDir:     "agent/debug",
		notesPath:    defaultNotesPath,
		statePath:    defaultStatePath,
		events:       NewEventLog(),
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
	m.pool = buildPool(cfg, client)
	m.timerStore = NewTimerStore(defaultTimersPath, m.fireTimer)
	m.timerStore.LoadPending() // 重启恢复：文件缺失/损坏时静默返回 0
	return m
}

// config 返回当前配置快照（热重载后为最新值）。返回值只读，不得修改。
func (m *Manager) config() config.AgentConfig {
	if m == nil {
		return config.AgentConfig{}
	}
	m.cfgMu.RLock()
	defer m.cfgMu.RUnlock()
	return m.cfg
}

// toolRegistry 返回当前工具注册表（模块热重载可能整体替换）。
func (m *Manager) toolRegistry() *ToolRegistry {
	if m == nil {
		return nil
	}
	m.registryMu.RLock()
	defer m.registryMu.RUnlock()
	return m.registry
}

// ApplyConfig 应用一份新配置：先整体替换快照，再按变更类型做副作用
// （模型池重建、工具注册表重建、builder 参数更新、轮询重启）。
// 需重启才能生效的字段不在此处理，由调用方比对 Diff 后提示。
func (m *Manager) ApplyConfig(next config.AgentConfig) {
	m.cfgMu.Lock()
	old := m.cfg
	m.cfg = next
	m.cfgMu.Unlock()

	// 工具板块变更：重建注册表并重新生成工具文档（system 静态区一次 miss）。
	if !reflect.DeepEqual(old.Modules, next.Modules) {
		registry := NewRegistry(enabledModules(next)...)
		m.registryMu.Lock()
		m.registry = registry
		m.registryMu.Unlock()
		if b, ok := m.builder.(*DefaultBuilder); ok {
			b.SetToolDocs(registry.PromptDocs())
		}
	}

	// 模型接入变更：重建候选池；顶层三件套变了就换 legacy 客户端。
	endpointChanged := old.BaseURL != next.BaseURL || old.APIKey != next.APIKey
	if endpointChanged || !reflect.DeepEqual(old.Models, next.Models) {
		m.poolMu.Lock()
		if endpointChanged && next.BaseURL != "" {
			m.legacyClient = NewClient(next.BaseURL, next.APIKey, llmRequestTimeout)
		}
		m.pool = buildPool(next, m.legacyClient)
		m.poolMu.Unlock()
	}

	// 提示词路径、历史窗口、多模态等交给 builder 更新（正文仍每请求热读）。
	if b, ok := m.builder.(interface{ Apply(config.AgentConfig) }); ok {
		b.Apply(next)
	}

	// 轮询间隔变更：停旧 ticker 起新的（幂等）。
	if old.SelfWake != next.SelfWake {
		m.StartSelfWake(selfWakeInterval(next))
	}
}

// AttachConfigStore 订阅配置热重载：热字段立即应用，需重启字段只告警。
func (m *Manager) AttachConfigStore(store *config.Store) {
	if store == nil {
		return
	}
	store.Subscribe(func(old, next *config.Config) {
		if next == nil || next.Agent == nil {
			return
		}
		report := config.Diff(old, next)
		if !report.Changed() {
			return
		}
		for _, field := range report.Fields(config.LevelRestart) {
			slog.Warn("配置项已变更，需重启生效", "field", field)
		}
		m.ApplyConfig(*next.Agent)
		m.events.Record("", 0, KindConfig, report.Summary())
		slog.Info("agent 配置已热更新", "changes", report.Summary())
	})
}

// wakeFor 防抖窗口的唤醒类型合并：任一消息是 chat（或紧急）即整轮 chat
// （聊天质量优先），纯后台唤醒才降级到 background 档。
func wakeFor(pending []TurnMessage) string {
	for _, m := range pending {
		if m.Wake == WakeChat || m.Wake == "" {
			return WakeChat
		}
	}
	return WakeBackground
}

// deliverTaskResult 后台任务完成投递：格式化为合成消息入来源会话队列
// （空闲时防抖唤醒，忙时追加到下一次请求）。
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
		Wake:       WakeBackground,
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
	if a := app.GetApp(); a != nil {
		if cfg := a.Current(); cfg != nil && cfg.Bot != nil && len(cfg.Bot.Admin_Number) > 0 {
			return int64(cfg.Bot.Admin_Number[0])
		}
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
		Wake:       WakeBackground,
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

// modelPool 返回当前候选池（热重载会整体替换）。
func (m *Manager) modelPool() *modelPool {
	if m == nil {
		return nil
	}
	m.poolMu.RLock()
	defer m.poolMu.RUnlock()
	return m.pool
}

// chat 按能力选候选并发请求；可重试错误（网络/超时/429/5xx）自动换下一个
// 候选，最多 attempts 次。返回实际使用的候选与档位（日志/统计用）。
func (m *Manager) chat(ctx context.Context, turn *Turn, req ChatRequest, need modelNeed, attempts int) (*ChatResponse, *modelSlot, string, error) {
	pool := m.modelPool()
	if pool == nil {
		return nil, nil, "", errNoModel
	}
	if attempts <= 0 {
		attempts = defaultMaxAttempts
	}
	wake := ""
	if turn != nil {
		wake = turn.Wake
	}
	var lastErr error
	tierName := wake
	tried := make(map[string]bool, attempts)
	for attempt := 0; attempt < attempts; attempt++ {
		slot, tier, err := pool.pick(wake, need, tried)
		if err != nil && need.Vision {
			// 没有支持视觉的候选：降级为纯文本（图片替换成标注），不报错。
			fallback := need
			fallback.Vision = false
			slot, tier, err = pool.pick(wake, fallback, tried)
		}
		if err != nil {
			if lastErr != nil {
				return nil, nil, tier, lastErr
			}
			return nil, nil, tier, err
		}
		tierName = tier
		tried[slot.key()] = true
		req.Model = slot.model
		if need.Vision && !slot.supports("vision") {
			// 候选不支持视觉：图片降级为文字标注（不修改会话里的原消息）。
			req.Messages = stripImageParts(req.Messages)
		}
		resp, chatErr := slot.client.Chat(ctx, req)
		if chatErr == nil {
			slot.markSuccess()
			return resp, slot, tier, nil
		}
		lastErr = chatErr
		if !retryableError(chatErr) {
			return nil, slot, tier, chatErr
		}
		slot.markFailure(time.Now())
		slog.Warn("模型候选失败，切换下一个",
			"candidate", slot.name, "tier", tier, "attempt", attempt+1, "err", chatErr)
	}
	return nil, nil, tierName, lastErr
}

// fireTimer P3.5 定时器到点回调：把提醒合成为一条 IncomingMessage 入队
// （发送者「定时」、SenderID=SelfID、文本 [定时提醒] 前缀）。走正常防抖
// 排队，不打断正在进行的思考；非紧急。
func (m *Manager) fireTimer(st ScheduledTimer) {
	m.Enqueue(IncomingMessage{
		Wake:       WakeBackground,
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

// handleTurn 处理一个 turn（spec §4 思考循环）：
// 加载笔记/情绪 → 组装上下文 → 循环调 LLM：
//   - 响应含 tool_calls：逐个执行（finish 只标记不执行，同批其他工具先执行），
//     assistant + tool 消息回填后继续下一轮
//   - 纯文本响应：视为直接回复发出（兼容不会用工具的模型），结束
//   - finish：结束
//   - 轮数耗尽 / LLM 错误：强制结束，记日志
func (m *Manager) handleTurn(turn *Turn) {
	m.runMu.Lock()
	defer m.runMu.Unlock()
	ctx := context.Background()
	beforeUsage := m.usage
	defer func() {
		slog.Info("本次活动用量", "requests", m.usage.Requests-beforeUsage.Requests,
			"prompt", m.usage.PromptTokens-beforeUsage.PromptTokens,
			"output", m.usage.OutputTokens-beforeUsage.OutputTokens,
			"cache_hit", m.usage.CacheHit-beforeUsage.CacheHit,
			"cache_miss", m.usage.CacheMiss-beforeUsage.CacheMiss)
	}()
	defer func() {
		if r := recover(); r != nil {
			m.conversation = workingConversation{}
			slog.Error("agent worker panic，已恢复继续服务",
				"chat_type", turn.ChatType, "chat_id", turn.ChatID, "panic", r)
		}
	}()

	m.loadTurnState(turn)
	// Inputs stay pinned for this entire activity, including inserted arrivals.
	// Older wakes become eligible only after a new handleTurn starts.
	m.conversation.messages = append([]ChatMessage(nil), m.conversation.messages...)
	for i := range m.conversation.messages {
		if m.conversation.messages[i].kind == contextActiveInput {
			m.conversation.messages[i].kind = contextWork
		}
	}
	msgs := m.beginTurn(turn)
	counted := meaningfulTurn(turn)
	cfg := m.config()
	maxRounds := cfg.MaxToolRounds
	if maxRounds <= 0 {
		maxRounds = defaultMaxToolRounds
	}
	attempts := cfg.MaxAttempts
	if attempts <= 0 {
		attempts = defaultMaxAttempts
	}

	for round := 1; round <= maxRounds; round++ {
		var toolCtx *ToolContext
		var tools []Tool
		var defs []ToolDef
		for {
			turn = m.refreshLiveContext(turn)
			toolCtx = &ToolContext{
				Store: m.store, Turn: turn, Timer: m.timerStore, Images: m.images,
				Tasks: m.tasks, Approvals: m.approvals, Browser: m.browser,
				Events: m.events, Workspace: cfg.WorkspaceDir,
				NotesPath: m.notesPath, StatePath: m.statePath, Admin: batchIsAdmin(turn),
			}
			tools = m.toolRegistry().Build(toolCtx)
			defs = toolDefs(tools)
			revision := m.conversation.compactions
			if err := m.prepareContext(ctx, turn, defs); err != nil {
				m.events.Record(turn.ChatType, turn.ChatID, KindError, err.Error())
				slog.Warn("上下文预算不足，暂停活动", "err", err)
				return
			}
			if next := m.takePending(); next != nil {
				turn = next
				m.loadTurnState(turn)
				m.beginTurn(turn)
				counted = meaningfulTurn(turn)
				continue
			}
			// A summary request may have taken time: refresh files and arrivals
			// again before issuing the next action request.
			if revision == m.conversation.compactions {
				break
			}
		}
		msgs = m.conversation.messages
		req := ChatRequest{
			Model:       cfg.Model,
			Messages:    msgs,
			Temperature: cfg.Temperature,
			Tools:       defs,
		}
		need := needForMessages(msgs)
		resp, slot, tier, chatErr := m.chat(ctx, turn, req, need, attempts)
		if chatErr != nil {
			if cfg.Debug {
				m.dumpDebug(turn, req, round, nil, nil, chatErr, false)
			}
			m.events.Record(turn.ChatType, turn.ChatID, KindError, chatErr.Error())
			slog.Warn("LLM 请求失败，本轮思考结束",
				"chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round, "tier", tier, "err", chatErr)
			return
		}
		// 用量打点：缓存命中率掉坑 = 前缀被打断的信号（webui 概览有累计视图）。
		u := resp.Usage
		candidate := ""
		if slot != nil {
			candidate = slot.name
		}
		m.recordUsageFor(candidate, u)
		m.conversation.unreadStart = len(msgs)
		m.events.Record(turn.ChatType, turn.ChatID, KindUsage,
			fmt.Sprintf("hit=%d miss=%d out=%d candidate=%s tier=%s",
				u.PromptCacheHitTokens, u.PromptCacheMissTokens, u.CompletionTokens, candidate, tier))
		slog.Info("用量", "chat_type", turn.ChatType, "chat_id", turn.ChatID, "round", round, "tier", tier,
			"candidate", candidate, "cache_hit", u.PromptCacheHitTokens, "cache_miss", u.PromptCacheMissTokens,
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
			if content != "" && !counted {
				m.conversation.wakes++
			}
			if cfg.Debug {
				m.dumpDebug(turn, req, round, resp, nil, nil, false)
			}
			msgs = append(msgs, ChatMessage{
				Role: "assistant", Content: content,
				ReasoningContent: resp.ReplayReasoning(),
			})
			m.conversation.messages = msgs
			m.conversation.unreadStart = len(msgs)
			m.replyText(turn, content)
			return
		}
		if !counted {
			for _, call := range calls {
				if call.Function.Name != "finish" {
					m.conversation.wakes++
					counted = true
					break
				}
			}
		}

		msgs = append(msgs, ChatMessage{
			Role: "assistant", Content: resp.TextContent(), ToolCalls: calls,
			// 思考模式 + tools：该字段必须随消息回传，否则下一次请求 400。
			ReasoningContent: resp.ReplayReasoning(),
		})
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
				finished = true
				outcomes[i] = toolOutcome{executed: true, id: call.ID, result: "{}"}
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
				outcomes[i] = toolOutcome{executed: true, id: call.ID, result: truncateContent(result)}
			}(i, call)
		}
		wg.Wait()
		var results []string
		for _, oc := range outcomes {
			if !oc.executed {
				continue
			}
			results = append(results, oc.result)
			msgs = append(msgs, ChatMessage{Role: "tool", Content: oc.result, ToolCallID: oc.id})
		}
		// P3-4 图片按需上传：本轮工具（view_message_images）申请查看的图片，
		// 在工具执行完当轮追加为一条 user 多模态消息（下一轮 LLM 请求即可见），
		// 随后清空；同轮工具均已完成，后续由账号级 worker 串行读取。
		if len(turn.pendingImages) > 0 {
			parts := make([]ContentPart, 0, len(turn.pendingImages)+1)
			parts = append(parts, ContentPart{Type: "text", Text: "以下图片来自上一轮工具结果（查询自动附带或你主动查看）"})
			for _, du := range turn.pendingImages {
				parts = append(parts, imagePart(du))
			}
			msgs = append(msgs, ChatMessage{Role: "user", Content: parts})
			turn.pendingImages = nil
		}
		if cfg.Debug {
			m.dumpDebug(turn, req, round, resp, results, nil, false)
		}
		m.conversation.messages = msgs
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
	if (turn.ChatType != "group" && turn.ChatType != "private") || turn.ChatID <= 0 {
		m.events.Record(turn.ChatType, turn.ChatID, KindError, "混合来源没有默认回复目标，请使用 send_message 明确指定会话")
		return
	}
	if err := validateOutgoingText(content); err != nil {
		m.events.Record(turn.ChatType, turn.ChatID, KindError, err.Error())
		slog.Warn("回复正文格式错误，不发送",
			"chat_type", turn.ChatType, "chat_id", turn.ChatID, "err", err)
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
// 直接可回放对比。Interrupted 保留以兼容旧 debug 文件，新消息不再触发它。
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

// llmRequestTimeout 单次 LLM 请求超时（defaultNewManager 构造客户端用同一值）。
const llmRequestTimeout = 120 * time.Second
