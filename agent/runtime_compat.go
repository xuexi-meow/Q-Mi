package agent

import agentruntime "Q-Mi/agent/runtime"

type ScheduledTimer = agentruntime.ScheduledTimer
type TimerStore = agentruntime.TimerStore
type BackgroundTask = agentruntime.BackgroundTask
type TaskManager = agentruntime.TaskManager
type Event = agentruntime.Event
type RunToolCall = agentruntime.RunToolCall
type RunUpdate = agentruntime.RunUpdate
type EventLog = agentruntime.EventLog
type Approval = agentruntime.Approval
type ApprovalManager = agentruntime.ApprovalManager

const (
	maxEventText = agentruntime.MaxEventText

	TaskRunning   = agentruntime.TaskRunning
	TaskDone      = agentruntime.TaskDone
	TaskFailed    = agentruntime.TaskFailed
	TaskCancelled = agentruntime.TaskCancelled

	KindTurnStart   = agentruntime.KindTurnStart
	KindReasoning   = agentruntime.KindReasoning
	KindOutput      = agentruntime.KindOutput
	KindToolCall    = agentruntime.KindToolCall
	KindToolResult  = agentruntime.KindToolResult
	KindReply       = agentruntime.KindReply
	KindError       = agentruntime.KindError
	KindFinish      = agentruntime.KindFinish
	KindInterrupted = agentruntime.KindInterrupted
	KindUsage       = agentruntime.KindUsage
	KindConfig      = agentruntime.KindConfig
	KindModelStream = agentruntime.KindModelStream

	ApprovalPending  = agentruntime.ApprovalPending
	ApprovalApproved = agentruntime.ApprovalApproved
	ApprovalRejected = agentruntime.ApprovalRejected
)

func NewTimerStore(path string, fire func(ScheduledTimer)) *TimerStore {
	return agentruntime.NewTimerStore(path, fire)
}

func NewTaskManager(onDone func(BackgroundTask)) *TaskManager {
	return agentruntime.NewTaskManager(onDone)
}

func NewEventLog() *EventLog { return agentruntime.NewEventLog() }

func NewApprovalManager(notify func(string), settled func(Approval, string, error)) *ApprovalManager {
	return agentruntime.NewApprovalManager(notify, settled)
}

func parseApprovalReply(text string) (int64, bool, bool) {
	return agentruntime.ParseApprovalReply(text)
}
