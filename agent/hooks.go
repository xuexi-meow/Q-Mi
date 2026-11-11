// hooks.go W1/W3 WebUI 的 agent 包导出钩子（spec §3.2）：薄封装包级单例，
// agent 未启用/未初始化时 nil 安全（返回空值或错误），供 webui 包调用。
// 写类钩子（NoteAdd/NoteDelete/MoodSet/Timer*）沿用 Wake 模式：manager 缺失
// 返回 ErrAgentNotEnabled；读类钩子（MoodGet/NotesList）沿用 W1 模式：直接
// 读默认路径文件，不触发 manager 惰性初始化。
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"Q-Mi/app"
)

// 钩子的错误语义（导出供 webui 区分「agent 未启用」与请求参数问题）。
var (
	ErrAgentNotEnabled = errors.New("agent 未启用")
	ErrSelfIDMissing   = errors.New("机器人 QQ 号未配置（bot.qq_number 为 0）")
)

// SetManagerForTest 跨包测试接缝：注入 manager 单例并返回恢复函数。
// 仅供 webui 等包的测试使用，生产代码禁止调用。
func SetManagerForTest(m *Manager) (restore func()) {
	managerMu.Lock()
	saved, savedInited := manager, managerInited
	manager, managerInited = m, true
	managerMu.Unlock()
	return func() {
		managerMu.Lock()
		manager, managerInited = saved, savedInited
		managerMu.Unlock()
	}
}

// EventsAfter 返回思考链事件环形缓冲中 ID > after 的事件（按序）。
// agent 未启用时返回 nil。
func EventsAfter(after uint64) []Event {
	m := inspectionManager()
	if m == nil {
		return nil
	}
	return m.events.After(after)
}

// Wake 把一条 [WebUI] 前缀消息注入 agent 的 self 队列（私聊自己、紧急）。
// agent 未启用或 selfID 未配置（qq_number=0）时返回错误。
func Wake(text string) error {
	m := getManager()
	if m == nil {
		return ErrAgentNotEnabled
	}
	if m.selfID <= 0 {
		return ErrSelfIDMissing
	}
	m.Enqueue(IncomingMessage{
		ChatType:   "private",
		ChatID:     m.selfID,
		SelfID:     m.selfID,
		SenderID:   m.selfID,
		SenderName: "WebUI",
		Text:       "[WebUI] " + text,
		Time:       time.Now(),
		Urgent:     false,
	})
	return nil
}

// MoodGet 读取情绪状态（agent/state.json）；文件缺失/损坏返回空串。
// 不走 manager（不触发惰性初始化），读默认路径。
func MoodGet() string {
	state, err := readStateFrom(defaultStatePath)
	if err != nil {
		return ""
	}
	return state.Mood
}

// NotesList 读取思考笔记（agent/notes.json）；文件缺失/损坏返回 nil。
// 不走 manager（不触发惰性初始化），读默认路径。
func NotesList() []Note {
	notes, err := LoadNotes(defaultNotesPath)
	if err != nil {
		return nil
	}
	return notes
}

// NoteAdd 追加一条思考笔记到 notes.json；agent 未启用返回错误。
func NoteAdd(text string) error {
	m := getManager()
	if m == nil {
		return ErrAgentNotEnabled
	}
	return AppendNote(m.notesPath, text)
}

// NoteDelete 按下标删除一条思考笔记并重写 notes.json；
// 下标越界（含负数）返回错误，文件不变。
func NoteDelete(index int) error {
	m := getManager()
	if m == nil {
		return ErrAgentNotEnabled
	}
	notes, err := LoadNotes(m.notesPath)
	if err != nil {
		return err
	}
	if index < 0 || index >= len(notes) {
		return fmt.Errorf("笔记下标越界: %d（共 %d 条）", index, len(notes))
	}
	notes = append(notes[:index], notes[index+1:]...)
	return saveNotes(m.notesPath, notes)
}

// saveNotes 全量重写笔记文件（NoteDelete 用，与 AppendNote 落盘规则一致）。
func saveNotes(path string, notes []Note) error {
	data, err := json.Marshal(notes)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// MoodSet 写入情绪状态到 state.json；agent 未启用返回错误。
func MoodSet(mood string) error {
	m := getManager()
	if m == nil {
		return ErrAgentNotEnabled
	}
	return writeState(m.statePath, mood)
}

// TimerList 待触发计时器快照（按触发时间正序）；agent 未启用返回 nil。
func TimerList() []ScheduledTimer {
	m := inspectionManager()
	if m == nil || m.timerStore == nil {
		return nil
	}
	return m.timerStore.List()
}

func inspectionManager() *Manager {
	managerMu.Lock()
	defer managerMu.Unlock()
	return manager
}

func RuntimeInspect() RuntimeInspection {
	if m := inspectionManager(); m != nil {
		return m.InspectRuntime()
	}
	phase := "uninitialized"
	if a := app.GetApp(); a != nil {
		if cfg := a.Current(); cfg != nil && (cfg.Agent == nil || !cfg.Agent.Enable) {
			phase = "disabled"
		}
	}
	return RuntimeInspection{Phase: phase}
}

func ContextInspect(q ContextQuery) (ContextPage, error) {
	m := inspectionManager()
	if m == nil {
		m = &Manager{}
	}
	return m.InspectContext(q)
}

func ContextEntryInspect(source string, version uint64, index int) (InspectionEntry, error) {
	m := inspectionManager()
	if m == nil {
		return InspectionEntry{}, ErrInspectionIndex
	}
	return m.InspectEntry(source, version, index)
}

func TaskInspect(limit int) []TaskInspection {
	out := []TaskInspection{}
	m := inspectionManager()
	if m == nil || m.tasks == nil {
		return out
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	tasks := m.tasks.List("")
	for i := len(tasks) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, taskInspection(tasks[i]))
	}
	return out
}

// TimerCount 在跑计时器数量（agent 未启用为 0）。
func TimerCount() int {
	return len(TimerList())
}

// TimerSchedule 定一个 WebUI 触发的定时提醒：挂在 self 会话队列
// （private+selfID），到点以 [定时提醒] 注入，回复给谁由模型自决。
// 返回计时器 ID；agent 未启用 / selfID 未配置 / 延迟非法（≤0 或超 7 天）
// 返回错误。
func TimerSchedule(delaySec int, note string) (string, error) {
	m := getManager()
	if m == nil || m.timerStore == nil {
		return "", ErrAgentNotEnabled
	}
	if m.selfID <= 0 {
		return "", ErrSelfIDMissing
	}
	rec, err := m.timerStore.Schedule(time.Duration(delaySec)*time.Second, note,
		"private", m.selfID, m.selfID)
	if err != nil {
		return "", err
	}
	return rec.ID, nil
}

// TimerCancel 取消一个未触发的计时器；不存在（已触发/已取消/未知 ID）返回错误。
func TimerCancel(id string) error {
	m := getManager()
	if m == nil || m.timerStore == nil {
		return ErrAgentNotEnabled
	}
	if !m.timerStore.Cancel(id) {
		return fmt.Errorf("计时器不存在或已触发: %s", id)
	}
	return nil
}
