// polling_test.go P3.6 定时轮询测试：ParseSelfWake 配置解析、
// StartSelfWake 端到端唤醒（真实 manager + 假 LLM）、关闭/未配置 QQ 的负例。
package agent

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"Q-Mi/utils/config"
)

// TestParseSelfWake 配置值 → 轮询间隔的纯转换：
// ""/"off"/"0" → (0,false)（false = 关闭，或由调用方给默认值）；
// 其余 ParseDuration 成功即 (d,true)。≥30s 下限由 config 层校验，这里不挡。
func TestParseSelfWake(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"", 0, false},
		{"off", 0, false},
		{"0", 0, false},
		{"1h", time.Hour, true},
		{"10m", 10 * time.Minute, true},
		{"90s", 90 * time.Second, true},
		// 转换层不校验下限：500ms 能转就转，拒绝是 config 层的职责
		{"500ms", 500 * time.Millisecond, true},
		{"  1h  ", time.Hour, true}, // 容忍 YAML 引号串里的首尾空白
	} {
		t.Run(tc.in, func(t *testing.T) {
			d, ok := ParseSelfWake(tc.in)
			if ok != tc.ok || d != tc.want {
				t.Errorf("ParseSelfWake(%q) = (%v, %v), want (%v, %v)", tc.in, d, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestSelfWakeInterval 配置值 → 实际轮询间隔（wiring 用的解析规则）：
// 空串 = 默认 10 分钟，"off"/"0"/非法值 = 0（关闭），其余原样。
func TestSelfWakeInterval(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  config.AgentConfig
		want time.Duration
	}{
		{"missing key defaults to 10m", config.AgentConfig{}, defaultSelfWakeInterval},
		{"explicit off", config.AgentConfig{SelfWake: "off"}, 0},
		{"explicit zero", config.AgentConfig{SelfWake: "0"}, 0},
		{"one hour", config.AgentConfig{SelfWake: "1h"}, time.Hour},
		{"whitespace trimmed", config.AgentConfig{SelfWake: " 30m "}, 30 * time.Minute},
		{"garbage treated as off (config layer already rejected)", config.AgentConfig{SelfWake: "junk"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := selfWakeInterval(tc.cfg); got != tc.want {
				t.Errorf("selfWakeInterval(%+v) = %v, want %v", tc.cfg, got, tc.want)
			}
		})
	}
}

// TestStartSelfWakeDeliversWakeMessages 端到端：StartSelfWake 定期合成的
// [定时轮询] 消息经真实 Enqueue→防抖→worker 抵达假 LLM，会话为
// private:<selfID>（即 sessionKey("private", 999)），发送者「轮询」、
// SelfID 带自身 QQ。
func TestStartSelfWakeDeliversWakeMessages(t *testing.T) {
	b := &stubBuilder{}
	f, srv := newFakeLLM(t, "pong")
	m := NewManager(b, NewClient(srv.URL, "sk-test", 5*time.Second), testCfg(5*time.Millisecond), nil, 999)
	m.notesPath = filepath.Join(t.TempDir(), "notes.json")
	m.statePath = filepath.Join(t.TempDir(), "state.json")
	startFakeNapCat(t)

	m.StartSelfWake(30 * time.Millisecond)

	waitFor(t, 3*time.Second, func() bool { return f.count() >= 2 }, "30ms 轮询应至少唤醒 2 次")
	turn := b.turn(0)
	if turn.ChatType != "private" || turn.ChatID != 999 {
		t.Errorf("轮询会话应为 private:999, got %s:%d", turn.ChatType, turn.ChatID)
	}
	if turn.SelfID != 999 {
		t.Errorf("turn.SelfID = %d, want 999", turn.SelfID)
	}
	if len(turn.NewMessages) < 1 {
		t.Fatal("turn 应至少含 1 条新消息")
	}
	// 该 manager 只会有轮询消息入队，取首条断言即可（极端调度下两个 tick
	// 合并进同一 turn 也属正常防抖行为，不影响唤醒语义）。
	wm := turn.NewMessages[0]
	if !strings.HasPrefix(wm.Text, "[定时轮询]") {
		t.Errorf("轮询文本应以 [定时轮询] 开头, got %q", wm.Text)
	}
	if wm.SenderName != "轮询" || wm.SenderID != 999 {
		t.Errorf("发送者应为 轮询(999), got %s(%d)", wm.SenderName, wm.SenderID)
	}
	if wm.ChatType != "private" || wm.ChatID != 999 {
		t.Errorf("消息应自带会话 private:999, got %s:%d", wm.ChatType, wm.ChatID)
	}
	if wm.Time.IsZero() {
		t.Error("轮询消息应带触发时间")
	}
}

// TestStartSelfWakeDisabledCases interval<=0（配置 off/0）或 selfID<=0
// （未配置机器人 QQ）时 StartSelfWake 直接返回，不产生任何唤醒。
func TestStartSelfWakeDisabledCases(t *testing.T) {
	for _, tc := range []struct {
		name     string
		interval time.Duration
		selfID   int64
	}{
		{"zero interval", 0, 999},
		{"negative interval", -time.Second, 999},
		{"selfID not configured", 30 * time.Millisecond, 0},
		{"negative selfID", 30 * time.Millisecond, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &stubBuilder{}
			f, srv := newFakeLLM(t, "pong")
			m := NewManager(b, NewClient(srv.URL, "sk-test", 5*time.Second), testCfg(5*time.Millisecond), nil, tc.selfID)
			m.notesPath = filepath.Join(t.TempDir(), "notes.json")
			m.statePath = filepath.Join(t.TempDir(), "state.json")

			m.StartSelfWake(tc.interval)

			time.Sleep(150 * time.Millisecond) // 覆盖 30ms 间隔下的 ~5 个 tick
			if got := f.count(); got != 0 {
				t.Errorf("不应产生任何轮询唤醒, LLM 请求数 = %d", got)
			}
		})
	}
}
