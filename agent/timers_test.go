// timers_test.go P3.5 定时器存储测试：到点触发、取消、上限、持久化与重启恢复。
package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"
)

// fireRecorder 计数并记录 fire 回调入参的测试桩（并发安全）。
type fireRecorder struct {
	mu  sync.Mutex
	got []ScheduledTimer
}

func (r *fireRecorder) record(st ScheduledTimer) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = append(r.got, st)
}

func (r *fireRecorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.got)
}

func (r *fireRecorder) all() []ScheduledTimer {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ScheduledTimer(nil), r.got...)
}

// readTimersFile 读持久化文件并解析为记录数组。
func readTimersFile(t *testing.T, path string) []ScheduledTimer {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取计时器文件 %s 失败: %v", path, err)
	}
	var recs []ScheduledTimer
	if err := json.Unmarshal(data, &recs); err != nil {
		t.Fatalf("计时器文件不是合法 JSON 数组: %v (%s)", err, data)
	}
	return recs
}

// TestScheduleFiresWithCompleteRecord 50ms 定时到点触发 fire，
// 回调收到完整 ScheduledTimer 字段（16 位 hex ID、FireAt、note、会话上下文）。
func TestScheduleFiresWithCompleteRecord(t *testing.T) {
	rec := &fireRecorder{}
	store := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), rec.record)

	before := time.Now()
	st, err := store.Schedule(50*time.Millisecond, "看看他回了没", "group", 888, 999)
	if err != nil {
		t.Fatalf("Schedule 应成功, got err: %v", err)
	}
	if store.Active() != 1 {
		t.Fatalf("调度后 Active = %d, want 1", store.Active())
	}
	if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(st.ID) {
		t.Errorf("ID 应为 crypto/rand 8 字节 hex（16 字符）, got %q", st.ID)
	}
	if st.Note != "看看他回了没" || st.ChatType != "group" || st.ChatID != 888 || st.SelfID != 999 {
		t.Errorf("记录字段不符: %+v", st)
	}
	if st.FireAt.Before(before) || st.FireAt.After(before.Add(2*time.Second)) {
		t.Errorf("FireAt = %v, 应为 now+50ms 附近 (before=%v)", st.FireAt, before)
	}

	waitFor(t, 3*time.Second, func() bool { return rec.count() == 1 }, "50ms 定时应触发 fire")
	got := rec.all()[0]
	if got != st {
		t.Errorf("fire 收到 %+v, want %+v", got, st)
	}
	waitFor(t, 3*time.Second, func() bool { return store.Active() == 0 }, "触发后应从 active 移除")
}

// TestSchedulePersistsRecordReadableByNewStore Schedule 后记录落盘为
// JSON 数组；新 store（同路径）LoadPending 能恢复同一 ID 的未来条目并到点触发。
func TestSchedulePersistsRecordReadableByNewStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timers.json")
	rec1 := &fireRecorder{}
	store1 := NewTimerStore(path, rec1.record)

	st, err := store1.Schedule(time.Second, "持久化的提醒", "private", 20002, 999)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	// 文件里已有这条记录
	fileRecs := readTimersFile(t, path)
	if len(fileRecs) != 1 || fileRecs[0].ID != st.ID || fileRecs[0].Note != "持久化的提醒" {
		t.Fatalf("持久化记录不符: %+v, want id=%s", fileRecs, st.ID)
	}

	// 新 store 从同一文件恢复：同 ID、同字段，到点触发新回调
	rec2 := &fireRecorder{}
	store2 := NewTimerStore(path, rec2.record)
	if n := store2.LoadPending(); n != 1 {
		t.Fatalf("LoadPending 应恢复 1 条, got %d", n)
	}
	if store2.Active() != 1 {
		t.Fatalf("恢复后 Active = %d, want 1", store2.Active())
	}
	waitFor(t, 5*time.Second, func() bool { return rec2.count() == 1 }, "恢复的定时应到点触发")
	got := rec2.all()[0]
	if got.ID != st.ID || got.Note != "持久化的提醒" || got.ChatType != "private" ||
		got.ChatID != 20002 || got.SelfID != 999 {
		t.Errorf("恢复后触发的记录不符: %+v, want id=%s", got, st.ID)
	}
	if !got.FireAt.Equal(st.FireAt) {
		t.Errorf("恢复后 FireAt = %v, want 原值 %v（重排不得重置时间）", got.FireAt, st.FireAt)
	}
}

// TestLoadPendingRestoresFutureAndFiresExpired 手写计时器文件：未来条目重排后
// 到点触发（保留原 ID），过期条目立即异步触发，且过期条目从文件移除。
func TestLoadPendingRestoresFutureAndFiresExpired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timers.json")
	future := ScheduledTimer{
		ID: "future01", FireAt: time.Now().Add(120 * time.Millisecond),
		Note: "还没到点", ChatType: "group", ChatID: 888, SelfID: 999,
	}
	expired := ScheduledTimer{
		ID: "expired1", FireAt: time.Now().Add(-time.Hour),
		Note: "早就该响", ChatType: "private", ChatID: 20002, SelfID: 999,
	}
	data, err := json.Marshal([]ScheduledTimer{future, expired})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	rec := &fireRecorder{}
	store := NewTimerStore(path, rec.record)
	if n := store.LoadPending(); n != 2 {
		t.Fatalf("LoadPending 应恢复 2 条, got %d", n)
	}
	if store.Active() != 1 {
		t.Fatalf("未来条目重排后 Active = %d, want 1", store.Active())
	}

	// 过期条目立即触发（LoadPending 返回即已异步发出）
	waitFor(t, 3*time.Second, func() bool { return rec.count() >= 1 }, "过期条目应立即触发")
	if first := rec.all()[0]; first.ID != "expired1" || first.Note != "早就该响" {
		t.Errorf("过期条目应先触发: %+v", first)
	}
	// 未来条目到点触发
	waitFor(t, 3*time.Second, func() bool { return rec.count() == 2 }, "未来条目应到点触发")
	for _, st := range rec.all() {
		if st.ID == "future01" && (st.Note != "还没到点" || st.ChatType != "group" || st.ChatID != 888) {
			t.Errorf("未来条目触发字段不符: %+v", st)
		}
	}

	// 过期条目已从文件移除（文件只剩未来条目；此刻它尚未到点）
	fileRecs := readTimersFile(t, path)
	if len(fileRecs) != 1 || fileRecs[0].ID != "future01" {
		t.Errorf("LoadPending 后文件应只剩 future01（移除已过期条目）: %+v", fileRecs)
	}
}

// TestLoadPendingDoesNotRewriteWhenOnlyFuture 仅重排未来条目时不得重复
// 持久化（文件内容逐字节不变）。
func TestLoadPendingDoesNotRewriteWhenOnlyFuture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timers.json")
	rec := ScheduledTimer{
		ID: "future01", FireAt: time.Now().Add(500 * time.Millisecond),
		Note: "未到点", ChatType: "group", ChatID: 888, SelfID: 999,
	}
	data, err := json.Marshal([]ScheduledTimer{rec})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewTimerStore(path, func(ScheduledTimer) { t.Error("500ms 内不应触发 fire") })
	if n := store.LoadPending(); n != 1 {
		t.Fatalf("LoadPending 应恢复 1 条, got %d", n)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(data) {
		t.Errorf("仅重排不应重写文件:\nbefore=%s\nafter =%s", data, after)
	}
	if ok := store.Cancel("future01"); !ok {
		t.Error("清理：取消恢复的计时器应成功")
	}
}

// TestLoadPendingMissingAndCorruptFile 文件缺失返回 0；损坏记 Warn 并返回 0。
func TestLoadPendingMissingAndCorruptFile(t *testing.T) {
	missing := NewTimerStore(filepath.Join(t.TempDir(), "nope.json"), func(ScheduledTimer) {})
	if n := missing.LoadPending(); n != 0 {
		t.Errorf("文件缺失 LoadPending = %d, want 0", n)
	}
	if missing.Active() != 0 {
		t.Errorf("文件缺失不应有计时, Active = %d", missing.Active())
	}

	corrupt := NewTimerStore(filepath.Join(t.TempDir(), "broken.json"), func(ScheduledTimer) {})
	if err := os.WriteFile(corrupt.path, []byte("not json {{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if n := corrupt.LoadPending(); n != 0 {
		t.Errorf("文件损坏 LoadPending = %d, want 0", n)
	}
	if corrupt.Active() != 0 {
		t.Errorf("文件损坏不应有计时, Active = %d", corrupt.Active())
	}
}

// TestCancelPreventsFire Cancel 停掉未到点的定时：不触发、Active 归零、
// 记录从文件移除；重复取消与未知 ID 返回 false。
func TestCancelPreventsFire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timers.json")
	rec := &fireRecorder{}
	store := NewTimerStore(path, rec.record)

	st, err := store.Schedule(150*time.Millisecond, "不该响的", "group", 888, 999)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if ok := store.Cancel(st.ID); !ok {
		t.Fatalf("Cancel 应成功")
	}
	time.Sleep(300 * time.Millisecond) // 越过原定触发时刻
	if rec.count() != 0 {
		t.Errorf("取消后不应触发 fire, got %d 次", rec.count())
	}
	if store.Active() != 0 {
		t.Errorf("取消后 Active = %d, want 0", store.Active())
	}
	if recs := readTimersFile(t, path); len(recs) != 0 {
		t.Errorf("取消后文件应清空, got %+v", recs)
	}
	if store.Cancel(st.ID) {
		t.Error("重复取消同一 ID 应返回 false")
	}
	if store.Cancel("nonexistent") {
		t.Error("取消未知 ID 应返回 false")
	}
}

// TestScheduleCapsAt32Active 同时 32 个为上限：第 33 个报错「计时器已达上限」；
// 逐个取消后可继续调度。
func TestScheduleCapsAt32Active(t *testing.T) {
	rec := &fireRecorder{}
	store := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), rec.record)

	ids := make([]string, 0, maxActive)
	for i := 0; i < maxActive; i++ {
		st, err := store.Schedule(time.Hour, "占位", "group", 888, 999)
		if err != nil {
			t.Fatalf("第 %d 个 Schedule 应成功, got err: %v", i+1, err)
		}
		ids = append(ids, st.ID)
	}
	if store.Active() != maxActive {
		t.Fatalf("Active = %d, want %d", store.Active(), maxActive)
	}
	_, err := store.Schedule(time.Hour, "超了", "group", 888, 999)
	if err == nil {
		t.Fatal("第 33 个 Schedule 应报错")
	}
	if !regexp.MustCompile(`计时器已达上限`).MatchString(err.Error()) {
		t.Errorf("上限报错应含「计时器已达上限」, got %v", err)
	}
	for _, id := range ids {
		if ok := store.Cancel(id); !ok {
			t.Errorf("取消 %s 应成功", id)
		}
	}
	if store.Active() != 0 {
		t.Fatalf("全部取消后 Active = %d, want 0", store.Active())
	}
	// 释放后可再调度
	if _, err := store.Schedule(time.Hour, "又行了", "group", 888, 999); err != nil {
		t.Errorf("释放后 Schedule 应成功, got %v", err)
	}
}

// TestScheduleValidatesDelay delay ≤0 或 >7 天报错；恰好 7 天可调度。
func TestScheduleValidatesDelay(t *testing.T) {
	store := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), func(ScheduledTimer) {})

	for _, bad := range []time.Duration{0, -time.Second, 7*24*time.Hour + time.Nanosecond} {
		if _, err := store.Schedule(bad, "x", "group", 888, 999); err == nil {
			t.Errorf("delay=%v 应报错", bad)
		}
	}
	st, err := store.Schedule(7*24*time.Hour, "边界", "group", 888, 999)
	if err != nil {
		t.Fatalf("恰好 7 天应可调度, got err: %v", err)
	}
	store.Cancel(st.ID)
}

// TestConcurrentScheduleIsSafe 并发 Schedule 不丢记录、ID 唯一、全部到点触发。
func TestConcurrentScheduleIsSafe(t *testing.T) {
	rec := &fireRecorder{}
	store := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), rec.record)

	const n = 16
	ids := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st, err := store.Schedule(60*time.Millisecond, "并发", "group", 888, 999)
			if err != nil {
				t.Errorf("并发 Schedule #%d 报错: %v", i, err)
				return
			}
			ids <- st.ID
		}(i)
	}
	wg.Wait()
	close(ids)

	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Errorf("ID 重复: %s", id)
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Fatalf("应产生 %d 个唯一 ID, got %d", n, len(seen))
	}
	if store.Active() != n {
		t.Errorf("Active = %d, want %d", store.Active(), n)
	}
	waitFor(t, 3*time.Second, func() bool { return rec.count() == n }, "全部定时应到点触发")
	waitFor(t, 3*time.Second, func() bool { return store.Active() == 0 }, "触发完应清空")
}

// TestFirePanicDoesNotCrashProcess fire 回调 panic 被 goroutine recover，
// 进程存活、记录照常清理。
func TestFirePanicDoesNotCrashProcess(t *testing.T) {
	rec := &fireRecorder{}
	store := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), func(st ScheduledTimer) {
		rec.record(st)
		panic("fire 炸了")
	})

	if _, err := store.Schedule(30*time.Millisecond, "会炸的", "group", 888, 999); err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	time.Sleep(200 * time.Millisecond) // 若未 recover，整个测试进程直接崩溃
	if rec.count() != 1 {
		t.Fatalf("panic 的 fire 也应被调用一次, got %d", rec.count())
	}
	if store.Active() != 0 {
		t.Errorf("panic 后记录也应清理, Active = %d", store.Active())
	}
}
