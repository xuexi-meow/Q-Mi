package tool

// ApprovalRequired 由工具发出的待审批信号，执行层负责通知与重新执行。
type ApprovalRequired struct{ Reason string }

func (e ApprovalRequired) Error() string { return e.Reason }
