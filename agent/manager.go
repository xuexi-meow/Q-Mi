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

	agentcontext "Q-Mi/agent/context"
	"Q-Mi/agent/model"
	agentruntime "Q-Mi/agent/runtime"
	"Q-Mi/app"
	"Q-Mi/bot"
	"Q-Mi/chain"
	"Q-Mi/storage"
	"Q-Mi/utils/config"
)

// IncomingMessage 事件接线层投给 Manager 的一条原始消息。
type IncomingMessage = agentruntime.IncomingMessage

// defaultDebounce 防抖窗口默认值（cfg.Debounce 非正时兜底）。
const defaultDebounce = 3 * time.Second

// defaultMaxAttempts 单次请求最多尝试的候选数（含首个）。
const defaultMaxAttempts = 2

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
	pool         *model.Pool
	usageMu      sync.Mutex // 保护按候选用量统计
	usageByModel map[string]*UsageSnapshot

	mu                sync.Mutex // 保护账号收件箱
	inbox             *accountInbox
	runMu             sync.Mutex // 串行化活动与工作上下文
	wakeMu            sync.Mutex // 保护轮询 goroutine（热重载换间隔时重启）
	wakeStop          chan struct{}
	conversation      workingConversation
	contextTurn       *Turn // 仅由 runMu 保护，恢复活动时不重新插入入站消息。
	usage             UsageSnapshot
	inspectionMu      sync.RWMutex
	inspectionRuntime RuntimeInspection
	inspectionRequest *ContextSnapshot
	inspectionWorking *ContextSnapshot
	inspectionVersion uint64
	sleepMu           sync.Mutex
	sleepStore        *agentruntime.SleepStore
	sleepTimer        *time.Timer
	sleepCancel       context.CancelFunc
	sleepWakeID       string
	sleepStopped      bool
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
	if b, ok := builder.(interface {
		SetHistoryAttention(agentcontext.HistoryAttention)
	}); ok {
		b.SetHistoryAttention(recentAttention)
	}
	// Builders that own image resolution share that cache with image tools.
	if b, ok := builder.(interface{ Images() *imageStore }); ok && b.Images() != nil {
		m.images = b.Images()
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
		if b, ok := m.builder.(interface{ SetToolDocs(string) }); ok {
			b.SetToolDocs(registry.PromptDocs())
		}
	}

	// 模型接入变更：重建候选池；顶层三件套变了就换 legacy 客户端。
	endpointChanged := old.BaseURL != next.BaseURL || old.APIKey != next.APIKey
	if endpointChanged || old.Model != next.Model || old.ToolChoice != next.ToolChoice || !reflect.DeepEqual(old.Models, next.Models) || !reflect.DeepEqual(old.WakeRoutes, next.WakeRoutes) {
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
	return agentruntime.WakeFor(pending)
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
func (m *Manager) modelPool() *model.Pool {
	if m == nil {
		return nil
	}
	m.poolMu.RLock()
	defer m.poolMu.RUnlock()
	return m.pool
}

// chat 按能力选候选并发请求；可重试错误（网络/超时/429/5xx）自动换下一个
// 候选，最多 attempts 次。返回实际使用的候选与档位（日志/统计用）。
func (m *Manager) chat(ctx context.Context, turn *Turn, req ChatRequest, need model.Need, attempts int) (*ChatResponse, *model.Candidate, string, error) {
	return m.chatWithWorking(ctx, turn, req, nil, need, attempts, 0)
}

func (m *Manager) chatWithWorking(ctx context.Context, turn *Turn, req ChatRequest, working []agentcontext.Message, need model.Need, attempts, round int) (*ChatResponse, *model.Candidate, string, error) {
	pool := m.modelPool()
	if pool == nil {
		return nil, nil, "", model.ErrNoModel
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
		slot, tier, err := pool.Pick(wake, need, tried)
		if err != nil && need.Vision {
			// 没有支持视觉的候选：降级为纯文本（图片替换成标注），不报错。
			fallback := need
			fallback.Vision = false
			slot, tier, err = pool.Pick(wake, fallback, tried)
		}
		if err != nil {
			if lastErr != nil {
				return nil, nil, tier, lastErr
			}
			return nil, nil, tier, err
		}
		tierName = tier
		tried[slot.Key()] = true
		req.Model = slot.Model()
		if len(req.Tools) > 0 {
			req.ToolChoice = slot.ToolChoice()
		}
		if need.Vision && !slot.Supports("vision") {
			// 候选不支持视觉：图片降级为文字标注（不修改会话里的原消息）。
			req.Messages = stripImageParts(req.Messages)
		}
		if working != nil {
			working = append([]agentcontext.Message(nil), working...)
			for i := range working {
				if i < len(req.Messages) {
					working[i].Wire = req.Messages[i]
				}
			}
		}
		phase := "summarizing"
		if len(req.Tools) > 0 {
			phase = "requesting"
			if working == nil {
				m.publishInspection("request", req.Messages, req.Tools)
			} else {
				m.publishContextInspection("request", working, req.Tools)
			}
		}
		m.setInspectionPhase(phase, 0, slot.Name(), slot.Model(), tier)
		var runID uint64
		if turn != nil {
			runID = m.events.BeginRun(turn.ChatType, turn.ChatID, round, slot.Name(), slot.Model())
		}
		onEvent := func(event StreamEvent) {
			if runID == 0 {
				return
			}
			calls := make([]RunToolCall, 0, len(event.ToolCallDeltas))
			received := int64(len(event.ContentDelta) + len(event.ReasoningDelta))
			for _, delta := range event.ToolCallDeltas {
				calls = append(calls, RunToolCall{
					Index: delta.Index, ID: delta.ID, Type: delta.Type,
					Name: delta.Name, Arguments: delta.Arguments,
				})
				received += int64(len(delta.ID) + len(delta.Name) + len(delta.Arguments))
			}
			m.events.UpdateRun(runID, RunUpdate{
				ReasoningDelta: event.ReasoningDelta, ContentDelta: event.ContentDelta,
				ToolCalls: calls, ReceivedBytes: received,
			})
		}
		resp, chatErr := chatWithModelClientStream(ctx, slot.Client(), req, onEvent)
		if isImageSizeLimitError(chatErr) && need.Vision {
			slog.Warn("模型拒绝图片载荷，改用纯文本重试",
				"candidate", slot.Name(), "round", round, "err", chatErr)
			req.Messages = stripImageParts(req.Messages)
			if working != nil {
				for i := range working {
					if i < len(req.Messages) {
						working[i].Wire = req.Messages[i]
					}
				}
				if len(req.Tools) > 0 {
					m.publishContextInspection("request", working, req.Tools)
				}
			} else if len(req.Tools) > 0 {
				m.publishInspection("request", req.Messages, req.Tools)
			}
			resp, chatErr = chatWithModelClientStream(ctx, slot.Client(), req, onEvent)
		}
		if chatErr == nil {
			m.events.EndRun(runID, "completed", "")
			m.inspectionMu.Lock()
			m.inspectionRuntime.LastUsage = resp.Usage
			m.inspectionMu.Unlock()
			slot.MarkSuccess()
			return resp, slot, tier, nil
		}
		m.events.EndRun(runID, "failed", chatErr.Error())
		lastErr = chatErr
		if !model.IsRetryableError(chatErr) {
			return nil, slot, tier, chatErr
		}
		slot.MarkFailure(time.Now())
		slog.Warn("模型候选失败，切换下一个",
			"candidate", slot.Name(), "tier", tier, "attempt", attempt+1, "err", chatErr)
	}
	return nil, nil, tierName, lastErr
}

func isImageSizeLimitError(err error) bool {
	var httpErr *model.HTTPError
	return errors.As(err, &httpErr) && httpErr.Status == 400 &&
		strings.Contains(strings.ToLower(httpErr.Body), "over image size limit")
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
//   - 纯文本响应：只保留为内部工作，反馈协议错误，不对外发送
//   - finish：结束
//   - 轮数耗尽 / LLM 错误：强制结束，记日志
func (m *Manager) handleTurn(turn *Turn) {
	m.runMu.Lock()
	defer m.runMu.Unlock()
	kept := make([]TurnMessage, 0, len(turn.NewMessages))
	for _, message := range turn.NewMessages {
		if !m.sleepGate(IncomingMessage{
			MessageID: message.MessageID, SelfID: turn.SelfID, ChatType: message.ChatType, ChatID: message.ChatID,
			SenderID: message.SenderID, SenderName: message.SenderName, Text: message.Text, Wake: message.Wake,
			Segments: message.Segments, Images: message.Images, Time: message.Time, Mentioned: message.Mentioned,
		}) {
			kept = append(kept, message)
		}
	}
	if len(turn.NewMessages) > 0 && len(kept) == 0 {
		return
	}
	turn.NewMessages = kept
	m.runActivity(turn, true)
}

// runActivity 的调用方持有 runMu；恢复时沿用原有工作记录。
func (m *Manager) runActivity(turn *Turn, incoming bool) {
	m.setInspectionPhase("preparing", 0, "", "", "")
	var inspectionErr error
	var inspectionDefs []ToolDef
	defer func() {
		m.publishContextInspection("working", m.conversation.Messages, inspectionDefs)
		m.finishInspection(inspectionErr)
	}()
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
			inspectionErr = fmt.Errorf("worker panic: %v", r)
			m.conversation = workingConversation{}
			slog.Error("agent worker panic，已恢复继续服务",
				"chat_type", turn.ChatType, "chat_id", turn.ChatID, "panic", r)
		}
	}()

	m.loadTurnState(turn)
	// Inputs stay pinned for this entire activity, including inserted arrivals.
	// Older wakes become eligible only after a new handleTurn starts.
	if incoming {
		m.conversation.BeginActivity()
		m.beginTurn(turn)
	}
	m.rememberContextTurn(turn)
	driver := &activityDriver{manager: m, config: m.config()}
	loop := &agentruntime.Loop{
		Conversation: &m.conversation, Driver: driver, Events: m.events,
		MaxRounds: driver.config.MaxToolRounds,
	}
	inspectionErr = loop.Run(ctx, turn)
	inspectionDefs = driver.defs
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
const llmRequestTimeout = model.DefaultRequestTimeout
