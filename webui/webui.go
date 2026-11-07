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
		case "/api/overview":
			handleOverview(w, r)
		default:
			http.NotFound(w, r)
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
	Napcat        napcatStatus `json:"napcat"`
	UptimeSeconds int64        `json:"uptime_seconds"`
	Messages      int64        `json:"messages"`
	Notices       int64        `json:"notices"`
	Mood          string       `json:"mood"`
	NotesCount    int          `json:"notes_count"`
	TimersCount   int          `json:"timers_count"`
}

// handleOverview 并行收集 NapCat 状态/版本、storage 统计、mood/notes/timers
// 计数与运行时长；NapCat 不可达时字段置空不报错（spec §4）。
func handleOverview(w http.ResponseWriter, r *http.Request) {
	ov := overview{
		UptimeSeconds: int64(time.Since(startTime).Seconds()),
		Mood:          agent.MoodGet(),
		NotesCount:    len(agent.NotesList()),
		TimersCount:   agent.TimerCount(), // W1 占位 -1，W3 补列表方法
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
