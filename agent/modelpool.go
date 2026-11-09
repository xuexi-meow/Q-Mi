// modelpool.go 模型档位池（spec 2026-11-09 §7）：一档多候选、候选启停、
// 能力匹配、失败冷却与保守 failover。
//
// 选择规则：唤醒类型经 wake_routes 映射到档位 → 档位内按顺序取第一个
// 「启用 + 能力满足 + 不在冷却」的候选；档位不可用时回退 chat。
// 请求失败只在可重试错误（网络/超时/429/5xx）时换下一个候选，4xx 直接失败。
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"Q-Mi/utils/config"
)

// defaultCandidateCooldown 候选失败后的默认冷却时间（配置 cooldown 缺省时）。
const defaultCandidateCooldown = 5 * time.Minute

// defaultMaxAttempts 单次请求最多尝试的候选数（含首个）。
const defaultMaxAttempts = 2

// errNoModel 没有任何可用候选（全部停用/能力不满足/冷却中）。
var errNoModel = errors.New("没有可用的模型候选")

// modelNeed 本次请求对模型能力的要求。
type modelNeed struct {
	Tools  bool
	Vision bool
	Video  bool
}

// needForMessages 从实际消息内容判断能力需求：主循环恒需 tools；
// 出现 image_url 内容部分则需要 vision。视频尚未落地，恒为 false。
func needForMessages(msgs []ChatMessage) modelNeed {
	need := modelNeed{Tools: true}
	for _, message := range msgs {
		parts, ok := message.Content.([]ContentPart)
		if !ok {
			continue
		}
		for _, part := range parts {
			if part.Type == "image_url" {
				need.Vision = true
				break
			}
		}
		if need.Vision {
			break
		}
	}
	return need
}

// modelSlot 一个候选的运行时状态（客户端 + 冷却）。
type modelSlot struct {
	name         string
	model        string
	client       *Client
	capabilities map[string]bool
	unrestricted bool // capabilities 未声明 = 不限制
	cooldown     time.Duration

	mu    sync.Mutex
	until time.Time
	fails int
}

func newModelSlot(cand config.ModelCandidate, client *Client) *modelSlot {
	caps := make(map[string]bool, len(cand.Capabilities))
	for _, c := range cand.Capabilities {
		caps[strings.ToLower(strings.TrimSpace(c))] = true
	}
	cooldown := defaultCandidateCooldown
	if cand.Cooldown != "" {
		if d, err := time.ParseDuration(cand.Cooldown); err == nil {
			cooldown = d
		}
	}
	return &modelSlot{
		name: cand.Name, model: cand.Model, client: client,
		capabilities: caps, unrestricted: len(caps) == 0, cooldown: cooldown,
	}
}

func (s *modelSlot) key() string { return s.name + "@" + s.model }

// supports 能力判断：未声明 capabilities 视为不限制（兼容旧配置）。
func (s *modelSlot) supports(capability string) bool {
	return s.unrestricted || s.capabilities[capability]
}

// eligible 当前是否可用：能力满足且不在冷却期。
func (s *modelSlot) eligible(need modelNeed, now time.Time) bool {
	if need.Tools && !s.supports("tools") {
		return false
	}
	if need.Vision && !s.supports("vision") {
		return false
	}
	if need.Video && !s.supports("video") {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return !now.Before(s.until)
}

func (s *modelSlot) markFailure(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fails++
	if s.cooldown > 0 {
		s.until = now.Add(s.cooldown)
	}
}

func (s *modelSlot) markSuccess() {
	s.mu.Lock()
	s.fails = 0
	s.mu.Unlock()
}

// modelPool 唤醒类型 → 档位 → 候选 的三层结构。
type modelPool struct {
	tiers  map[string][]*modelSlot
	routes map[string]string
}

// buildPool 构造模型池：未配置 models 时 chat 档只有 legacy 一个候选；
// 显式配置的档位按 candidates 顺序展开（旧单模型写法归一为单候选）。
func buildPool(cfg config.AgentConfig, legacy *Client) *modelPool {
	pool := &modelPool{tiers: map[string][]*modelSlot{}, routes: map[string]string{}}
	legacySlot := newModelSlot(config.ModelCandidate{Name: "legacy", Model: cfg.Model}, legacy)
	pool.tiers[WakeChat] = []*modelSlot{legacySlot}

	for tierName, tier := range cfg.Models {
		if !tier.Enabled() {
			pool.tiers[tierName] = nil
			continue
		}
		var slots []*modelSlot
		for _, cand := range tier.EffectiveCandidates(tierName) {
			if !cand.Enabled() {
				continue
			}
			client, ok := candidateClient(cand, legacy)
			if !ok {
				continue
			}
			slots = append(slots, newModelSlot(cand, client))
		}
		pool.tiers[tierName] = slots
	}
	// chat 档必须始终可用：显式 chat 档为空/停用时退回 legacy。
	if len(pool.tiers[WakeChat]) == 0 {
		pool.tiers[WakeChat] = []*modelSlot{legacySlot}
	}
	for wake, target := range cfg.WakeRoutes {
		if target != "" {
			pool.routes[wake] = target
		}
	}
	return pool
}

// candidateClient 按候选配置建客户端：没写 base_url/key 的候选复用 legacy
// 客户端（沿用顶层三件套）；写了就必须齐全，api_key_env 缺失则跳过该候选。
func candidateClient(cand config.ModelCandidate, legacy *Client) (*Client, bool) {
	if cand.BaseURL == "" && cand.APIKey == "" && cand.APIKeyEnv == "" {
		return legacy, true
	}
	key := cand.APIKey
	if key == "" && cand.APIKeyEnv != "" {
		key = os.Getenv(cand.APIKeyEnv)
	}
	if cand.BaseURL == "" || key == "" {
		slog.Warn("模型候选接入参数不完整，已跳过",
			"candidate", cand.Name, "base_url_set", cand.BaseURL != "", "key_set", key != "")
		return nil, false
	}
	return NewClient(cand.BaseURL, key, llmRequestTimeout), true
}

// pick 选一个可用候选：先映射档位，再回退 chat；skip 用于 failover 排除已试过的。
func (p *modelPool) pick(wake string, need modelNeed, skip map[string]bool) (*modelSlot, string, error) {
	if p == nil {
		return nil, "", errNoModel
	}
	tier := wake
	if tier == "" {
		tier = WakeChat
	}
	if routed, ok := p.routes[tier]; ok {
		tier = routed
	}
	for _, name := range tierChain(tier) {
		now := time.Now()
		for _, slot := range p.tiers[name] {
			if slot == nil || slot.client == nil {
				continue
			}
			if skip[slot.key()] {
				continue
			}
			if !slot.eligible(need, now) {
				continue
			}
			return slot, name, nil
		}
	}
	return nil, tier, errNoModel
}

// tierChain 档位回退链：目标档 → chat（去重）。
func tierChain(tier string) []string {
	if tier == "" || tier == WakeChat {
		return []string{WakeChat}
	}
	return []string{tier, WakeChat}
}

// retryableError 判断失败是否值得换候选：网络错误/超时/429/5xx 可重试；
// 4xx（模型不存在、参数错误、图片不支持）换候选也无意义，直接失败。
func retryableError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.Status == 429 || httpErr.Status >= 500
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// stripImageParts 给不支持 vision 的候选降级：把 image_url 部分替换为文字标注，
// 返回新切片，不修改会话里的原消息。
func stripImageParts(msgs []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, len(msgs))
	copy(out, msgs)
	for i, message := range out {
		parts, ok := message.Content.([]ContentPart)
		if !ok {
			continue
		}
		kept := make([]ContentPart, 0, len(parts))
		images := 0
		for _, part := range parts {
			if part.Type == "image_url" {
				images++
				continue
			}
			kept = append(kept, part)
		}
		if images == 0 {
			continue
		}
		kept = append(kept, ContentPart{Type: "text", Text: fmt.Sprintf("（%d 张图片已省略）", images)})
		out[i].Content = kept
	}
	return out
}
