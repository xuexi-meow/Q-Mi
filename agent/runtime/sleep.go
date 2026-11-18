package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"
)

type SleepSettings struct {
	Min, Max, Window time.Duration
	Threshold        int
}

type SleepMemory struct {
	ChatType  string    `json:"chat_type"`
	ChatID    int64     `json:"chat_id,string"`
	Text      string    `json:"text"`
	SourceIDs []int64   `json:"source_ids"`
	UpdatedAt time.Time `json:"updated_at"`
}

type sleepSignal struct {
	ID       int64     `json:"id"`
	Time     time.Time `json:"time"`
	ChatType string    `json:"chat_type"`
	ChatID   int64     `json:"chat_id,string"`
}

type SleepSnapshot struct {
	ID               string            `json:"id"`
	SelfID           int64             `json:"self_id,string"`
	Sleeping         bool              `json:"sleeping"`
	Reason           string            `json:"reason"`
	StartedAt        time.Time         `json:"started_at"`
	WakeAt           time.Time         `json:"wake_at"`
	WokeAt           time.Time         `json:"woke_at"`
	WakeReason       string            `json:"wake_reason"`
	PendingWake      bool              `json:"pending_wake"`
	WindowSeconds    int               `json:"window_seconds"`
	Threshold        int               `json:"threshold"`
	DirectedCount    int               `json:"directed_count"`
	Consolidation    string            `json:"consolidation"`
	Consolidated     int               `json:"consolidated"`
	MemoryCount      int               `json:"memory_count"`
	LastError        string            `json:"last_error,omitempty"`
	Deferred         []IncomingMessage `json:"deferred,omitempty"`
	DeferredOmitted  int               `json:"deferred_omitted"`
	Memories         []SleepMemory     `json:"memories,omitempty"`
	Signals          []sleepSignal     `json:"signals,omitempty"`
	ObservedIDs      []int64           `json:"observed_ids,omitempty"`
	ConsolidationEnd time.Time         `json:"consolidation_end,omitempty"`
}

// SleepStore commits state before exposing transitions. It owns no timers or
// model calls, so restoring a deadline never samples another duration.
type SleepStore struct {
	mu    sync.Mutex
	path  string
	state SleepSnapshot
}

func NewSleepStore(path string, selfID int64) (*SleepStore, error) {
	s := &SleepStore{path: path, state: SleepSnapshot{SelfID: selfID}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.state); err != nil {
		return nil, fmt.Errorf("睡眠记录损坏: %w", err)
	}
	if s.state.SelfID != selfID {
		return nil, errors.New("睡眠记录属于另一个账号")
	}
	if s.state.Sleeping && (s.state.ID == "" || s.state.StartedAt.IsZero() ||
		!s.state.WakeAt.After(s.state.StartedAt) || s.state.WindowSeconds < 1 || s.state.Threshold < 1) {
		return nil, errors.New("睡眠记录缺少有效的起止时间或唤醒阈值")
	}
	return s, nil
}

func cloneSleep(state SleepSnapshot) SleepSnapshot {
	state.Signals = slices.Clone(state.Signals)
	state.ObservedIDs = slices.Clone(state.ObservedIDs)
	state.Deferred = slices.Clone(state.Deferred)
	for i := range state.Deferred {
		state.Deferred[i].Segments = slices.Clone(state.Deferred[i].Segments)
		state.Deferred[i].Images = slices.Clone(state.Deferred[i].Images)
	}
	state.Memories = slices.Clone(state.Memories)
	for i := range state.Memories {
		state.Memories[i].SourceIDs = slices.Clone(state.Memories[i].SourceIDs)
	}
	return state
}

func (s *SleepStore) Snapshot(now time.Time) SleepSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := cloneSleep(s.state)
	state.MemoryCount = len(state.Memories)
	state.DirectedCount = 0
	for _, signal := range state.Signals {
		if now.Sub(signal.Time) <= time.Duration(state.WindowSeconds)*time.Second {
			state.DirectedCount++
		}
	}
	return state
}

func (s *SleepStore) Start(now time.Time, reason string, settings SleepSettings) (SleepSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Sleeping || s.state.PendingWake {
		return SleepSnapshot{}, errors.New("已经睡着，或上次醒来后的消息尚未处理")
	}
	if settings.Min <= 0 || settings.Max < settings.Min || settings.Max > time.Hour ||
		settings.Window < time.Second || settings.Window > 10*time.Minute || settings.Threshold < 2 || settings.Threshold > 100 {
		return SleepSnapshot{}, errors.New("睡眠时长或消息唤醒参数无效")
	}
	duration := settings.Min
	if span := settings.Max - settings.Min; span > 0 {
		// Two samples favor the middle without a hard-coded fixed nap.
		duration += time.Duration((rand.Int64N(int64(span)) + rand.Int64N(int64(span))) / 2)
	}
	next := SleepSnapshot{
		ID: newTimerID(), SelfID: s.state.SelfID, Sleeping: true,
		Reason: reason, StartedAt: now, WakeAt: now.Add(duration),
		WindowSeconds: int(settings.Window.Seconds()), Threshold: settings.Threshold,
		Consolidation: "pending", Memories: slices.Clone(s.state.Memories),
	}
	if err := s.commit(next); err != nil {
		return SleepSnapshot{}, err
	}
	return cloneSleep(next), nil
}

func (s *SleepStore) Observe(now time.Time, msg IncomingMessage, directed bool) (bool, error) {
	_, woke, err := s.ObserveMessage(now, msg, directed)
	return woke, err
}

// ObserveMessage decides holding and waking under the same lock. A concurrent
// wake must never make an already-awake message disappear in the sleep gate.
func (s *SleepStore) ObserveMessage(now time.Time, msg IncomingMessage, directed bool) (held, woke bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.state.Sleeping {
		return false, false, nil
	}
	next := cloneSleep(s.state)
	if (msg.Wake == "" || msg.Wake == "chat") && directed && msg.MessageID != 0 {
		// Preserve deduplication after a signal leaves the sliding window.
		// Saturation stops additional votes; the natural deadline still wakes.
		if slices.Contains(next.ObservedIDs, msg.MessageID) || len(next.ObservedIDs) >= 8192 {
			return true, false, nil
		}
		next.ObservedIDs = append(next.ObservedIDs, msg.MessageID)
		next.Signals = slices.DeleteFunc(next.Signals, func(signal sleepSignal) bool {
			return now.Sub(signal.Time) > time.Duration(next.WindowSeconds)*time.Second
		})
		for _, signal := range next.Signals {
			if signal.ID == msg.MessageID {
				return true, false, nil
			}
		}
		next.Signals = append(next.Signals, sleepSignal{ID: msg.MessageID, Time: now, ChatType: msg.ChatType, ChatID: msg.ChatID})
		if len(next.Signals) >= next.Threshold {
			wakeSleep(&next, now, "messages")
		}
	} else if msg.Wake == "background" && msg.SenderName != "轮询" {
		if len(next.Deferred) < 64 {
			msg.Text = limitRunes(msg.Text, 2000)
			msg.Images, msg.Segments = nil, nil
			next.Deferred = append(next.Deferred, msg)
		} else {
			next.DeferredOmitted++
		}
	} else {
		return true, false, nil
	}
	if err := s.commit(next); err != nil {
		return true, false, err
	}
	return true, !next.Sleeping, nil
}

func wakeSleep(state *SleepSnapshot, now time.Time, reason string) {
	state.Sleeping, state.PendingWake = false, true
	state.WokeAt, state.WakeReason = now, reason
	if state.Consolidation == "pending" || state.Consolidation == "running" {
		state.Consolidation = "interrupted"
	}
}

func (s *SleepStore) Wake(now time.Time, id, reason string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.state.Sleeping || s.state.ID != id {
		return false, nil
	}
	next := cloneSleep(s.state)
	wakeSleep(&next, now, reason)
	if err := s.commit(next); err != nil {
		return false, err
	}
	return true, nil
}

func (s *SleepStore) Acknowledge(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.ID != id || s.state.Sleeping {
		return errors.New("唤醒记录已变化")
	}
	next := cloneSleep(s.state)
	next.PendingWake, next.Deferred = false, nil
	next.Signals, next.ObservedIDs = nil, nil
	return s.commit(next)
}

func (s *SleepStore) SetConsolidation(id, status, detail string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.ID != id || !s.state.Sleeping {
		return errors.New("本次睡眠已结束")
	}
	next := cloneSleep(s.state)
	next.Consolidation, next.LastError = status, limitRunes(detail, 1000)
	if status == "done" || status == "failed" {
		next.ConsolidationEnd = time.Now()
	}
	return s.commit(next)
}

func (s *SleepStore) CompleteMemory(id string, memory SleepMemory) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.ID != id || !s.state.Sleeping {
		return errors.New("本次睡眠已结束，不再提交整理结果")
	}
	if memory.ChatID <= 0 || (memory.ChatType != "group" && memory.ChatType != "private") ||
		memory.Text == "" || len(memory.SourceIDs) == 0 {
		return errors.New("整理结果缺少会话或证据")
	}
	next := cloneSleep(s.state)
	memory.UpdatedAt = time.Now()
	next.Memories = slices.DeleteFunc(next.Memories, func(old SleepMemory) bool {
		return old.ChatType == memory.ChatType && old.ChatID == memory.ChatID
	})
	next.Memories = append(next.Memories, memory)
	if len(next.Memories) > 100 {
		next.Memories = next.Memories[len(next.Memories)-100:]
	}
	next.Consolidated++
	return s.commit(next)
}

func (s *SleepStore) commit(next SleepSnapshot) error {
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".sleep-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(data); err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, s.path)
	}
	if err != nil {
		return fmt.Errorf("保存睡眠状态失败: %w", err)
	}
	s.state = next
	return nil
}
