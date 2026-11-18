package webui

import (
	"errors"
	"net/http"

	"Q-Mi/agent"
)

func handleSleep(w http.ResponseWriter, r *http.Request) {
	if !inspectionGET(w, r) {
		return
	}
	state, err := agent.SleepInspect()
	writeSleep(w, state, err)
}

func handleSleepWake(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		inspectionError(w, 405, "method", "仅支持 POST")
		return
	}
	state, err := agent.SleepWake()
	writeSleep(w, state, err)
}

func writeSleep(w http.ResponseWriter, state agent.SleepSnapshot, err error) {
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, agent.ErrAgentNotEnabled) {
			status = http.StatusServiceUnavailable
		}
		inspectionError(w, status, "sleep", err.Error())
		return
	}
	state.Signals, state.Deferred = nil, nil
	state.ObservedIDs = nil
	writeJSON(w, state)
}
