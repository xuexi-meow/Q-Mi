package webui

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"Q-Mi/agent"
)

func contextControlError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agent.ErrContextBusy):
		inspectionError(w, 409, "busy", err.Error())
	case errors.Is(err, agent.ErrInspectionVersion):
		inspectionError(w, 409, "version_conflict", err.Error())
	case errors.Is(err, agent.ErrInspectionIndex):
		inspectionError(w, 404, "not_found", err.Error())
	case errors.Is(err, agent.ErrAgentNotEnabled):
		inspectionError(w, 503, "unavailable", err.Error())
	default:
		inspectionError(w, 400, "context_action", err.Error())
	}
}

func handleContextEdit(w http.ResponseWriter, r *http.Request) {
	if !inspectionGET(w, r) {
		return
	}
	version, err := strconv.ParseUint(r.URL.Query().Get("version"), 10, 64)
	index, indexErr := strconv.Atoi(r.URL.Query().Get("index"))
	if err != nil || version == 0 || indexErr != nil || index < 0 ||
		(r.URL.Query().Get("source") != "" && r.URL.Query().Get("source") != "working") {
		inspectionError(w, 400, "query", "需要工作记录的有效 version 和 index")
		return
	}
	entry, err := agent.ContextEntryEdit(version, index)
	if err != nil {
		contextControlError(w, err)
		return
	}
	writeJSON(w, entry)
}

func handleContextAction(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		inspectionError(w, 405, "method", "仅支持 POST")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 3*1024*1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var payload struct {
		agent.ContextAction
		Index *int `json:"index"`
	}
	err := decoder.Decode(&payload)
	if err == nil {
		var trailing any
		if decoder.Decode(&trailing) != io.EOF {
			err = errors.New("请求必须只有一个 JSON 对象")
		}
	}
	if err != nil {
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) {
			inspectionError(w, 413, "size", "上下文操作请求过大")
		} else {
			inspectionError(w, 400, "json", "无效的上下文操作 JSON")
		}
		return
	}
	action := payload.ContextAction
	if payload.Index != nil {
		action.Index = *payload.Index
	}
	valid := action.Version > 0
	switch action.Action {
	case "edit":
		valid = valid && payload.Index != nil && action.Index >= 0 && (action.Text != nil || action.ClearReasoning)
	case "delete":
		valid = valid && payload.Index != nil && action.Index >= 0 && action.Text == nil && !action.ClearReasoning
	case "compact", "clean", "reset", "resume":
		valid = valid && action.Text == nil && !action.ClearReasoning
	default:
		valid = false
	}
	if !valid {
		inspectionError(w, 400, "action", "上下文操作参数无效")
		return
	}
	if action.Action == "compact" && !action.Preview {
		// 摘要使用模型请求的时间上限，不能受普通页面的 15 秒写超时影响。
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(150 * time.Second))
		ctx, cancel := context.WithTimeout(r.Context(), 130*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
	}
	result, err := agent.ContextControl(r.Context(), action)
	if err != nil {
		contextControlError(w, err)
		return
	}
	writeJSON(w, result)
}
