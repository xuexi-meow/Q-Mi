// timers.go P3.5 定时器与主动行为：TimerStore 管理一次性定时提醒，
// 记录持久化为 agent/timers.json（JSON 数组），进程重启后 LoadPending
// 恢复未触发的计时。到点触发经 fire 回调（manager 注入，合成为
// [定时提醒] IncomingMessage 入队），异步执行且 recover，不影响本包。
package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// maxActive 同时挂起的计时器上限（防模型刷屏式排任务）。
const maxActive = 32

// maxTimerDelay 单次定时上限：7 天。
const maxTimerDelay = 7 * 24 * time.Hour

// ScheduledTimer 一条定时提醒记录（持久化文件与 fire 回调共用）。
type ScheduledTimer struct {
	ID       string    `json:"id"`
	FireAt   time.Time `json:"fire_at"`
	Note     string    `json:"note"`
	ChatType string    `json:"chat_type"`
	ChatID   int64     `json:"chat_id"`
	SelfID   int64     `json:"self_id"`
}

// TimerStore 一次性定时器集合：active 为在跑的底层 timer，records 为
// 待触发记录（与 active 一一对应）。mu 保护两个 map 与持久化写盘。
type TimerStore struct {
	mu      sync.Mutex
	active  map[string]*time.Timer
	records map[string]ScheduledTimer
	path    string
	fire    func(ScheduledTimer)
}

// NewTimerStore 构造计时器存储。fire 为到点回调（异步调用，内部 recover）。
// 只构造不加载：重启恢复由调用方显式调 LoadPending。
func NewTimerStore(path string, fire func(ScheduledTimer)) *TimerStore {
	return &TimerStore{
		active:  make(map[string]*time.Timer),
		records: make(map[string]ScheduledTimer),
		path:    path,
		fire:    fire,
	}
}

// Schedule 定一个 delay 后触发的一次性提醒，记录同步持久化到 path。
// delay ≤0 或 >7 天报错；在跑计时器达 maxActive 报错。
func (s *TimerStore) Schedule(delay time.Duration, note string, chatType string, chatID, selfID int64) (ScheduledTimer, error) {
	if delay <= 0 {
		return ScheduledTimer{}, fmt.Errorf("定时延迟必须为正数, got %v", delay)
	}
	if delay > maxTimerDelay {
		return ScheduledTimer{}, fmt.Errorf("定时最长 7 天（%d 秒）, got %v", int(maxTimerDelay.Seconds()), delay)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.active) >= maxActive {
		return ScheduledTimer{}, errors.New("计时器已达上限（32 个），请先用 cancel_timer 取消一些")
	}
	rec := ScheduledTimer{
		ID:       newTimerID(),
		FireAt:   time.Now().Add(delay),
		Note:     note,
		ChatType: chatType,
		ChatID:   chatID,
		SelfID:   selfID,
	}
	s.startLocked(rec, delay)
	s.persistLocked()
	return rec, nil
}

// Cancel 取消一个未触发的定时：停底层 timer、删记录并持久化。
// 记录不存在（已触发/已取消/未知 ID）返回 false。
func (s *TimerStore) Cancel(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.records[id]; !ok {
		return false
	}
	if t, ok := s.active[id]; ok {
		t.Stop()
		delete(s.active, id)
	}
	delete(s.records, id)
	s.persistLocked()
	return true
}

// LoadPending 重启恢复：读 path，未来条目用剩余时长重排（保留原 ID 与
// FireAt，不重复持久化），过期条目立即异步触发并从文件移除。
// 文件缺失返回 0；损坏记 Warn 返回 0。
func (s *TimerStore) LoadPending() int {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			slog.Warn("读取计时器文件失败，跳过恢复", "path", s.path, "err", err)
		}
		return 0
	}
	var recs []ScheduledTimer
	if err := json.Unmarshal(data, &recs); err != nil {
		slog.Warn("计时器文件损坏，忽略全部未触发计时", "path", s.path, "err", err)
		return 0
	}
	now := time.Now()
	var due []ScheduledTimer
	restored := 0
	s.mu.Lock()
	for _, rec := range recs {
		if rec.ID == "" {
			continue
		}
		if _, dup := s.records[rec.ID]; dup {
			continue
		}
		delay := rec.FireAt.Sub(now)
		if delay <= 0 {
			due = append(due, rec) // 已过期：立即触发
		} else {
			s.startLocked(rec, delay) // 重排即可，记录已在文件里
		}
		restored++
	}
	if len(due) > 0 {
		s.persistLocked() // 把已触发/过期条目从文件移除，避免重启后重复提醒
	}
	s.mu.Unlock()
	for _, rec := range due {
		s.fireAsync(rec)
	}
	return restored
}

// List 返回待触发记录快照，按 FireAt 正序（WebUI 计时器列表用）。
func (s *TimerStore) List() []ScheduledTimer {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ScheduledTimer, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FireAt.Before(out[j].FireAt) })
	return out
}

// Active 在跑的计时器数量（测试/调试用）。
func (s *TimerStore) Active() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.active)
}

// startLocked 启动底层 timer 并登记记录（调用方持有 mu）。
func (s *TimerStore) startLocked(rec ScheduledTimer, delay time.Duration) {
	s.records[rec.ID] = rec
	s.active[rec.ID] = time.AfterFunc(delay, func() { s.onFire(rec.ID) })
}

// onFire 到点回调：记录存在才触发（并发 Cancel 抢先删除则静默跳过），
// 删除记录后异步调 fire。
func (s *TimerStore) onFire(id string) {
	s.mu.Lock()
	rec, ok := s.records[id]
	if ok {
		delete(s.records, id)
		delete(s.active, id)
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	s.fireAsync(rec)
}

// fireAsync 异步调 fire 回调：goroutine + recover，回调 panic 不影响本包。
func (s *TimerStore) fireAsync(rec ScheduledTimer) {
	if s.fire == nil {
		return
	}
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("计时器 fire 回调 panic，已恢复", "timer_id", rec.ID, "panic", r)
			}
		}()
		s.fire(rec)
	}()
}

// persistLocked 全量重写 path 为 records 的 JSON 数组（调用方持有 mu）。
// 写失败只 Warn 不阻断——计时丢失可容忍，不该打断正常流程。
func (s *TimerStore) persistLocked() {
	recs := make([]ScheduledTimer, 0, len(s.records))
	for _, r := range s.records {
		recs = append(recs, r)
	}
	data, err := json.Marshal(recs)
	if err != nil {
		slog.Warn("序列化计时器记录失败", "err", err)
		return
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		slog.Warn("创建计时器目录失败", "path", s.path, "err", err)
		return
	}
	if err := os.WriteFile(s.path, data, 0o600); err != nil {
		slog.Warn("持久化计时器记录失败（不影响运行）", "path", s.path, "err", err)
	}
}

// newTimerID 生成 8 字节 crypto/rand hex ID（16 字符）。
// crypto/rand 不可用（几乎不可能）时退化为纳秒时间戳，保唯一性。
func newTimerID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
