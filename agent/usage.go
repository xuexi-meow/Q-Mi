// agent/usage.go LLM 用量累计（进程内，自启动起）。多会话 worker 并发更新，
// 全原子操作无锁；webui 概览经 UsageSnapshot 读取。prompt cache 命中率 =
// hit / (hit + miss)，命中率掉坑即上下文前缀被打断的信号。
package agent

import "sync/atomic"

// llmUsage 进程级累计计数器。
var llmUsage struct {
	requests int64
	hit      int64
	miss     int64
	prompt   int64
	output   int64
}

// recordUsage 累加一次 LLM 响应的用量。
func recordUsage(u Usage) {
	atomic.AddInt64(&llmUsage.requests, 1)
	atomic.AddInt64(&llmUsage.hit, int64(u.PromptCacheHitTokens))
	atomic.AddInt64(&llmUsage.miss, int64(u.PromptCacheMissTokens))
	atomic.AddInt64(&llmUsage.prompt, int64(u.PromptTokens))
	atomic.AddInt64(&llmUsage.output, int64(u.CompletionTokens))
}

// UsageSnapshot 用量累计快照。
type UsageSnapshot struct {
	Requests     int64 `json:"requests"`
	CacheHit     int64 `json:"cache_hit_tokens"`
	CacheMiss    int64 `json:"cache_miss_tokens"`
	PromptTokens int64 `json:"prompt_tokens"`
	OutputTokens int64 `json:"output_tokens"`
}

// HitRate 缓存命中率：hit/(hit+miss)；无输入时返回 0。
func (s UsageSnapshot) HitRate() float64 {
	total := s.CacheHit + s.CacheMiss
	if total == 0 {
		return 0
	}
	return float64(s.CacheHit) / float64(total)
}

// UsageTotals 返回自启动起的累计用量快照。
func UsageTotals() UsageSnapshot {
	return UsageSnapshot{
		Requests:     atomic.LoadInt64(&llmUsage.requests),
		CacheHit:     atomic.LoadInt64(&llmUsage.hit),
		CacheMiss:    atomic.LoadInt64(&llmUsage.miss),
		PromptTokens: atomic.LoadInt64(&llmUsage.prompt),
		OutputTokens: atomic.LoadInt64(&llmUsage.output),
	}
}
