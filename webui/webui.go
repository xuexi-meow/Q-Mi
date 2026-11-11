// Package webui Q-Mi Web 控制台（W1，spec 2026-11-07 §2/§4/§5）：
// 独立 HTTP server（与 OneBot 上报端口隔离）、go:embed 静态资源、
// Bearer token 鉴权、/api/overview 概览。Start 非阻塞，由调用方 Shutdown。
package webui

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"Q-Mi/agent"
	"Q-Mi/api"
	"Q-Mi/app"
	"Q-Mi/storage"
	"Q-Mi/utils/config"
	"Q-Mi/utils/request"
)

// Config 供外部引用的配置类型别名。
type Config = config.WebUIConfig

//go:embed static
var staticFS embed.FS

// startTime 进程内 WebUI 包加载时间，作为运行时长起点。
var startTime = time.Now()

// napcatProbeTimeout NapCat 状态探测超时（不可达时快速放弃，不拖住概览）。
const napcatProbeTimeout = 3 * time.Second

// storeProvider 记忆库获取接缝：app.GetApp().Store（可能为 nil = 未启用）。
// 变量形式便于测试注入。
var storeProvider = func() *storage.Store {
	a := app.GetApp()
	if a == nil {
		return nil
	}
	return a.Store
}

// Start 启动 WebUI HTTP server：net.Listen 成功即返回 *http.Server（非阻塞），
// 关闭由调用方对返回值调 Shutdown。
func Start(cfg config.WebUIConfig) (*http.Server, error) {
	mux := http.NewServeMux()

	// 静态资源：无鉴权但无敏感内容（spec §6）
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		index, err := staticFS.ReadFile("static/index.html")
		if err != nil {
			http.Error(w, "index missing", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(index)
	})
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("内嵌静态资源不可用: %w", err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))

	// /api/ 全部走 Bearer 鉴权（spec §2）
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r, cfg.Token) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="qmi"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/api/runtime":
			handleRuntime(w, r)
		case "/api/context":
			handleContext(w, r)
		case "/api/context/message":
			handleContextEntry(w, r)
		case "/api/tasks":
			handleTasks(w, r)
		case "/api/overview":
			handleOverview(w, r)
		case "/api/chats":
			handleChats(w, r)
		case "/api/messages":
			handleMessages(w, r)
		case "/api/search":
			handleSearch(w, r)
		case "/api/events":
			handleEvents(w, r)
		case "/api/notes":
			handleNotes(w, r)
		case "/api/mood":
			handleMood(w, r)
		case "/api/timers":
			handleTimers(w, r)
		case "/api/wake":
			handleWake(w, r)
		case "/api/file":
			handleFile(w, r)
		case "/api/friends":
			handleFriends(w, r)
		case "/api/groups":
			handleGroups(w, r)
		case "/api/members":
			handleMembers(w, r)
		case "/api/napcat":
			handleNapcat(w, r)
		case "/api/config":
			if r.Method == http.MethodPut {
				handleConfigPut(w, r)
			} else {
				handleConfigGet(w, r)
			}
		case "/api/config/validate":
			handleConfigValidate(w, r)
		case "/api/config/reload":
			handleConfigReload(w, r)
		case "/api/models/test":
			handleModelTest(w, r)
		default:
			switch {
			case strings.HasPrefix(r.URL.Path, "/api/notes/"):
				handleNoteDelete(w, r)
			case strings.HasPrefix(r.URL.Path, "/api/timers/"):
				handleTimerCancel(w, r)
			default:
				http.NotFound(w, r)
			}
		}
	})

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("WebUI 监听 %s 失败: %w", addr, err)
	}
	srv := &http.Server{
		Addr:              listener.Addr().String(),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("WebUI 服务器错误", "err", err)
		}
	}()
	slog.Info("WebUI 已启动", "addr", srv.Addr)
	return srv, nil
}

// authorized Bearer token 校验：常数时间比较；token 未配置时拒绝一切 API。
func authorized(r *http.Request, token string) bool {
	if token == "" {
		return false
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	return subtleEqual(got, token)
}

// subtleEqual 常数时间字符串比较。
func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// napcatStatus 概览的 NapCat 卡片字段；不可达时零值不报错。
type napcatStatus struct {
	Online  bool   `json:"online"`
	Version string `json:"version"`
}

// overview /api/overview 响应。messages/notices 为 -1 表示记忆库不可用。
type overview struct {
	Napcat        napcatStatus                   `json:"napcat"`
	UptimeSeconds int64                          `json:"uptime_seconds"`
	Messages      int64                          `json:"messages"`
	Notices       int64                          `json:"notices"`
	Mood          string                         `json:"mood"`
	NotesCount    int                            `json:"notes_count"`
	TimersCount   int                            `json:"timers_count"`
	Usage         agent.UsageSnapshot            `json:"usage"`
	UsageByModel  map[string]agent.UsageSnapshot `json:"usage_by_model"`
}

// handleOverview 并行收集 NapCat 状态/版本、storage 统计、mood/notes/timers
// 计数与运行时长；NapCat 不可达时字段置空不报错（spec §4）。
func handleOverview(w http.ResponseWriter, r *http.Request) {
	ov := overview{
		UptimeSeconds: int64(time.Since(startTime).Seconds()),
		Mood:          agent.MoodGet(),
		NotesCount:    len(agent.NotesList()),
		TimersCount:   agent.TimerCount(),
		Usage:         agent.UsageTotals(),
		UsageByModel:  agent.UsageByModelTotals(),
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // NapCat get_status + get_version_info
		defer wg.Done()
		ov.Napcat = probeNapcat(r)
	}()
	wg.Add(1)
	go func() { // storage 统计（store 可为 nil）
		defer wg.Done()
		if s := storeProvider(); s != nil {
			if msgs, notices, err := s.Stats(); err == nil {
				ov.Messages, ov.Notices = msgs, notices
			}
		} else {
			ov.Messages, ov.Notices = -1, -1
		}
	}()
	wg.Wait()

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(ov); err != nil {
		slog.Warn("WebUI 概览序列化失败", "err", err)
	}
}

// probeNapcat 探测 NapCat：任一调用失败（未初始化/不可达/retcode 非 0）即
// 返回零值状态，不影响概览其余字段。
func probeNapcat(r *http.Request) napcatStatus {
	ctx, cancel := context.WithTimeout(r.Context(), napcatProbeTimeout)
	defer cancel()

	var st napcatStatus
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if resp, err := request.Call(ctx, &api.GetStatus{}); err == nil {
			var data struct {
				Online bool `json:"online"`
			}
			if json.Unmarshal(resp.Data, &data) == nil {
				st.Online = data.Online
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		if resp, err := request.Call(ctx, &api.GetVersionInfo{}); err == nil {
			var data struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(resp.Data, &data) == nil {
				st.Version = data.Version
			}
		}
	}()
	wg.Wait()
	return st
}

// messageJSON /api/messages 与 /api/search 响应的消息行形态（spec §4）。
// direction/user_id 供前端 recv/sent 区分与「昵称(QQ号)」展示。
type messageJSON struct {
	MessageID       int64            `json:"message_id"`
	ChatType        string           `json:"chat_type"`
	ChatID          int64            `json:"chat_id"`
	Direction       string           `json:"direction"`
	UserID          int64            `json:"user_id"`
	SenderName      string           `json:"sender_name"`
	Text            string           `json:"text"`
	RawMessage      string           `json:"raw_message"`
	Time            float64          `json:"time"`
	Recalled        bool             `json:"recalled"`
	Mentions        []string         `json:"mentions"`
	AddressingKnown bool             `json:"addressing_known"`
	Segments        []messageSegment `json:"segments"`
	Reply           map[string]any   `json:"reply,omitempty"`
}

// cqImageRe 匹配 [CQ:image,...] 码（值内逗号转义为 &#44;，不含 ]）。
// 与 agent/context.go 的 replaceImageCQ 同规则（该函数未导出，包内复制）。
var cqImageRe = regexp.MustCompile(`\[CQ:image,[^\]]*\]`)

// toMessageJSON MessageRecord 转 JSON 行；text 为展示文本（[CQ:image,...]
// 码替换为 [图片]，其余 CQ 码原样），raw_message 保留原始内容。
func toMessageJSON(r *storage.MessageRecord) messageJSON {
	row := messageJSON{
		MessageID: r.MessageID, ChatType: r.ChatType, ChatID: r.ChatID,
		Direction: r.Direction, UserID: r.UserID,
		SenderName: r.SenderName, Text: cqImageRe.ReplaceAllString(r.RawMessage, "[图片]"),
		RawMessage: r.RawMessage, Time: r.Time, Recalled: r.Recalled,
	}
	addMessageMetadata(&row, r)
	return row
}

// queryInt 解析整型查询参数：缺失/非法/非正数用默认值，超出上限截到 max。
func queryInt(r *http.Request, key string, def, max int) int {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return def
	}
	if v > max {
		return max
	}
	return v
}

// writeJSON 序列化响应；编码失败只记日志（响应头已发出）。
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("WebUI 响应序列化失败", "err", err)
	}
}

// handleChats GET /api/chats 会话摘要列表；store 未启用返回空数组。
func handleChats(w http.ResponseWriter, r *http.Request) {
	sums := []storage.ChatSummary{}
	if s := storeProvider(); s != nil {
		out, err := s.ChatSummaries()
		if err != nil {
			slog.Warn("WebUI 会话列表查询失败", "err", err)
			http.Error(w, "查询会话列表失败", http.StatusInternalServerError)
			return
		}
		sums = out
	}
	writeJSON(w, sums)
}

// handleMessages GET /api/messages?chat_type&chat_id&limit&before_time
// 分页查询某会话消息（新→旧）；before_time 为可选游标（只取 time < 该值）。
func handleMessages(w http.ResponseWriter, r *http.Request) {
	chatType := r.URL.Query().Get("chat_type")
	chatIDStr := r.URL.Query().Get("chat_id")
	chatID, err := strconv.ParseInt(chatIDStr, 10, 64)
	if chatType == "" || chatIDStr == "" || err != nil || chatID == 0 {
		http.Error(w, "chat_type 与 chat_id 为必填参数", http.StatusBadRequest)
		return
	}
	f := storage.MessageFilter{
		ChatType: chatType,
		ChatID:   chatID,
		Limit:    queryInt(r, "limit", 50, 200),
	}
	if b := r.URL.Query().Get("before_time"); b != "" {
		before, err := strconv.ParseFloat(b, 64)
		if err != nil {
			http.Error(w, "before_time 应为 unix 秒数字", http.StatusBadRequest)
			return
		}
		if before > 0 {
			f.Until = time.UnixMilli(int64(before * 1000))
		}
	}
	s := storeProvider()
	if s == nil {
		writeJSON(w, []messageJSON{})
		return
	}
	var rows []*storage.MessageRecord
	if around := r.URL.Query().Get("around_message_id"); around != "" {
		id, parseErr := strconv.ParseInt(around, 10, 64)
		if parseErr != nil || id == 0 || r.URL.Query().Get("before_time") != "" {
			http.Error(w, "around_message_id 无效或与 before_time 冲突", 400)
			return
		}
		rows, err = s.MessageWindow(chatType, chatID, id, f.Limit)
		if errors.Is(err, storage.ErrMessageAnchor) {
			http.Error(w, "消息不在该会话中", 404)
			return
		}
	} else {
		rows, err = s.QueryMessages(f)
	}
	if err != nil {
		slog.Warn("WebUI 消息查询失败", "err", err)
		http.Error(w, "查询消息失败", http.StatusInternalServerError)
		return
	}
	out := make([]messageJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, toMessageJSON(row))
	}
	writeJSON(w, out)
}

// searchRequest POST /api/search 请求体。
type searchRequest struct {
	Query    string `json:"query"`
	ChatType string `json:"chat_type"`
	ChatID   int64  `json:"chat_id"`
	Limit    int    `json:"limit"`
}

// handleSearch POST /api/search 全文搜索；body 缺失/非法或 query 为空 → 400。
func handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req searchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体应为 JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Query) == "" {
		http.Error(w, "query 不能为空", http.StatusBadRequest)
		return
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	f := storage.MessageFilter{ChatType: req.ChatType, ChatID: req.ChatID, Limit: limit}
	s := storeProvider()
	if s == nil {
		writeJSON(w, []messageJSON{})
		return
	}
	rows, err := s.SearchMessages(req.Query, f)
	if err != nil {
		slog.Warn("WebUI 搜索失败", "err", err)
		http.Error(w, "搜索失败", http.StatusInternalServerError)
		return
	}
	out := make([]messageJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, toMessageJSON(row))
	}
	writeJSON(w, out)
}

// handleEvents GET /api/events?after=N 思考链事件增量（agent 未启用为空数组）。
func handleEvents(w http.ResponseWriter, r *http.Request) {
	after := queryInt(r, "after", 0, int(^uint(0)>>1))
	events := agent.EventsAfter(uint64(after))
	out := make([]agent.Event, 0, len(events))
	out = append(out, events...)
	writeJSON(w, out)
}

// ================= W3 agent 控制页（spec §3.2/§4） =================

// maxTimerDelaySeconds WebUI 新增计时器的延迟上限（7 天），下限 1 秒。
const maxTimerDelaySeconds = 7 * 24 * 3600

// hookErrStatus 钩子错误的 HTTP 状态映射：agent 未启用/机器人 QQ 未配置是
// 服务端状态 → 503；其余（下标越界、计时器不存在等）属请求问题 → 400。
func hookErrStatus(err error) int {
	if errors.Is(err, agent.ErrAgentNotEnabled) || errors.Is(err, agent.ErrSelfIDMissing) {
		return http.StatusServiceUnavailable
	}
	return http.StatusBadRequest
}

// noteJSON /api/notes 列表行：index 为删除用的数组下标，time 为 unix 秒。
type noteJSON struct {
	Index int     `json:"index"`
	Time  float64 `json:"time"`
	Text  string  `json:"text"`
}

// handleNotes GET 列出思考笔记 / POST 新增（{text}，非空校验）。
func handleNotes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		notes := agent.NotesList()
		out := make([]noteJSON, 0, len(notes))
		for i, n := range notes {
			out = append(out, noteJSON{Index: i, Time: float64(n.Time.Unix()), Text: n.Text})
		}
		writeJSON(w, out)
	case http.MethodPost:
		var req struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "请求体应为 JSON", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Text) == "" {
			http.Error(w, "text 不能为空", http.StatusBadRequest)
			return
		}
		if err := agent.NoteAdd(req.Text); err != nil {
			http.Error(w, err.Error(), hookErrStatus(err))
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// handleNoteDelete DELETE /api/notes/{index} 按下标删除思考笔记。
func handleNoteDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/api/notes/"))
	if err != nil {
		http.Error(w, "下标应为整数", http.StatusBadRequest)
		return
	}
	if err := agent.NoteDelete(idx); err != nil {
		http.Error(w, err.Error(), hookErrStatus(err))
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleMood GET 读情绪 / POST 写情绪（{mood}）。
func handleMood(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, map[string]string{"mood": agent.MoodGet()})
	case http.MethodPost:
		var req struct {
			Mood string `json:"mood"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "请求体应为 JSON", http.StatusBadRequest)
			return
		}
		if err := agent.MoodSet(req.Mood); err != nil {
			http.Error(w, err.Error(), hookErrStatus(err))
			return
		}
		writeJSON(w, map[string]any{"ok": true})
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// handleTimers GET 列出待触发计时器 / POST 新增
// （{delay_seconds, note}，延迟校验 1..604800 秒）。
func handleTimers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list := agent.TimerList()
		if list == nil {
			list = []agent.ScheduledTimer{}
		}
		writeJSON(w, list)
	case http.MethodPost:
		var req struct {
			DelaySeconds int    `json:"delay_seconds"`
			Note         string `json:"note"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "请求体应为 JSON", http.StatusBadRequest)
			return
		}
		if req.DelaySeconds < 1 || req.DelaySeconds > maxTimerDelaySeconds {
			http.Error(w, "delay_seconds 应为 1..604800（秒）", http.StatusBadRequest)
			return
		}
		id, err := agent.TimerSchedule(req.DelaySeconds, req.Note)
		if err != nil {
			http.Error(w, err.Error(), hookErrStatus(err))
			return
		}
		writeJSON(w, map[string]string{"id": id})
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// handleTimerCancel DELETE /api/timers/{id} 取消一个未触发的计时器。
func handleTimerCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/timers/")
	if id == "" {
		http.Error(w, "缺少计时器 ID", http.StatusBadRequest)
		return
	}
	if err := agent.TimerCancel(id); err != nil {
		http.Error(w, err.Error(), hookErrStatus(err))
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleWake POST /api/wake {text} 手动唤醒（text 非空校验，注入 self 队列）。
func handleWake(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "请求体应为 JSON", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		http.Error(w, "text 不能为空", http.StatusBadRequest)
		return
	}
	if err := agent.Wake(req.Text); err != nil {
		http.Error(w, err.Error(), hookErrStatus(err))
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// fileWhitelist /api/file 可编辑文件白名单（spec §6：杜绝路径穿越），
// persona/guide 是 builder 构造时读取的提示词文件。
var fileWhitelist = map[string]string{
	"persona": "agent/persona.md",
	"guide":   "agent/guide.md",
}

// handleFile GET /api/file?name=persona|guide 读内容（未创建过返回空串）；
// PUT /api/file {name, content} 覆盖写（0600，父目录自动创建）。
// 白名单外一律 400。
func handleFile(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		name := r.URL.Query().Get("name")
		path, pathErr := promptFilePath(name)
		if pathErr != nil {
			http.Error(w, "name 仅支持 persona 或 guide", http.StatusBadRequest)
			return
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				slog.Warn("WebUI 读取文件失败", "path", path, "err", err)
				http.Error(w, "读取文件失败", http.StatusInternalServerError)
				return
			}
			data = nil // 尚未创建过：编辑器从空白开始
		}
		writeJSON(w, map[string]string{"content": string(data), "path": path})
	case http.MethodPut:
		var req struct {
			Name    string `json:"name"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "请求体应为 JSON", http.StatusBadRequest)
			return
		}
		path, pathErr := promptFilePath(req.Name)
		if pathErr != nil {
			http.Error(w, "name 仅支持 persona 或 guide", http.StatusBadRequest)
			return
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			slog.Warn("WebUI 创建文件目录失败", "dir", filepath.Dir(path), "err", err)
			http.Error(w, "创建目录失败", http.StatusInternalServerError)
			return
		}
		if err := os.WriteFile(path, []byte(req.Content), 0o600); err != nil {
			slog.Warn("WebUI 写文件失败", "path", path, "err", err)
			http.Error(w, "写入文件失败", http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]any{"ok": true, "effect": "next_request"})
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}

// ================= W4 QQ 管理页（spec §4：好友/群/成员/动作直调） =================

// napcatCallTimeout NapCat API 调用超时（服务端 WriteTimeout 15s 之内）。
const napcatCallTimeout = 10 * time.Second

// napcatFail NapCat 侧失败（未初始化/不可达/retcode 非 0）统一 502 +
// 简短 JSON 错误（不泄漏堆栈，spec §4：NapCat 不可达不 500）。
func napcatFail(w http.ResponseWriter, err error) {
	slog.Warn("WebUI 调用 NapCat 失败", "err", err)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusBadGateway)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// groupJSON /api/groups 响应条目（NapCat get_group_list data 的展示子集）。
type groupJSON struct {
	GroupID     int64  `json:"group_id"`
	GroupName   string `json:"group_name"`
	MemberCount int    `json:"member_count"`
}

// handleFriends GET /api/friends 好友列表（user_id/nickname/remark/level）。
func handleFriends(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), napcatCallTimeout)
	defer cancel()
	resp, err := request.GetFriendList(ctx, &api.GetFriendList{})
	if err != nil {
		napcatFail(w, err)
		return
	}
	writeJSON(w, resp.Data)
}

// handleGroups GET /api/groups 群列表（group_id/group_name/member_count）。
func handleGroups(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), napcatCallTimeout)
	defer cancel()
	resp, err := request.Do[[]groupJSON](ctx, &api.GetGroupList{})
	if err != nil {
		napcatFail(w, err)
		return
	}
	writeJSON(w, resp.Data)
}

// maxMembersReturned /api/members 单次返回的成员条数上限（与 agent
// lookup_members 同值，防一次性渲染过大列表）。
const maxMembersReturned = 100

// handleMembers GET /api/members?group_id= 群成员列表：超过 100 条截断并带
// total/returned/truncated 标记；group_id 缺失/非法 400（不触达 NapCat）。
func handleMembers(w http.ResponseWriter, r *http.Request) {
	gid, err := strconv.ParseInt(r.URL.Query().Get("group_id"), 10, 64)
	if err != nil || gid == 0 {
		http.Error(w, "group_id 为必填参数（非 0 整数）", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), napcatCallTimeout)
	defer cancel()
	resp, err := request.Do[[]map[string]any](ctx, &api.GetGroupMemberList{GroupID: gid})
	if err != nil {
		napcatFail(w, err)
		return
	}
	total := len(resp.Data)
	truncated := total > maxMembersReturned
	members := resp.Data
	if truncated {
		members = members[:maxMembersReturned]
	}
	writeJSON(w, map[string]any{
		"total": total, "returned": len(members), "truncated": truncated, "members": members,
	})
}

// dangerousNapcatActions /api/napcat 直调中需 params.confirm=true 确认的动作
// 集合。基础清单与 agent/tools.go 的 dangerousNapcatActions 同源（该集合未导出，
// 此处复制维护，见 napcatActionTypes 的重复维护说明）；P4 QQ 空间两项
// （set_qzone_ban / set_qzone_msg_right）只在 WebUI 侧要求显式确认——账号级
// 配置改动对人工界面应二次确认，agent 侧未列入管理员门槛。
var dangerousNapcatActions = map[string]bool{
	"set_group_kick":      true,
	"set_group_leave":     true,
	"delete_friend":       true,
	"set_qq_avatar":       true,
	"set_qq_profile":      true,
	"set_group_name":      true,
	"delete_qzone_msg":    true,
	"set_qzone_ban":       true,
	"set_qzone_msg_right": true,
}

// napcatActionRegistry 动作名（无 / 前缀）→ 请求结构体类型，与 agent 包
// napcat_api 逃生舱的 actionRegistry 同源（api 包结构体清单）。agent 侧
// 注册表未导出且本次不允许改 agent，故在 webui 复制 actionTypes 清单构建；
// api 包新增动作时两处需同步（agent 的注册表覆盖率有测试兜底，见报告）。
var napcatActionRegistry = buildNapcatRegistry(napcatActionTypes())

// buildNapcatRegistry 由动作实例列表构建名称 → 类型映射（与 agent 同规则）。
func buildNapcatRegistry(list []api.Request) map[string]reflect.Type {
	m := make(map[string]reflect.Type, len(list))
	for _, a := range list {
		name := strings.TrimPrefix(a.Path(), "/")
		if _, dup := m[name]; dup {
			panic("webui: 动作注册表路径重复 " + name)
		}
		m[name] = reflect.TypeOf(a).Elem()
	}
	return m
}

// napcatActionTypes 全量 NapCat 动作请求类型（api 包 196 个动作，与
// agent/tools.go 的 actionTypes 保持一致）。
func napcatActionTypes() []api.Request {
	return []api.Request{
		// api.go 已有
		&api.SendGroupMsg{}, &api.SendPrivateMsg{}, &api.GetMsg{}, &api.DeleteMsg{},

		// ---------------- 核心接口 ----------------
		&api.SetGroupTodo{}, &api.CompleteGroupTodo{}, &api.CancelGroupTodo{},
		&api.SendGroupPoke{}, &api.SendFriendPoke{}, &api.SendPoke{},

		// ---------------- 群组接口 ----------------
		&api.GetGroupDetailInfo{}, &api.GetGroupList{}, &api.GetGroupInfo{},
		&api.GetGroupMemberList{}, &api.GetGroupMemberInfo{},
		&api.SetGroupAddRequest{}, &api.SetGroupLeave{}, &api.SetGroupWholeBan{},
		&api.SetGroupBan{}, &api.SetGroupKick{}, &api.SetGroupAdmin{},
		&api.SetGroupName{}, &api.SetGroupCard{}, &api.GetEssenceMsgList{},
		&api.GetGroupIgnoredNotifies{}, &api.DeleteEssenceMsg{}, &api.GetGroupNotice{},
		&api.SetEssenceMsg{}, &api.DeleteGroupNotice{}, &api.GetGroupShutList{},
		&api.GetGroupIgnoreAddRequest{}, &api.SetGroupMemberInvitePolicy{},
		&api.SetGroupMemberPermissions{}, &api.SetGroupNewMemberHistoryVisibility{},
		&api.GetGroupHonorInfo{}, &api.GetGroupAtAllRemain{}, &api.SetGroupPortrait{},

		// ---------------- 群组扩展 ----------------
		&api.SetGroupAddOption{}, &api.SetGroupRobotAddOption{}, &api.SetGroupSearch{},
		&api.SetGroupRemark{}, &api.GetGroupInfoEx{}, &api.SetGroupSign{}, &api.SendGroupSign{},
		&api.GetGroupSignedList{}, &api.GetGroupShareLink{},

		// ---------------- 好友/用户 ----------------
		&api.SetFriendRemark{}, &api.SendLike{}, &api.GetFriendList{},
		&api.SetFriendAddRequest{}, &api.GetCookies{}, &api.GetRecentContact{},
		&api.GetFriendsWithCategory{}, &api.GetProfileLike{}, &api.SetDiyOnlineStatus{},
		&api.GetUnidirectionalFriendList{}, &api.DeleteFriend{},

		// ---------------- 消息接口 ----------------
		&api.SendMsg{}, &api.ForwardFriendSingleMsg{}, &api.ForwardGroupSingleMsg{},
		&api.MarkGroupMsgAsRead{}, &api.MarkPrivateMsgAsRead{}, &api.MarkMsgAsRead{},
		&api.MarkAllAsRead{},
		&api.SendForwardMsg{}, &api.SendGroupForwardMsg{}, &api.SendPrivateForwardMsg{},
		&api.GetForwardMsg{}, &api.GetGroupMsgHistory{}, &api.GetFriendMsgHistory{},

		// ---------------- 消息扩展 ----------------
		&api.FetchEmojiLike{}, &api.GetEmojiLikes{}, &api.FetchPttText{},
		&api.ArkShareGroup{}, &api.ArkSharePeer{}, &api.SendGroupArkShare{}, &api.SendArkShare{},
		&api.SetMsgEmojiLike{}, &api.ClickInlineKeyboardButton{},

		// ---------------- 文件接口 ----------------
		&api.GetFile{}, &api.GetImage{}, &api.GetRecord{},
		&api.GetGroupFileURL{}, &api.GetPrivateFileURL{},

		// ---------------- 文件扩展 ----------------
		&api.MoveGroupFile{}, &api.RenameGroupFile{}, &api.TransGroupFile{},
		&api.CreateFlashTask{}, &api.GetFlashFileList{}, &api.GetFlashFileURL{},
		&api.SendFlashMsg{}, &api.GetShareLink{}, &api.GetFilesetInfo{},
		&api.GetOnlineFileMsg{}, &api.SendOnlineFile{}, &api.SendOnlineFolder{},
		&api.ReceiveOnlineFile{}, &api.RefuseOnlineFile{}, &api.CancelOnlineFile{},
		&api.DownloadFileset{}, &api.GetFilesetID{},

		// ---------------- Go-CQHTTP 文件 ----------------
		&api.GetGroupRootFiles{}, &api.UploadGroupFile{}, &api.DeleteGroupFile{},
		&api.CreateGroupFileFolder{}, &api.DeleteGroupFolder{}, &api.GetGroupFileSystemInfo{},
		&api.GetGroupFilesByFolder{}, &api.DownloadFile{}, &api.UploadPrivateFile{},

		// ---------------- 系统接口 ----------------
		&api.GetLoginInfo{}, &api.GetVersionInfo{}, &api.CanSendRecord{}, &api.CanSendImage{},
		&api.GetStatus{}, &api.GetCsrfToken{}, &api.GetCredentials{}, &api.NcGetPacketStatus{},
		&api.SetRestart{}, &api.GetGroupSystemMsg{}, &api.CleanCache{},
		&api.SetDoubtFriendsAddRequest{}, &api.GetDoubtFriendsAddRequest{},

		// ---------------- 系统扩展 ----------------
		&api.GetRkey{}, &api.GetRkeyServer{}, &api.SetOnlineStatus{}, &api.GetRobotUinRange{},
		&api.FetchCustomFace{}, &api.FetchCustomFaceDetail{}, &api.AddCustomFace{},
		&api.DeleteCustomFace{}, &api.SetCustomFaceDesc{}, &api.SetInputStatus{},
		&api.NcGetUserStatus{}, &api.NcGetRkey{}, &api.GetMiniAppArk{}, &api.SendPacket{},
		&api.BotExit{}, &api.GetCollectionList{},

		// ---------------- Go-CQHTTP 其他 ----------------
		&api.SetQQProfile{}, &api.CheckURLSafely{}, &api.GetOnlineClients{},
		&api.GetStrangerInfo{}, &api.GetModelShow{}, &api.SetModelShow{},
		&api.SendGroupNotice{}, &api.HandleQuickOperation{},

		// ---------------- 扩展接口 ----------------
		&api.SetGroupKickMembers{}, &api.CreateCollection{}, &api.SetSelfLongNick{},
		&api.SetQQAvatar{}, &api.TranslateEn2Zh{}, &api.GetClientKey{},
		&api.SendQzoneMsg{}, &api.OCRImage{}, &api.DeleteQzoneMsg{}, &api.OCRImageInternal{},
		&api.SetGroupSpecialTitle{}, &api.GetAICharacters{},
		&api.GetQzoneFeeds{}, &api.GetQzoneMsgList{}, &api.GetQzoneMsgDetail{},
		&api.GetQzoneCommentList{}, &api.GetQzoneCommentReplies{},
		&api.LikeQzone{}, &api.UnlikeQzone{}, &api.CommentQzone{},
		&api.GetQzoneProfile{}, &api.SetQzoneProfile{}, &api.SetQzoneMsgRight{}, &api.SetQzoneBan{},

		// ---------------- AI 扩展 ----------------
		&api.GetAIRecord{}, &api.SendGroupAIRecord{},

		// ---------------- QQ空间扩展(空友爱看) ----------------
		&api.GetQzonePublicCapabilities{}, &api.GetQzonePublicFeeds{},
		&api.GetQzonePublicFeedDetail{}, &api.GetQzonePublicComments{},
		&api.GetQzonePublicReplies{}, &api.SetQzonePublicUpvote{}, &api.SetQzonePublicLike{},
		&api.SetQzonePublicFollow{}, &api.SetQzonePublicCommentLike{},
		&api.CommentQzonePublicFeed{}, &api.ReplyQzonePublicFeed{},
		&api.DeleteQzonePublicComment{}, &api.DeleteQzonePublicReply{},
		&api.ForwardQzonePublicFeed{},

		// ---------------- 群相册 ----------------
		&api.DelGroupAlbumMedia{}, &api.SetGroupAlbumMediaLike{},
		&api.CancelGroupAlbumMediaLike{}, &api.DoGroupAlbumComment{},
		&api.GetGroupAlbumMediaList{}, &api.GetQunAlbumList{},
		&api.UploadImageToQunAlbum{}, &api.UploadImagesToQunAlbum{}, &api.UploadVideoToQunAlbum{},
	}
}

// handleNapcat POST /api/napcat {action, params} 动作直调：
//   - action 必须在白名单（napcatActionRegistry，接受带 / 前缀）内，否则 400；
//   - 危险动作（dangerousNapcatActions）要求 params.confirm=true 才执行，confirm
//     剥离后不透传给 NapCat，缺失/非 true 一律 400——WebUI token 持有者即管理员，
//     采用显式 confirm 开关而非 403 拒绝，人工界面体验更好；
//   - NapCat 未初始化/不可达/retcode 非 0 → 502 简短 JSON（napcatFail）。
//
// GET 返回 {actions, dangerous} 白名单全集（前端动作下拉用）。
func handleNapcat(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		names := make([]string, 0, len(napcatActionRegistry))
		for name := range napcatActionRegistry {
			names = append(names, name)
		}
		sort.Strings(names)
		danger := make([]string, 0, len(dangerousNapcatActions))
		for name := range dangerousNapcatActions {
			danger = append(danger, name)
		}
		sort.Strings(danger)
		writeJSON(w, map[string]any{"actions": names, "dangerous": danger})
	case http.MethodPost:
		var req struct {
			Action string          `json:"action"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "请求体应为 JSON", http.StatusBadRequest)
			return
		}
		name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(req.Action), "/"))
		typ, ok := napcatActionRegistry[name]
		if !ok {
			http.Error(w, fmt.Sprintf("未知动作 %q，见 GET /api/napcat 白名单", req.Action), http.StatusBadRequest)
			return
		}
		params := req.Params
		if dangerousNapcatActions[name] {
			fields := map[string]json.RawMessage{}
			if len(params) > 0 {
				if err := json.Unmarshal(params, &fields); err != nil {
					http.Error(w, "params 应为 JSON 对象", http.StatusBadRequest)
					return
				}
			}
			confirmed := false
			if raw, has := fields["confirm"]; has {
				_ = json.Unmarshal(raw, &confirmed)
			}
			if !confirmed {
				http.Error(w, "危险操作需在 params 中带 confirm:true 确认后执行", http.StatusBadRequest)
				return
			}
			delete(fields, "confirm") // confirm 是 WebUI 门禁字段，不透传给 NapCat
			params, _ = json.Marshal(fields)
		}
		reqVal := reflect.New(typ) // *T，T 为该动作的请求结构体
		if len(params) > 0 {
			if err := json.Unmarshal(params, reqVal.Interface()); err != nil {
				http.Error(w, "params 解析失败（应为该动作的参数对象）: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), napcatCallTimeout)
		defer cancel()
		resp, err := request.Call(ctx, reqVal.Interface().(api.Request))
		if err != nil {
			napcatFail(w, err)
			return
		}
		if len(resp.Data) == 0 || string(resp.Data) == "null" {
			writeJSON(w, map[string]any{"ok": true})
			return
		}
		writeJSON(w, resp.Data)
	default:
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
	}
}
