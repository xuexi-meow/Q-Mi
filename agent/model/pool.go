package model

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"Q-Mi/utils/config"
)

const (
	chatTier                 = "chat"
	defaultCandidateCooldown = 5 * time.Minute
)

var ErrNoModel = errors.New("没有可用的模型候选")

// Need 是一次请求所需的模型能力。
type Need struct {
	Tools  bool
	Vision bool
	Video  bool
}

// Candidate 保存候选接入信息和并发安全的失败冷却状态。
type Candidate struct {
	name         string
	model        string
	toolChoice   string
	client       *Client
	capabilities map[string]bool
	unrestricted bool
	cooldown     time.Duration

	mu    sync.Mutex
	until time.Time
	fails int
}

func newCandidate(cfg config.ModelCandidate, client *Client) *Candidate {
	caps := make(map[string]bool, len(cfg.Capabilities))
	for _, capability := range cfg.Capabilities {
		caps[strings.ToLower(strings.TrimSpace(capability))] = true
	}
	cooldown := defaultCandidateCooldown
	if cfg.Cooldown != "" {
		if duration, err := time.ParseDuration(cfg.Cooldown); err == nil {
			cooldown = duration
		}
	}
	return &Candidate{
		name: cfg.Name, model: cfg.Model, toolChoice: cfg.ToolChoice, client: client,
		capabilities: caps, unrestricted: len(caps) == 0, cooldown: cooldown,
	}
}

func (c *Candidate) Name() string       { return c.name }
func (c *Candidate) Model() string      { return c.model }
func (c *Candidate) ToolChoice() string { return c.toolChoice }
func (c *Candidate) Client() *Client    { return c.client }
func (c *Candidate) Key() string        { return c.name + "@" + c.model }

// Supports 保持旧配置语义：未声明能力即不限制。
func (c *Candidate) Supports(capability string) bool {
	return c.unrestricted || c.capabilities[capability]
}

func (c *Candidate) eligible(need Need, now time.Time) bool {
	if need.Tools && !c.Supports("tools") {
		return false
	}
	if need.Vision && !c.Supports("vision") {
		return false
	}
	if need.Video && !c.Supports("video") {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return !now.Before(c.until)
}

func (c *Candidate) MarkFailure(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fails++
	if c.cooldown > 0 {
		c.until = now.Add(c.cooldown)
	}
}

func (c *Candidate) MarkSuccess() {
	c.mu.Lock()
	c.fails = 0
	c.mu.Unlock()
}

// Pool 的档位和路由构造后只读；热配置更新由调用方整体替换。
type Pool struct {
	tiers  map[string][]*Candidate
	routes map[string]string
}

func NewPool(cfg config.AgentConfig, legacy *Client) *Pool {
	pool := &Pool{tiers: map[string][]*Candidate{}, routes: map[string]string{}}
	fallback := newCandidate(config.ModelCandidate{
		Name: "legacy", Model: cfg.Model, ToolChoice: cfg.ToolChoice,
	}, legacy)
	pool.tiers[chatTier] = []*Candidate{fallback}
	for name, tier := range cfg.Models {
		if !tier.Enabled() {
			pool.tiers[name] = nil
			continue
		}
		var candidates []*Candidate
		for _, cfgCandidate := range tier.EffectiveCandidates(name) {
			if !cfgCandidate.Enabled() {
				continue
			}
			if cfgCandidate.ToolChoice == "" {
				cfgCandidate.ToolChoice = cfg.ToolChoice
			}
			client, ok := candidateClient(cfgCandidate, legacy)
			if !ok {
				continue
			}
			candidates = append(candidates, newCandidate(cfgCandidate, client))
		}
		pool.tiers[name] = candidates
	}
	if len(pool.tiers[chatTier]) == 0 {
		pool.tiers[chatTier] = []*Candidate{fallback}
	}
	for wake, target := range cfg.WakeRoutes {
		if target != "" {
			pool.routes[wake] = target
		}
	}
	return pool
}

func candidateClient(cfg config.ModelCandidate, legacy *Client) (*Client, bool) {
	if cfg.BaseURL == "" && cfg.APIKey == "" && cfg.APIKeyEnv == "" {
		return legacy, true
	}
	key := cfg.APIKey
	if key == "" && cfg.APIKeyEnv != "" {
		key = os.Getenv(cfg.APIKeyEnv)
	}
	if cfg.BaseURL == "" || key == "" {
		slog.Warn("模型候选接入参数不完整，已跳过",
			"candidate", cfg.Name, "base_url_set", cfg.BaseURL != "", "key_set", key != "")
		return nil, false
	}
	return NewClient(cfg.BaseURL, key, DefaultRequestTimeout), true
}

// Pick 按路由档位、候选配置顺序选择；目标档不可用时回退 chat。
func (p *Pool) Pick(wake string, need Need, skip map[string]bool) (*Candidate, string, error) {
	if p == nil {
		return nil, "", ErrNoModel
	}
	tier := wake
	if tier == "" {
		tier = chatTier
	}
	if routed, ok := p.routes[tier]; ok {
		tier = routed
	}
	for _, name := range tierChain(tier) {
		now := time.Now()
		for _, candidate := range p.tiers[name] {
			if candidate == nil || candidate.client == nil || skip[candidate.Key()] {
				continue
			}
			if candidate.eligible(need, now) {
				return candidate, name, nil
			}
		}
	}
	return nil, tier, ErrNoModel
}

func tierChain(tier string) []string {
	if tier == "" || tier == chatTier {
		return []string{chatTier}
	}
	return []string{tier, chatTier}
}

// IsRetryableError 只为候选接入、账号或网络故障切换候选，不重试请求参数错误。
func IsRetryableError(err error) bool {
	if err == nil {
		return false
	}
	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		switch httpErr.Status {
		case 401, 402, 403, 429:
			return true
		}
		return httpErr.Status >= 500
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}
