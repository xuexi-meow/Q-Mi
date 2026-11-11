package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSendMessageRejectsContextEnvelope(t *testing.T) {
	napcat := startFakeNapCat(t)
	tools, _, _ := testToolSet(t, nil)
	send := findTool(t, tools, "send_message")
	for _, text := range []string{
		"[11-08 18:04] [群:888] <我> 嗷",
		"[18:04] [群:888] <我> 嗷",
		"[11-08 18:04] [私聊:123] <我> 在",
		"[群:888] <我> 在",
		"<我> 在",
		"&#91;18:04&#93; &#91;群:888&#93; &lt;我&gt; 嗷",
		"正常的一行\n[11-08 18:04] [群:888] <我> 嗷",
		"<context>这条可以轻松接一句。</context>",
		"<think>先看看要不要回复</think>",
		"<analysis>\n不需要回\n</analysis>",
		"[思考笔记]\n以后少说话",
		"[当前状态快照]\n内部笔记",
		"[/当前状态快照]",
		"[工作上下文摘要]\n未完成研究",
		"[/工作上下文摘要]",
		"当前情绪状态: 开心",
		"当前时间: 2026-11-09 00:15\n在吗",
		"[定时轮询] 例行醒来",
		`{"kind":"qq_message","chat_type":"group","chat_id":888,"user_id":999,"message_id":80,"text":"嗷"}`,
	} {
		t.Run(text, func(t *testing.T) {
			for _, rich := range []bool{false, true} {
				args := map[string]any{"chat_type": "group", "chat_id": 888}
				if rich {
					args["segments"] = []segmentParam{
						{Type: "at", Data: map[string]any{"qq": "123"}},
						{Type: "text", Data: map[string]any{"text": text}},
					}
				} else {
					args["text"] = text
				}
				data, err := json.Marshal(args)
				if err != nil {
					t.Fatal(err)
				}
				before := napcat.count()
				_, err = send.Run(data)
				if err == nil || !strings.Contains(err.Error(), "正文") {
					t.Errorf("rich=%v: 应返回正文格式错误, got %v", rich, err)
				}
				if got := napcat.count(); got != before {
					t.Errorf("rich=%v: 被拒绝的正文不应发送, 请求数 %d -> %d", rich, before, got)
				}
			}
		})
	}
}

func TestSendMessageRejectsEnvelopeSplitAcrossTextSegments(t *testing.T) {
	napcat := startFakeNapCat(t)
	tools, _, _ := testToolSet(t, nil)
	_, err := findTool(t, tools, "send_message").Run(json.RawMessage(
		`{"chat_type":"private","chat_id":123,"segments":[` +
			`{"type":"text","data":{"text":"[11-08 18:04] "}},` +
			`{"type":"text","data":{"text":"[私聊:123] <我> 在"}}]}`))
	if err == nil {
		t.Error("拆成多个 text 段的上下文前缀也应拒绝")
	}
	if got := napcat.count(); got != 0 {
		t.Errorf("不应发送任何消息, got %d", got)
	}
}

func TestSendMessagePreservesChatText(t *testing.T) {
	napcat := startFakeNapCat(t)
	tools, _, _ := testToolSet(t, nil)
	for _, text := range []string{
		"这也太离谱了",
		"18:04 再看吧",
		"[18:04] 开始测试",
		"数组下标是 [0]，比较符号是 < 和 >",
		"这是我的 context，不是标签",
		"> [11-08 18:04] [群:888] <我> 这是一段引用",
		"示例日志：\n```text\n[11-08 18:04] [群:888] <我> 在\n```",
		"~~~html\n<context>示例标签</context>\n~~~",
		"文档里提到 `<context>` 标签",
		"不需要把 &lt; 和 &gt; 解码后发出去",
		`{"chat_id":888,"text":"ordinary application JSON"}`,
		"```json\n{\"kind\":\"qq_message\",\"chat_type\":\"group\",\"chat_id\":888,\"user_id\":999,\"text\":\"example\"}\n```",
	} {
		t.Run(text, func(t *testing.T) {
			data, err := json.Marshal(map[string]any{
				"chat_type": "group", "chat_id": 888, "text": text,
			})
			if err != nil {
				t.Fatal(err)
			}
			before := napcat.count()
			runToolOK(t, tools, "send_message", string(data))
			if got := napcat.sentText(t, before); got != text {
				t.Errorf("正常正文应原样保留: got %q, want %q", got, text)
			}
		})
	}
}

func TestPlainTextCompatRejectsContextEnvelope(t *testing.T) {
	m, _, napcat := newLoopManager(t, testCfg(time.Millisecond), &stubBuilder{}, nil, []string{
		`{"choices":[{"message":{"role":"assistant","content":"[11-08 18:04] [群:888] <我> 嗷"}}]}`,
	})
	m.handleTurn(&Turn{ChatType: "group", ChatID: 888, SelfID: 999})
	if got := napcat.count(); got != 0 {
		t.Errorf("纯文本兼容路径也不应发送上下文前缀, got %d", got)
	}
	events := m.events.After(0)
	if len(events) == 0 || events[len(events)-1].Kind != KindError {
		t.Errorf("拦截后应记录错误事件, got %+v", events)
	}
}

func TestThinkLoopRejectsEnvelopeAndLetsModelRetry(t *testing.T) {
	m, f, napcat := newLoopManager(t, testCfg(time.Millisecond), &stubBuilder{}, nil, []string{
		toolCallResp("bad", "send_message", `{"chat_type":"group","chat_id":888,"text":"[11-08 18:04] [群:888] <我> 嗷"}`),
		toolCallResp("good", "send_message", `{"chat_type":"group","chat_id":888,"text":"嗷"}`),
		toolCallResp("done", "finish", ""),
	})
	m.handleTurn(&Turn{ChatType: "group", ChatID: 888, SelfID: 999})
	if got := napcat.count(); got != 1 {
		t.Fatalf("只应发出修正后的消息, got %d", got)
	}
	if got := napcat.sentText(t, 0); got != "嗷" {
		t.Errorf("实际发送正文 = %q", got)
	}
	last := f.reqs[1].Messages[len(f.reqs[1].Messages)-1]
	if last.Role != "tool" || last.ToolCallID != "bad" || !strings.Contains(contentText(last), "error") {
		t.Errorf("格式错误应回填模型供修正, got %+v", last)
	}
}
