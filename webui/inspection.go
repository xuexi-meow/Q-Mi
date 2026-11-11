package webui

import (
	"errors"
	"net/http"
	"strconv"

	"Q-Mi/agent"
)

func inspectionError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	writeJSON(w, map[string]string{"error": message, "code": code})
}

func inspectionGET(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		return true
	}
	w.Header().Set("Allow", "GET")
	inspectionError(w, 405, "method", "仅支持 GET")
	return false
}

func strictInt(r *http.Request, key string, fallback, min, max int) (int, error) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return fallback, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < min {
		return 0, errors.New(key + " 参数无效")
	}
	if n > max {
		n = max
	}
	return n, nil
}

func contextSource(r *http.Request) (string, error) {
	source := r.URL.Query().Get("source")
	if source == "" {
		source = "working"
	}
	if source != "request" && source != "working" {
		return "", errors.New("source 仅支持 request 或 working")
	}
	return source, nil
}

func handleRuntime(w http.ResponseWriter, r *http.Request) {
	if !inspectionGET(w, r) {
		return
	}
	writeJSON(w, agent.RuntimeInspect())
}

func handleContext(w http.ResponseWriter, r *http.Request) {
	if !inspectionGET(w, r) {
		return
	}
	source, err := contextSource(r)
	if err != nil {
		inspectionError(w, 400, "query", err.Error())
		return
	}
	offset, err := strictInt(r, "offset", 0, 0, int(^uint(0)>>1))
	if err != nil {
		inspectionError(w, 400, "query", err.Error())
		return
	}
	limit, err := strictInt(r, "limit", 40, 1, 100)
	if err != nil {
		inspectionError(w, 400, "query", err.Error())
		return
	}
	kind := r.URL.Query().Get("kind")
	switch kind {
	case "", "all", "prompt", "state", "summary", "input", "output", "tool_result", "tool_definition":
	default:
		inspectionError(w, 400, "query", "kind 参数无效")
		return
	}
	chatType, chatID := r.URL.Query().Get("chat_type"), r.URL.Query().Get("chat_id")
	if chatType != "" || chatID != "" {
		id, err := strconv.ParseInt(chatID, 10, 64)
		if err != nil || id <= 0 || (chatType != "group" && chatType != "private") {
			inspectionError(w, 400, "query", "会话类型和正整数 ID 必须同时提供")
			return
		}
	}
	page, err := agent.ContextInspect(agent.ContextQuery{Source: source, Kind: kind, ChatType: chatType, ChatID: chatID, Query: r.URL.Query().Get("q"), Offset: offset, Limit: limit})
	if err != nil {
		inspectionError(w, 400, "query", err.Error())
		return
	}
	writeJSON(w, page)
}

func handleContextEntry(w http.ResponseWriter, r *http.Request) {
	if !inspectionGET(w, r) {
		return
	}
	source, err := contextSource(r)
	if err != nil {
		inspectionError(w, 400, "query", err.Error())
		return
	}
	version, err := strconv.ParseUint(r.URL.Query().Get("version"), 10, 64)
	index, indexErr := strconv.Atoi(r.URL.Query().Get("index"))
	if err != nil || version == 0 || indexErr != nil || index < 0 {
		inspectionError(w, 400, "query", "需要有效的 version 和 index")
		return
	}
	entry, err := agent.ContextEntryInspect(source, version, index)
	if errors.Is(err, agent.ErrInspectionVersion) {
		inspectionError(w, 409, "version_conflict", err.Error())
		return
	}
	if err != nil {
		inspectionError(w, 404, "not_found", err.Error())
		return
	}
	writeJSON(w, entry)
}

func handleTasks(w http.ResponseWriter, r *http.Request) {
	if !inspectionGET(w, r) {
		return
	}
	limit, err := strictInt(r, "limit", 100, 1, 100)
	if err != nil {
		inspectionError(w, 400, "query", err.Error())
		return
	}
	writeJSON(w, agent.TaskInspect(limit))
}
