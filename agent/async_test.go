package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestSameRoundToolCallsRunConcurrently 同轮多工具并发执行：两个慢 poke 的
// fake NapCat 处理期间峰值并发 ≥ 2；结果仍按调用顺序回填。
func TestSameRoundToolCallsRunConcurrently(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	script := []string{
		twoToolCallResp("c1", "poke", `{"chat_type":"group","chat_id":888,"user_id":10001}`,
			"c2", "poke", `{"chat_type":"group","chat_id":888,"user_id":10002}`),
		toolCallResp("c3", "finish", ""),
	}
	m, f, _ := newLoopManager(t, cfg, b, nil, script)

	var mu sync.Mutex
	active, peak := 0, 0
	startP3NapCat(t, func(string, map[string]any) any {
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		mu.Unlock()
		time.Sleep(80 * time.Millisecond)
		mu.Lock()
		active--
		mu.Unlock()
		return map[string]any{}
	})

	m.Enqueue(msg("group", 888, "戳他们俩"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 2 }, "并发执行后应继续 finish 轮")

	mu.Lock()
	p := peak
	mu.Unlock()
	if p < 2 {
		t.Fatalf("峰值并发 = %d, want ≥ 2（同轮工具应并发执行）", p)
	}
	// 第 2 轮请求的 tool 消息按 c1→c2 顺序回填
	var ids []string
	for _, msg := range f.reqs[1].Messages {
		if msg.Role == "tool" {
			ids = append(ids, msg.ToolCallID)
		}
	}
	if len(ids) != 2 || ids[0] != "c1" || ids[1] != "c2" {
		t.Errorf("tool 消息顺序 = %v, want [c1 c2]", ids)
	}
}

// TestAsyncToolReceiptAndDelivery _async 调用立即返回任务回执（不阻塞
// turn/finish）；任务完成后合成 [后台任务] 消息唤醒新 turn。
func TestAsyncToolReceiptAndDelivery(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	script := []string{
		toolCallResp("c1", "upload_group_file", `{"_async":true,"group_id":888,"file":"/tmp/x.zip","name":"x.zip"}`),
		toolCallResp("c2", "finish", ""),
		// 完成投递唤醒的第三轮
		toolCallResp("c3", "finish", ""),
	}
	m, f, _ := newLoopManager(t, cfg, b, nil, script)

	release := make(chan struct{})
	startP3NapCat(t, func(path string, _ map[string]any) any {
		if path == "/upload_group_file" {
			<-release // 阻塞任务直到测试放行，保证时序可控
		}
		return map[string]any{}
	})

	m.Enqueue(msg("group", 888, "传个文件"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 2 }, "回执轮 + finish 轮（不被后台任务阻塞）")

	// 回执：task_id/status/note，且不含上传结果（请求1的消息数组里）
	toolMsgs := f.reqs[1].Messages
	receipt := contentText(toolMsgs[len(toolMsgs)-1])
	if !strings.Contains(receipt, `"task_id":1`) || !strings.Contains(receipt, `"status":"started"`) {
		t.Errorf("回执不符: %s", receipt)
	}

	close(release) // 任务完成 → 投递唤醒
	waitFor(t, 5*time.Second, func() bool { return f.count() == 3 }, "任务完成应唤醒第三轮")

	turn1 := b.turn(1)
	if len(turn1.NewMessages) == 0 || !strings.Contains(turn1.NewMessages[0].Text, "[后台任务 #1] upload_group_file 完成") {
		t.Errorf("投递行缺失: %+v", turn1.NewMessages)
	}
}

// TestAsyncRejectedForNonAsyncableTool 非 Asyncable 工具带 _async → 报错回填。
func TestAsyncRejectedForNonAsyncableTool(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	script := []string{
		toolCallResp("c1", "think", `{"_async":true,"note":"x"}`),
		toolCallResp("c2", "finish", ""),
	}
	m, f, _ := newLoopManager(t, cfg, b, nil, script)
	startFakeNapCat(t)

	m.Enqueue(msg("group", 888, "试试异步记笔记"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 2 }, "报错后应继续 finish 轮")

	toolMsgs := f.reqs[1].Messages
	out := contentText(toolMsgs[len(toolMsgs)-1])
	if !strings.Contains(out, "不支持 _async") {
		t.Errorf("应报错回填「不支持 _async」, got %s", out)
	}
}

// TestTaskCancelStopsDelivery 后台任务被 task_cancel 后：状态 cancelled、
// 结果不再投递（不唤醒新 turn）。
func TestTaskCancelStopsDelivery(t *testing.T) {
	b := &stubBuilder{}
	cfg := testCfg(20 * time.Millisecond)
	script := []string{
		toolCallResp("c1", "upload_group_file", `{"_async":true,"group_id":888,"file":"/tmp/x.zip","name":"x.zip"}`),
		toolCallResp("c2", "task_cancel", `{"task_id":1}`),
		toolCallResp("c3", "finish", ""),
	}
	m, f, _ := newLoopManager(t, cfg, b, nil, script)

	release := make(chan struct{})
	startP3NapCat(t, func(path string, _ map[string]any) any {
		if path == "/upload_group_file" {
			<-release
		}
		return map[string]any{}
	})

	m.Enqueue(msg("group", 888, "传一半不想传了"))
	waitFor(t, 5*time.Second, func() bool { return f.count() == 3 }, "取消轮 + finish 轮")

	close(release) // 任务即使最终跑完，也不得投递
	time.Sleep(150 * time.Millisecond)
	if got := f.count(); got != 3 {
		t.Errorf("取消后不应唤醒投递, LLM 请求数 = %d, want 3", got)
	}
	// 状态机：取消即终态
	snap, ok := m.tasks.Get(1)
	if !ok || snap.Status != TaskCancelled {
		t.Errorf("任务状态 = %+v, want cancelled", snap)
	}
}

// TestTaskToolsListGet task_list/task_get 的列表与详情（含结果全文）。
func TestTaskToolsListGet(t *testing.T) {
	tm := NewTaskManager(nil)
	done := make(chan struct{})
	tm.Start(BackgroundTask{Tool: "upload_group_file", ArgsBrief: `{"group_id":1}`}, func(context.Context) (string, error) {
		<-done
		return `{"ok":true}`, nil
	})
	ctx := &ToolContext{Tasks: tm}
	tools := buildTools(ctx)

	// running 态列表
	running := runToolOK(t, tools, "task_list", `{"status":"running"}`)
	if !strings.Contains(running, `"tool":"upload_group_file"`) || !strings.Contains(running, `"status":"running"`) {
		t.Errorf("running 列表不符: %s", running)
	}
	// 完成后 task_get 拿全文
	close(done)
	waitFor(t, 2*time.Second, func() bool { return tm.RunningCount() == 0 }, "任务应完成")
	detail := runToolOK(t, tools, "task_get", `{"task_id":1}`)
	if !strings.Contains(detail, `"status":"done"`) || !strings.Contains(detail, `result`) {
		t.Errorf("详情不符: %s", detail)
	}
	// 未知任务报错
	if err := runToolErr(t, tools, "task_get", `{"task_id":99}`); !strings.Contains(err.Error(), "没有找到任务 99") {
		t.Errorf("未知任务应报错: %v", err)
	}
}

// TestSelfEventsTool self_events 按新→旧、kind/chat_id 过滤、limit 生效。
func TestSelfEventsTool(t *testing.T) {
	events := NewEventLog()
	events.Record("group", 888, KindToolCall, "poke {\"group_id\":888}")
	events.Record("group", 888, KindReasoning, "想想先戳谁")
	events.Record("private", 20001, KindReply, "私聊回复")
	events.Record("group", 888, KindFinish, "结束思考")

	ctx := &ToolContext{Events: events}
	tools := buildTools(ctx)

	all := runToolOK(t, tools, "self_events", `{}`)
	got := strings.Count(all, "\n") + 1
	if got != 4 {
		t.Fatalf("应返回 4 条, got %d: %s", got, all)
	}
	if !strings.HasPrefix(all, fmt.Sprintf("#4")) { // 新→旧
		t.Errorf("应以最新事件开头: %s", all)
	}
	if s := runToolOK(t, tools, "self_events", `{"kind":"reasoning"}`); !strings.Contains(s, "想想先戳谁") || strings.Contains(s, "私聊回复") {
		t.Errorf("kind 过滤不符: %s", s)
	}
	if s := runToolOK(t, tools, "self_events", `{"chat_id":20001}`); !strings.Contains(s, "私聊回复") || strings.Contains(s, "想想先戳谁") {
		t.Errorf("chat_id 过滤不符: %s", s)
	}
	if s := runToolOK(t, tools, "self_events", `{"limit":2}`); strings.Count(s, "\n")+1 != 2 {
		t.Errorf("limit=2 应只返回 2 条: %s", s)
	}
}
