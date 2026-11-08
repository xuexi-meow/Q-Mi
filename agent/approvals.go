// approvals.go 危险/越界操作的审批流（spec 权限模型）：工具 Run 返回
// approvalNeeded 信号时，execToolCall 把操作登记进 ApprovalManager 并通知
// 管理员 QQ；管理员回复「同意 <id>」→ 后台执行并投递结果，「拒绝 <id>」→
// 投递拒绝回执。审批任务不阻塞发起 turn（与后台任务同语义）。
package agent

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ApprovalStatus 审批单状态。
const (
	ApprovalPending  = "pending"
	ApprovalApproved = "approved"
	ApprovalRejected = "rejected"
)

// Approval 一张审批单快照。
type Approval struct {
	ID        int64     `json:"id"`
	Tool      string    `json:"tool"`
	Args      string    `json:"args"`   // 原始参数 JSON（展示用）
	Reason    string    `json:"reason"` // 需要审批的原因
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`

	// 来源会话：结果投递按此路由
	ChatType string `json:"chat_type,omitempty"`
	ChatID   int64  `json:"chat_id"`
	SelfID   int64  `json:"self_id"`

	run func(approved bool) (string, error) // 审批后的执行闭包（捕获Approved ctx）
}

// ApprovalManager 审批单注册表。notify 把审批请求文本发给管理员 QQ
// （manager 注入，走 bot.Reply 私聊）；onSettled 在同意执行完成/拒绝时回调
// （manager 格式化投递回来源会话）。
type ApprovalManager struct {
	mu        sync.Mutex
	nextID    int64
	pending   map[int64]*Approval
	notify    func(text string)
	onSettled func(Approval, string, error) // (单子, 结果文本, 执行错误；拒绝时文本为空)
}

// NewApprovalManager 构造审批管理器。notify/onSettled 可为 nil（测试）。
func NewApprovalManager(notify func(string), onSettled func(Approval, string, error)) *ApprovalManager {
	return &ApprovalManager{
		nextID:    1,
		pending:   make(map[int64]*Approval),
		notify:    notify,
		onSettled: onSettled,
	}
}

// Request 登记一张审批单并通知管理员，返回单号。同一时间同一参数的重复
// 请求不去重（管理员看到两条就批两次，模型侧自行避免）。
func (m *ApprovalManager) Request(a Approval) (int64, error) {
	m.mu.Lock()
	a.ID = m.nextID
	m.nextID++
	a.Status = ApprovalPending
	a.CreatedAt = time.Now()
	m.pending[a.ID] = &a
	id := a.ID
	m.mu.Unlock()

	if m.notify != nil {
		args := a.Args
		if len(args) > 300 {
			args = args[:300] + "…(截断)"
		}
		m.notify(fmt.Sprintf("[操作审批 #%d] %s\n原因: %s\n参数: %s\n回复「同意 %d」执行，「拒绝 %d」取消",
			a.ID, a.Tool, a.Reason, args, a.ID, a.ID))
	}
	return id, nil
}

// Resolve 管理员裁决。同意 → 异步执行 run(true)，结果经 onSettled 投递；
// 拒绝 → 立即 onSettled(approval, "")。单不存在/已裁决返回错误。
func (m *ApprovalManager) Resolve(id int64, approved bool) error {
	m.mu.Lock()
	a, ok := m.pending[id]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("没有找到审批单 %d", id)
	}
	if a.Status != ApprovalPending {
		m.mu.Unlock()
		return fmt.Errorf("审批单 %d 已处理（%s）", id, a.Status)
	}
	now := time.Now()
	if approved {
		a.Status = ApprovalApproved
	} else {
		a.Status = ApprovalRejected
	}
	_ = now
	snapshot := *a
	delete(m.pending, id)
	run := a.run
	m.mu.Unlock()

	if !approved {
		if m.onSettled != nil {
			m.onSettled(snapshot, "", nil)
		}
		return nil
	}
	if run == nil {
		return nil
	}
	// 异步执行，与后台任务同语义：不阻塞管理员的会话
	go func() {
		result, err := run(true)
		if m.onSettled != nil {
			m.onSettled(snapshot, result, err)
		}
	}()
	return nil
}

// PendingCount 待审批数量（概览/测试用）。
func (m *ApprovalManager) PendingCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.pending)
}

// normalizeApprovalText 审批回复解析：支持「同意 1」「拒绝 1」（大小写/
// 全角空格容错）。返回 (id, approved, ok)。
func parseApprovalReply(text string) (int64, bool, bool) {
	t := strings.TrimSpace(strings.ReplaceAll(text, "　", " "))
	for _, kw := range []string{"同意", "approve"} {
		if strings.HasPrefix(strings.ToLower(t), kw) {
			var id int64
			if _, err := fmt.Sscanf(strings.TrimSpace(t[len(kw):]), "%d", &id); err == nil {
				return id, true, true
			}
		}
	}
	for _, kw := range []string{"拒绝", "reject"} {
		if strings.HasPrefix(strings.ToLower(t), kw) {
			var id int64
			if _, err := fmt.Sscanf(strings.TrimSpace(t[len(kw):]), "%d", &id); err == nil {
				return id, false, true
			}
		}
	}
	return 0, false, false
}
