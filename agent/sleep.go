package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	agentruntime "Q-Mi/agent/runtime"
	"Q-Mi/storage"
)

type SleepSnapshot = agentruntime.SleepSnapshot

func (m *Manager) sleepController() (*agentruntime.SleepStore, error) {
	m.sleepMu.Lock()
	defer m.sleepMu.Unlock()
	if m.sleepStore != nil {
		return m.sleepStore, nil
	}
	if m.selfID <= 0 {
		return nil, ErrSelfIDMissing
	}
	path := m.statePath
	if path == "" {
		path = defaultStatePath
	}
	controller, err := agentruntime.NewSleepStore(filepath.Join(filepath.Dir(path), "sleep.json"), m.selfID)
	if err != nil {
		return nil, err
	}
	m.sleepStore = controller
	return controller, nil
}

func (m *Manager) SleepStatus() (SleepSnapshot, error) {
	controller, err := m.sleepController()
	if err != nil {
		return SleepSnapshot{}, err
	}
	return controller.Snapshot(time.Now()), nil
}

func (m *Manager) RequestSleep(reason string) (SleepSnapshot, error) {
	if !m.config().Sleep.IsEnabled() {
		return SleepSnapshot{}, errors.New("睡眠功能已关闭")
	}
	if m.store == nil {
		return SleepSnapshot{}, errors.New("消息存档未启用，无法保证睡眠期间的消息可恢复")
	}
	controller, err := m.sleepController()
	if err != nil {
		return SleepSnapshot{}, err
	}
	settings := m.config().Sleep.WithDefaults()
	state, err := controller.Start(time.Now(), reason, agentruntime.SleepSettings{
		Min: time.Duration(settings.MinSeconds) * time.Second, Max: time.Duration(settings.MaxSeconds) * time.Second,
		Window: time.Duration(settings.WindowSeconds) * time.Second, Threshold: settings.Threshold,
	})
	if err != nil {
		return SleepSnapshot{}, err
	}
	m.events.Record("", 0, "sleep", fmt.Sprintf("入睡，预计 %s 自然醒；%s", state.WakeAt.Format("15:04:05"), reason))
	m.scheduleSleep(state)
	return state, nil
}

// RestoreSleep must run at startup, even if no new QQ event arrives.
func (m *Manager) RestoreSleep() error {
	controller, err := m.sleepController()
	if err != nil {
		return err
	}
	state := controller.Snapshot(time.Now())
	if state.Sleeping {
		if !time.Now().Before(state.WakeAt) {
			return m.wakeSleep(state.ID, "timer")
		}
		m.scheduleSleep(state)
	} else if state.PendingWake {
		m.launchSleepWake(state.ID)
	}
	return nil
}

func (m *Manager) scheduleSleep(state SleepSnapshot) {
	m.sleepMu.Lock()
	if m.sleepStopped {
		m.sleepMu.Unlock()
		return
	}
	if m.sleepTimer != nil {
		m.sleepTimer.Stop()
	}
	m.sleepTimer = time.AfterFunc(max(0, time.Until(state.WakeAt)), func() {
		if err := m.wakeSleep(state.ID, "timer"); err != nil {
			slog.Error("自然唤醒失败，睡眠记录保留，可从 WebUI 重试", "err", err)
		}
	})
	if m.sleepCancel != nil {
		m.sleepCancel()
	}
	m.sleepCancel = nil
	m.sleepMu.Unlock()
	go m.runSleepMemory(state.ID)
}

func (m *Manager) wakeSleep(id, reason string) error {
	controller, err := m.sleepController()
	if err != nil {
		return err
	}
	woke, err := controller.Wake(time.Now(), id, reason)
	if err != nil {
		return err
	}
	if woke {
		m.events.Record("", 0, "sleep", "结束睡眠："+reason)
		m.launchSleepWake(id)
	}
	return nil
}

func (m *Manager) ForceWake() (SleepSnapshot, error) {
	state, err := m.SleepStatus()
	if err != nil {
		return SleepSnapshot{}, err
	}
	if err := m.wakeSleep(state.ID, "webui"); err != nil {
		return SleepSnapshot{}, err
	}
	if state.PendingWake {
		m.launchSleepWake(state.ID)
	}
	return m.SleepStatus()
}

func (m *Manager) sleepDirected(msg IncomingMessage) bool {
	if msg.SenderID <= 0 || msg.SenderID == m.selfID || (msg.SelfID > 0 && msg.SelfID != m.selfID) {
		return false
	}
	if msg.ChatType == "private" {
		return true
	}
	if msg.ChatType != "group" {
		return false
	}
	var segments []contextSegment
	if json.Unmarshal(msg.Segments, &segments) != nil {
		return false
	}
	for _, segment := range segments {
		if segment.Type == "at" && segmentInt(segment.Data["qq"]) == m.selfID {
			return true
		}
		if segment.Type == "reply" && m.store != nil {
			record, err := m.store.GetMessage(segmentInt(segment.Data["id"]))
			if err == nil && record != nil && !record.Recalled && record.UserID == m.selfID &&
				record.ChatType == msg.ChatType && record.ChatID == msg.ChatID {
				return true
			}
		}
	}
	return false
}

func segmentInt(raw json.RawMessage) int64 {
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		n, _ = strconv.ParseInt(text, 10, 64)
	}
	return n
}

// sleepGate never accumulates raw chat in the live Inbox. The event store is
// independent; this fallback also covers events whose archive handler runs later.
func (m *Manager) sleepGate(msg IncomingMessage) bool {
	controller, err := m.sleepController()
	if err != nil {
		return false
	}
	state := controller.Snapshot(time.Now())
	if !state.Sleeping {
		return false
	}
	if msg.MessageID != 0 && m.store != nil && (msg.ChatType == "group" || msg.ChatType == "private") {
		record, findErr := m.store.GetMessage(msg.MessageID)
		if findErr != nil {
			slog.Error("睡眠消息存档查询失败", "err", findErr)
		} else if record == nil {
			at := msg.Time
			if at.IsZero() {
				at = time.Now()
			}
			err := m.store.PutMessage(&storage.MessageRecord{MessageID: msg.MessageID, Direction: "recv",
				ChatType: msg.ChatType, ChatID: msg.ChatID, UserID: msg.SenderID, SenderName: msg.SenderName,
				RawMessage: msg.Text, Segments: msg.Segments, Time: float64(at.UnixMilli()) / 1000})
			if err != nil {
				slog.Error("睡眠消息存档失败", "err", err)
			}
		}
	}
	directed := m.sleepDirected(msg)
	if msg.MessageID == 0 && msg.SenderName != "轮询" {
		msg.Wake = WakeBackground
	}
	held, woke, err := controller.ObserveMessage(time.Now(), msg, directed)
	if err != nil {
		m.events.Record(msg.ChatType, msg.ChatID, KindError, "记录睡眠期间事件失败："+err.Error())
	}
	if woke {
		m.events.Record(msg.ChatType, msg.ChatID, "sleep", "密集指向消息唤醒")
		m.launchSleepWake(controller.Snapshot(time.Now()).ID)
	}
	return held
}

func (m *Manager) launchSleepWake(id string) {
	m.sleepMu.Lock()
	if m.sleepStopped || m.sleepWakeID == id {
		m.sleepMu.Unlock()
		return
	}
	if m.sleepStore == nil {
		m.sleepMu.Unlock()
		return
	}
	if state := m.sleepStore.Snapshot(time.Now()); state.ID != id || !state.PendingWake {
		m.sleepMu.Unlock()
		return
	}
	m.sleepWakeID = id
	if m.sleepTimer != nil {
		m.sleepTimer.Stop()
	}
	if m.sleepCancel != nil {
		m.sleepCancel()
		m.sleepCancel = nil
	}
	m.sleepMu.Unlock()
	go func() {
		m.runMu.Lock()
		defer m.runMu.Unlock()
		defer func() {
			m.sleepMu.Lock()
			if m.sleepWakeID == id {
				m.sleepWakeID = ""
			}
			m.sleepMu.Unlock()
		}()
		m.sleepMu.Lock()
		stopped := m.sleepStopped
		m.sleepMu.Unlock()
		if stopped {
			return
		}
		controller, err := m.sleepController()
		if err != nil {
			return
		}
		state := controller.Snapshot(time.Now())
		if state.ID != id || !state.PendingWake {
			return
		}
		turn, err := m.sleepWakeTurn(state)
		if err != nil {
			m.events.Record("", 0, KindError, "读取离线消息失败，可再次强制唤醒："+err.Error())
			return
		}
		// Once inserted into working context, all wake material remains available
		// even if its first request is blocked by the context budget.
		m.runActivity(turn, true)
		if err := controller.Acknowledge(id); err != nil {
			m.events.Record("", 0, KindError, "确认唤醒记录失败："+err.Error())
		}
	}()
}

// stopSleep leaves the persisted deadline intact for the next process.
func (m *Manager) stopSleep() {
	m.sleepMu.Lock()
	defer m.sleepMu.Unlock()
	m.sleepStopped = true
	if m.sleepTimer != nil {
		m.sleepTimer.Stop()
	}
	if m.sleepCancel != nil {
		m.sleepCancel()
		m.sleepCancel = nil
	}
}

func (m *Manager) sleepWakeTurn(state SleepSnapshot) (*Turn, error) {
	now := time.Now()
	turn := &Turn{SelfID: m.selfID, Wake: WakeBackground, Now: now}
	turn.NewMessages = append(turn.NewMessages, TurnMessage{
		Wake: WakeBackground, SenderID: m.selfID, SenderName: "睡眠状态", Time: now,
		Text: fmt.Sprintf("[醒来] 原因=%s；离线时间 %s 至 %s。以下都是离线期间的背景，不是刚刚对你说的话。按会话核对 @、引用和说话对象；不必逐条补回复，不因醒来而主动宣布状态。摘录不全时可查原会话存档。",
			state.WakeReason, state.StartedAt.Format(time.RFC3339), state.WokeAt.Format(time.RFC3339)),
	})
	if m.store != nil {
		chats, err := m.store.ChatSummaries()
		if err != nil {
			return nil, err
		}
		directedChats := map[string]bool{}
		for _, signal := range state.Signals {
			directedChats[fmt.Sprintf("%s:%d", signal.ChatType, signal.ChatID)] = true
		}
		sort.SliceStable(chats, func(i, j int) bool {
			return directedChats[fmt.Sprintf("%s:%d", chats[i].ChatType, chats[i].ChatID)] &&
				!directedChats[fmt.Sprintf("%s:%d", chats[j].ChatType, chats[j].ChatID)]
		})
		shown, omitted := 0, 0
		for _, chat := range chats {
			if chat.LastTime < float64(state.StartedAt.Unix()) {
				continue
			}
			if shown >= 8 {
				omitted++
				continue
			}
			// QQ events have second precision. Include the boundary seconds so
			// arrivals immediately after sleeping or before waking are retained.
			records, err := m.store.QueryMessages(storage.MessageFilter{ChatType: chat.ChatType, ChatID: chat.ChatID,
				Since: state.StartedAt.Truncate(time.Second), Until: state.WokeAt.Add(time.Second).Truncate(time.Second), Limit: 4,
				Attention: recentAttention(&Turn{SelfID: m.selfID})})
			if err != nil {
				return nil, err
			}
			seen := map[int64]bool{}
			for _, record := range records {
				seen[record.MessageID] = true
			}
			for _, signal := range state.Signals {
				if signal.ChatType != chat.ChatType || signal.ChatID != chat.ChatID || seen[signal.ID] || len(records) >= 12 {
					continue
				}
				record, err := m.store.GetMessage(signal.ID)
				if err != nil {
					return nil, err
				}
				if record == nil || record.Recalled || record.ChatType != chat.ChatType || record.ChatID != chat.ChatID {
					continue
				}
				records = append(records, record)
				seen[record.MessageID] = true
			}
			if len(records) == 0 {
				continue
			}
			sort.SliceStable(records, func(i, j int) bool { return records[i].Time > records[j].Time })
			rows := make([]map[string]any, 0, len(records))
			for i := len(records) - 1; i >= 0; i-- {
				record := records[i]
				rows = append(rows, map[string]any{"message_id": record.MessageID, "sender_id": record.UserID,
					"sender_name": record.SenderName, "time": record.Time, "text": truncateRunes(record.RawMessage, 300),
					"relations": sleepRelations(record.Segments)})
			}
			text, _ := marshalCompact(map[string]any{"offline_excerpt": true, "complete": false,
				"chat_type": chat.ChatType, "chat_id": chat.ChatID, "recent_messages": rows})
			turn.NewMessages = append(turn.NewMessages, TurnMessage{Wake: WakeBackground, ChatType: chat.ChatType,
				ChatID: chat.ChatID, SenderName: "离线消息摘录", Text: text, Time: now})
			shown++
		}
		if omitted > 0 {
			turn.NewMessages[0].Text += fmt.Sprintf(" 另有 %d 个会话未展示，必要时查存档。", omitted)
		}
	}
	for i, message := range state.Deferred {
		if i >= 16 {
			turn.NewMessages[0].Text += fmt.Sprintf(" 另有 %d 条后台回执未展示，可用 task_get/self_events 查证。", len(state.Deferred)-i+state.DeferredOmitted)
			break
		}
		turn.NewMessages = append(turn.NewMessages, TurnMessage{Wake: WakeBackground, ChatType: message.ChatType,
			ChatID: message.ChatID, SenderID: message.SenderID, SenderName: message.SenderName,
			Text: "[离线期间后台回执] " + truncateRunes(message.Text, 300), Time: message.Time})
	}
	return turn, nil
}

func sleepRelations(raw json.RawMessage) []contextSegment {
	var segments []contextSegment
	_ = json.Unmarshal(raw, &segments)
	result := []contextSegment{}
	for _, segment := range segments {
		if segment.Type == "at" || segment.Type == "reply" {
			result = append(result, segment)
		}
	}
	return result
}

func SleepInspect() (SleepSnapshot, error) {
	if m := getManager(); m != nil {
		return m.SleepStatus()
	}
	return SleepSnapshot{}, ErrAgentNotEnabled
}

func SleepWake() (SleepSnapshot, error) {
	if m := getManager(); m != nil {
		return m.ForceWake()
	}
	return SleepSnapshot{}, ErrAgentNotEnabled
}
