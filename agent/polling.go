// polling.go P3.6 定时轮询（外部定时唤醒兜底）：模型自驱的 schedule 工具链
// 会断链（重启丢失、模型忘排下一次），也没有冷启动——agent 启用后若没人说话
// 就永远沉睡。这里用代码级 time.Ticker 定期合成一条 [定时轮询] 消息投进
// 自己的会话（sessionKey("private", selfID)），保证醒来的下限节奏。
// 轮询 ≠ 发言：唤醒只是把"想想看要不要做事"的机会递给模型，说不说、说什么
// 由模型自决（guide.md「定时轮询」节约束行为）。
//
// 间隔来自 agent.self_wake（utils/config 校验 ≥30s）；空串=默认 10 分钟，
// "off"/"0"=关闭。无需显式停止：goroutine 与进程同生命周期，Manager 单例
// 不销毁复用，进程退出时由 runtime 统一回收。
package agent

import (
	"strings"
	"time"

	"Q-Mi/utils/config"
)

// defaultSelfWakeInterval self_wake 未配置（空串/缺键）时的默认轮询间隔。
const defaultSelfWakeInterval = 10 * time.Minute

// selfWakeText 轮询唤醒消息文本：明确"例行检查、不强制发言"的语义。
const selfWakeText = "[定时轮询] 例行醒来。看看有什么想做的：可以查查看、想想事、也可以什么都不做。不强制发言，没要做的事就安静结束。"

// selfWakeSender 轮询消息的发送者名（IncomingMessage.SenderName / TurnMessage 行展示用）。
const selfWakeSender = "轮询"

// ParseSelfWake 解析 agent.self_wake 配置值为轮询间隔（纯转换，无校验）：
//   - "" / "off" / "0" → (0, false)：false 表示关闭，或由调用方补默认值
//     （wiring 对空串给 defaultSelfWakeInterval，对 off/0 不轮询）
//   - 其余按 time.ParseDuration 转换，成功 → (d, true)
//
// <30s 下限与非法值的拒绝由 config 层负责（validateAgentConfig 已挡），
// 这里遇到解析失败按 (0, false) 处理——调用方语义为"关闭"。
func ParseSelfWake(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	switch s {
	case "", "off", "0":
		return 0, false
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, false
	}
	return d, true
}

// selfWakeInterval 把配置值解析成轮询间隔：空串 → 默认 10 分钟；
// "off"/"0"/解析失败 → 0（StartSelfWake 直接跳过）；其余为配置的时长。
func selfWakeInterval(cfg config.AgentConfig) time.Duration {
	if strings.TrimSpace(cfg.SelfWake) == "" {
		return defaultSelfWakeInterval
	}
	d, _ := ParseSelfWake(cfg.SelfWake)
	return d
}

// StartSelfWake P3.6 定时轮询：起 goroutine 每 interval 合成一条
// [定时轮询] 私聊消息入自己的会话（sessionKey("private", selfID)），
// 作为模型自驱 schedule 链断掉/无冷启动时的代码级唤醒兜底。
// 走正常 Enqueue→防抖排队，不紧急、不打断正在进行的思考。
//
// interval<=0（配置 "off"/"0"）或 selfID<=0（未配置 bot.qq_number）直接返回。
// 无需显式停止：goroutine 与进程同生命周期，Manager 为进程级单例不复用，
// 进程退出时由 runtime 统一回收。
func (m *Manager) StartSelfWake(interval time.Duration) {
	if interval <= 0 || m.selfID <= 0 {
		return
	}
	selfID := m.selfID
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			m.Enqueue(IncomingMessage{
				ChatType:   "private",
				ChatID:     selfID,
				SelfID:     selfID,
				SenderID:   selfID,
				SenderName: selfWakeSender,
				Text:       selfWakeText,
				Time:       time.Now(),
				Urgent:     false,
			})
		}
	}()
}
