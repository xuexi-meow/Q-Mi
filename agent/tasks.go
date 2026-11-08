// tasks.go 后台任务管理器（spec §2.5 方案 B）：Asyncable 工具以 {"_async":true}
// 调用时由 manager 把 Run 丢进本管理器后台执行，当轮立即返回任务回执，
// 完成后经回调把结果格式化为合成消息唤醒模型。任务进程内管理、重启清零；
// 单任务最长寿命 10 分钟（防僵尸 goroutine，超时判 failed）。
package agent

import (
	"context"
	"errors"
	"sync"
	"time"
)

var (
	errTaskNotFound   = errors.New("任务不存在")
	errTaskNotRunning = errors.New("任务已结束，无法取消")
)

// maxTaskLifetime 单个后台任务最长寿命。
const maxTaskLifetime = 10 * time.Minute

// 任务状态。
const (
	TaskRunning   = "running"
	TaskDone      = "done"
	TaskFailed    = "failed"
	TaskCancelled = "cancelled"
)

// BackgroundTask 一个后台任务的完整快照（List/Get 返回值；字段只增不改，
// 完成后 Result/Status 终态）。
type BackgroundTask struct {
	ID        int64  `json:"id"`
	Tool      string `json:"tool"`
	ArgsBrief string `json:"args_brief"` // 参数摘要（总览展示，调用方截断）
	// 任务来源会话：完成投递按此路由（同 fireTimer 的会话语义）
	ChatType string `json:"chat_type,omitempty"`
	ChatID   int64  `json:"chat_id"`
	SelfID   int64  `json:"self_id"`

	Status    string     `json:"status"`
	StartedAt time.Time  `json:"started_at"`
	Result    string     `json:"result,omitempty"`
	Err       string     `json:"err,omitempty"`
	DoneAt    *time.Time `json:"done_at,omitempty"`

	cancel context.CancelFunc
}

// TaskManager 后台任务注册表。并发安全；Start 的 run 闭包在独立 goroutine
// 内执行，ctx 带 10 分钟超时，turn 打断（turn ctx 取消）不影响后台任务。
type TaskManager struct {
	mu     sync.Mutex
	nextID int64
	tasks  map[int64]*BackgroundTask
	order  []int64 // 插入序（List 保序）
	onDone func(BackgroundTask)
}

// NewTaskManager 构造管理器。onDone 在任务完成/失败时回调（已取消的任务不
// 回调），由 manager 把结果投递给来源会话；可为 nil（测试）。
func NewTaskManager(onDone func(BackgroundTask)) *TaskManager {
	return &TaskManager{
		nextID: 1,
		tasks:  make(map[int64]*BackgroundTask),
		onDone: onDone,
	}
}

// Start 登记并启动一个后台任务。t 的 Tool/ArgsBrief/来源会话由调用方填好；
// run 在独立 goroutine 内执行，ctx 带 maxTaskLifetime 超时。返回任务 ID。
func (m *TaskManager) Start(t BackgroundTask, run func(ctx context.Context) (string, error)) int64 {
	ctx, cancel := context.WithTimeout(context.Background(), maxTaskLifetime)

	m.mu.Lock()
	t.ID = m.nextID
	m.nextID++
	t.Status = TaskRunning
	t.StartedAt = time.Now()
	t.cancel = cancel
	m.tasks[t.ID] = &t
	m.order = append(m.order, t.ID)
	id := t.ID
	m.mu.Unlock()

	go func() {
		result, runErr := run(ctx)
		m.finish(id, result, runErr, cancel)
	}()
	return id
}

// finish 记录终态；被取消的任务不覆盖 cancelled 状态、不回调。
func (m *TaskManager) finish(id int64, result string, runErr error, cancel context.CancelFunc) {
	m.mu.Lock()
	t, ok := m.tasks[id]
	if !ok || t.Status != TaskRunning {
		m.mu.Unlock()
		cancel()
		return
	}
	now := time.Now()
	t.DoneAt = &now
	t.Result = result
	if runErr != nil {
		t.Status = TaskFailed
		t.Err = runErr.Error()
	} else {
		t.Status = TaskDone
	}
	snapshot := *t
	m.mu.Unlock()

	cancel()
	if m.onDone != nil {
		m.onDone(snapshot)
	}
}

// List 任务快照，按启动时间正序（老→新）；status 非 "" 且非 "all" 时按状态过滤。
func (m *TaskManager) List(status string) []BackgroundTask {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]BackgroundTask, 0, len(m.order))
	for _, id := range m.order {
		t := *m.tasks[id]
		if status != "" && status != "all" && t.Status != status {
			continue
		}
		out = append(out, t)
	}
	return out
}

// Get 单任务快照；不存在返回 false。
func (m *TaskManager) Get(id int64) (BackgroundTask, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return BackgroundTask{}, false
	}
	return *t, true
}

// Cancel 取消一个 running 任务（status→cancelled、ctx 取消）；任务不存在或
// 已终态返回错误。被取消的任务不会回调 onDone（调用方同步知情）。
func (m *TaskManager) Cancel(id int64) (BackgroundTask, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.tasks[id]
	if !ok {
		return BackgroundTask{}, errTaskNotFound
	}
	if t.Status != TaskRunning {
		return *t, errTaskNotRunning
	}
	t.Status = TaskCancelled
	now := time.Now()
	t.DoneAt = &now
	if t.cancel != nil {
		t.cancel()
	}
	return *t, nil
}

// RunningCount 运行中任务数（概览/测试用）。
func (m *TaskManager) RunningCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, t := range m.tasks {
		if t.Status == TaskRunning {
			n++
		}
	}
	return n
}
