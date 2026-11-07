// hooks_test.go W3 WebUI agent 控制钩子（spec §3.2）：笔记增删、情绪写入、
// 计时器列表/排期/取消。注入模式沿用 W1 事件钩子测试（manager 单例直换）。
package agent

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// hookManager 构造只含钩子所需字段的最小 Manager：临时目录路径、selfID=999。
// 不启动 worker/LLM，钩子本身不依赖这些。
func hookManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	return &Manager{
		selfID:     999,
		notesPath:  filepath.Join(dir, "notes.json"),
		statePath:  filepath.Join(dir, "state.json"),
		timerStore: NewTimerStore(filepath.Join(dir, "timers.json"), nil),
	}
}

// withHookManager 把 manager 注入包级单例并在测试结束恢复。
// 注入（managerInited=true）避免 getManager 惰性初始化读到真实配置。
func withHookManager(t *testing.T, m *Manager) {
	t.Helper()
	managerMu.Lock()
	saved, savedInited := manager, managerInited
	manager, managerInited = m, true
	managerMu.Unlock()
	t.Cleanup(func() {
		managerMu.Lock()
		manager, managerInited = saved, savedInited
		managerMu.Unlock()
	})
}

// TestNoteAddDeleteHooks NoteAdd 追加、NoteDelete 按下标删除并重写文件；
// 越界（含负数）报错且文件不变。
func TestNoteAddDeleteHooks(t *testing.T) {
	m := hookManager(t)
	withHookManager(t, m)

	if err := NoteAdd("第一条"); err != nil {
		t.Fatalf("NoteAdd: %v", err)
	}
	if err := NoteAdd("第二条"); err != nil {
		t.Fatalf("NoteAdd: %v", err)
	}
	notes, err := LoadNotes(m.notesPath)
	if err != nil {
		t.Fatalf("LoadNotes: %v", err)
	}
	if len(notes) != 2 || notes[0].Text != "第一条" || notes[1].Text != "第二条" {
		t.Fatalf("笔记内容 = %+v, want [第一条 第二条]", notes)
	}

	if err := NoteDelete(0); err != nil {
		t.Fatalf("NoteDelete(0): %v", err)
	}
	notes, err = LoadNotes(m.notesPath)
	if err != nil {
		t.Fatalf("LoadNotes: %v", err)
	}
	if len(notes) != 1 || notes[0].Text != "第二条" {
		t.Fatalf("删除首条后 = %+v, want [第二条]", notes)
	}

	// 越界报错，文件不变
	before, _ := LoadNotes(m.notesPath)
	if err := NoteDelete(1); err == nil {
		t.Error("NoteDelete(1)（越界）应报错")
	}
	if err := NoteDelete(-1); err == nil {
		t.Error("NoteDelete(-1) 应报错")
	}
	after, err := LoadNotes(m.notesPath)
	if err != nil {
		t.Fatalf("LoadNotes: %v", err)
	}
	if len(after) != len(before) || after[0].Text != before[0].Text {
		t.Errorf("越界删除不应改动文件: before=%+v after=%+v", before, after)
	}
}

// TestMoodSetHook MoodSet 写 state.json（走 manager 的 statePath）。
func TestMoodSetHook(t *testing.T) {
	m := hookManager(t)
	withHookManager(t, m)

	if err := MoodSet("有点困"); err != nil {
		t.Fatalf("MoodSet: %v", err)
	}
	st, err := readStateFrom(m.statePath)
	if err != nil {
		t.Fatalf("readStateFrom: %v", err)
	}
	if st.Mood != "有点困" {
		t.Errorf("state.Mood = %q, want 有点困", st.Mood)
	}
}

// TestHooksAgentDisabled agent 未启用（manager 为 nil）时写类钩子返回
// ErrAgentNotEnabled，列表类返回空，不 panic、不碰默认路径文件。
func TestHooksAgentDisabled(t *testing.T) {
	withHookManager(t, nil)

	if err := NoteAdd("x"); !errors.Is(err, ErrAgentNotEnabled) {
		t.Errorf("NoteAdd err = %v, want ErrAgentNotEnabled", err)
	}
	if err := NoteDelete(0); !errors.Is(err, ErrAgentNotEnabled) {
		t.Errorf("NoteDelete err = %v, want ErrAgentNotEnabled", err)
	}
	if err := MoodSet("开心"); !errors.Is(err, ErrAgentNotEnabled) {
		t.Errorf("MoodSet err = %v, want ErrAgentNotEnabled", err)
	}
	if _, err := TimerSchedule(60, "x"); !errors.Is(err, ErrAgentNotEnabled) {
		t.Errorf("TimerSchedule err = %v, want ErrAgentNotEnabled", err)
	}
	if err := TimerCancel("deadbeef"); !errors.Is(err, ErrAgentNotEnabled) {
		t.Errorf("TimerCancel err = %v, want ErrAgentNotEnabled", err)
	}
	if list := TimerList(); list != nil {
		t.Errorf("TimerList 应为 nil, got %+v", list)
	}
	if got := TimerCount(); got != 0 {
		t.Errorf("TimerCount = %d, want 0", got)
	}
}

// TestTimerListSortedByFireAt TimerList 返回按 FireAt 正序快照，TimerCount 计数一致。
func TestTimerListSortedByFireAt(t *testing.T) {
	m := hookManager(t)
	withHookManager(t, m)

	if _, err := TimerSchedule(3*3600, "三小时"); err != nil {
		t.Fatalf("TimerSchedule: %v", err)
	}
	if _, err := TimerSchedule(3600, "一小时"); err != nil {
		t.Fatalf("TimerSchedule: %v", err)
	}
	if _, err := TimerSchedule(7200, "两小时"); err != nil {
		t.Fatalf("TimerSchedule: %v", err)
	}

	list := TimerList()
	if len(list) != 3 {
		t.Fatalf("TimerList 长度 = %d, want 3", len(list))
	}
	wantOrder := []string{"一小时", "两小时", "三小时"}
	for i, w := range wantOrder {
		if list[i].Note != w {
			t.Errorf("list[%d].Note = %q, want %q（应按 FireAt 正序）", i, list[i].Note, w)
		}
		if i > 0 && !list[i-1].FireAt.Before(list[i].FireAt) {
			t.Errorf("FireAt 未正序: list[%d]=%v list[%d]=%v", i-1, list[i-1].FireAt, i, list[i].FireAt)
		}
	}
	if got := TimerCount(); got != 3 {
		t.Errorf("TimerCount = %d, want 3", got)
	}
}

// TestTimerScheduleSelfSession WebUI 排的计时器挂在 self 会话（private+selfID），
// note 原样透传；延迟 0 与超 7 天报错，7 天整可排。
func TestTimerScheduleSelfSession(t *testing.T) {
	m := hookManager(t)
	withHookManager(t, m)

	id, err := TimerSchedule(60, "喝水")
	if err != nil {
		t.Fatalf("TimerSchedule: %v", err)
	}
	if id == "" {
		t.Fatal("TimerSchedule 应返回非空 ID")
	}
	list := TimerList()
	if len(list) != 1 {
		t.Fatalf("TimerList 长度 = %d, want 1", len(list))
	}
	rec := list[0]
	if rec.ID != id || rec.Note != "喝水" ||
		rec.ChatType != "private" || rec.ChatID != 999 || rec.SelfID != 999 {
		t.Errorf("记录 = %+v, want private/999/999 note=喝水 id=%s", rec, id)
	}
	if rec.FireAt.IsZero() {
		t.Error("FireAt 应为排期时刻")
	}

	if _, err := TimerSchedule(0, "x"); err == nil {
		t.Error("delay=0 应报错")
	}
	if _, err := TimerSchedule(-5, "x"); err == nil {
		t.Error("delay<0 应报错")
	}
	if _, err := TimerSchedule(7*24*3600+1, "x"); err == nil {
		t.Error("delay 超过 7 天应报错")
	}
	if _, err := TimerSchedule(7*24*3600, "七天整"); err != nil {
		t.Errorf("delay=7 天整应可排, got %v", err)
	}
}

// TestTimerScheduleSelfIDMissing manager 存在但 selfID 未配置（0）时报错。
func TestTimerScheduleSelfIDMissing(t *testing.T) {
	m := hookManager(t)
	m.selfID = 0
	withHookManager(t, m)

	if _, err := TimerSchedule(60, "x"); !errors.Is(err, ErrSelfIDMissing) {
		t.Errorf("TimerSchedule err = %v, want ErrSelfIDMissing", err)
	}
}

// TestTimerCancelHook 取消存在的计时器成功且列表清空；未知 ID 报错。
func TestTimerCancelHook(t *testing.T) {
	m := hookManager(t)
	withHookManager(t, m)

	id, err := TimerSchedule(3600, "待取消")
	if err != nil {
		t.Fatalf("TimerSchedule: %v", err)
	}
	if err := TimerCancel(id); err != nil {
		t.Fatalf("TimerCancel(%s): %v", id, err)
	}
	if got := TimerCount(); got != 0 {
		t.Errorf("取消后 TimerCount = %d, want 0", got)
	}
	if err := TimerCancel("deadbeef"); err == nil {
		t.Error("取消未知 ID 应报错")
	}
}

// TestTimerStoreListEmpty TimerStore.List 对空存储返回空切片（非 nil JSON 友好）。
func TestTimerStoreListEmpty(t *testing.T) {
	s := NewTimerStore(filepath.Join(t.TempDir(), "timers.json"), nil)
	list := s.List()
	if list == nil || len(list) != 0 {
		t.Errorf("空存储 List = %+v, want 空非 nil 切片", list)
	}
	_ = time.Now // 保持 import
}
