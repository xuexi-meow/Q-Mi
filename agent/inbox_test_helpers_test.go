package agent

import (
	"testing"

	agentruntime "Q-Mi/agent/runtime"
)

// newCapturedInbox 避免在事件接线测试里启动真实 worker。
func newCapturedInbox(t *testing.T) *accountInbox {
	t.Helper()
	inbox := agentruntime.NewInbox()
	t.Cleanup(inbox.Close)
	return inbox
}
