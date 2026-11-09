package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/viper"
)

const DefaultConfig = `bot:
  qq_number: 0
  admin_number:
    - 1234
netsetting:
  host: "0.0.0.0"
  port: 9090
  pattern: "/Q-Mi"
  token: ""
napcat:
  ws:
    enable: false
    host: "127.0.0.1"
    port: 8080
    token: ""
  http:
    enable: true
    host: "127.0.0.1"
    port: 8080
    token: ""
storage:
  enable: true
  path: "Q-Mi.db"
agent:
  enable: false
  base_url: ""
  api_key: ""
  model: ""
  persona_file: "agent/persona.md"
  guide_file: "agent/guide.md"
  debounce: 3s
  self_wake: "10m"
  temperature: 0.8
  debug: false
  multimodal: true
  max_tool_rounds: 10
  history:
    max: 40
    trim: 20
webui:
  enable: false
  host: "127.0.0.1"
  port: 6077
  token: ""
`

type Config struct {
	Bot        *Bot
	NetSetting *NetSetting
	Napcat     *Napcat
	Storage    *Storage
	Agent      *AgentConfig
	WebUI      *WebUIConfig
}

// WebUIConfig Web 控制台配置，与 Config.yaml 中 webui 块对应。
type WebUIConfig struct {
	Enable bool   `yaml:"enable" mapstructure:"enable"`
	Host   string `yaml:"host" mapstructure:"host"`
	Port   int    `yaml:"port" mapstructure:"port"`
	Token  string `yaml:"token" mapstructure:"token"`
}

// Storage 消息记忆库配置
type Storage struct {
	Enable bool   `yaml:"enable"`
	Path   string `yaml:"path"`
}

type Bot struct {
	QQ_Number    int   `yaml:"qq_number"`
	Admin_Number []int `yaml:"admin_number"`
}

type NetSetting struct {
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
	Pattern string `yaml:"pattern"`
	Token   string `yaml:"token"`
}

// Napcat NapCat 客户端连接配置,与 Config.yaml 中 napcat 下的 ws/http 子块对应
type Napcat struct {
	WS   NapcatConn `yaml:"ws"`
	HTTP NapcatConn `yaml:"http"`
}

// NapcatConn 一条连接通道的配置
type NapcatConn struct {
	Enable bool   `yaml:"enable"`
	Host   string `yaml:"host"`
	Port   int    `yaml:"port"`
	Token  string `yaml:"token"`
}

// ModelCandidate 档位内的一个模型候选：选择顺序、启停、能力与失败冷却。
type ModelCandidate struct {
	Name          string   `yaml:"name" mapstructure:"name"`
	Enable        *bool    `yaml:"enable" mapstructure:"enable"` // 缺省 true
	BaseURL       string   `yaml:"base_url" mapstructure:"base_url"`
	APIKey        string   `yaml:"api_key" mapstructure:"api_key"`
	APIKeyEnv     string   `yaml:"api_key_env" mapstructure:"api_key_env"`
	Model         string   `yaml:"model" mapstructure:"model"`
	Capabilities  []string `yaml:"capabilities" mapstructure:"capabilities"`
	ContextWindow int      `yaml:"context_window" mapstructure:"context_window"`
	Weight        int      `yaml:"weight" mapstructure:"weight"`
	Cooldown      string   `yaml:"cooldown" mapstructure:"cooldown"` // 失败冷却，缺省 5m；"0" 关闭
}

// Enabled 候选是否启用（缺省启用）。
func (c ModelCandidate) Enabled() bool { return c.Enable == nil || *c.Enable }

// ModelTier 一个模型档位：多候选 + 选择策略 + 整档启停。
// 旧写法（单模型字段 BaseURL/APIKey/Model/Capabilities）继续支持，
// 经 EffectiveCandidates 归一为「唯一候选、name=档位名」。
type ModelTier struct {
	Enable       *bool            `yaml:"enable" mapstructure:"enable"` // 缺省 true
	Strategy     string           `yaml:"strategy" mapstructure:"strategy"`
	Candidates   []ModelCandidate `yaml:"candidates" mapstructure:"candidates"`
	BaseURL      string           `yaml:"base_url" mapstructure:"base_url"`
	APIKey       string           `yaml:"api_key" mapstructure:"api_key"`
	Model        string           `yaml:"model" mapstructure:"model"`
	Capabilities []string         `yaml:"capabilities" mapstructure:"capabilities"`
}

// Enabled 档位是否启用（缺省启用）。
func (t ModelTier) Enabled() bool { return t.Enable == nil || *t.Enable }

// EffectiveCandidates 归一化新老写法：显式 candidates 优先；否则把旧单模型
// 字段视为唯一候选（name = 档位名）。两者都没有返回 nil。
func (t ModelTier) EffectiveCandidates(tierName string) []ModelCandidate {
	if len(t.Candidates) > 0 {
		out := make([]ModelCandidate, len(t.Candidates))
		copy(out, t.Candidates)
		return out
	}
	if t.BaseURL == "" && t.APIKey == "" && t.Model == "" {
		return nil
	}
	return []ModelCandidate{{
		Name: tierName, BaseURL: t.BaseURL, APIKey: t.APIKey,
		Model: t.Model, Capabilities: t.Capabilities,
	}}
}

// AgentConfig AI 交互配置，与 Config.yaml 中 agent 块对应。
// 注意：viper UnmarshalKey 走 mapstructure，多单词键需 mapstructure 标签。
type AgentConfig struct {
	Enable      bool   `yaml:"enable" mapstructure:"enable"`
	BaseURL     string `yaml:"base_url" mapstructure:"base_url"`
	APIKey      string `yaml:"api_key" mapstructure:"api_key"`
	Model       string `yaml:"model" mapstructure:"model"`
	PersonaFile string `yaml:"persona_file" mapstructure:"persona_file"`
	GuideFile   string `yaml:"guide_file" mapstructure:"guide_file"`
	// ImageDir 内容寻址图片缓存落盘目录；空串 = 默认 agent/images。
	ImageDir string `yaml:"image_dir" mapstructure:"image_dir"`
	// WorkspaceDir 文件操作工作区根目录（computer 板块围栏）；空串 = 默认 agent/workspace。
	WorkspaceDir string `yaml:"workspace_dir" mapstructure:"workspace_dir"`
	// BrowserExec 浏览器可执行文件路径（browser 板块）；空串 = 自动探测 Chrome/Edge。
	BrowserExec string `yaml:"browser_executable" mapstructure:"browser_executable"`
	// Models 分档模型档位（model profile）：键为档位名（chat / background /
	// video / 自定义），值为该档的接入参数与能力标签。路由规则（manager）：
	// 含视频内容 → video 档（未配置则 chat 并降级提示）；后台类唤醒
	// （轮询/通知/定时/任务回执）→ background 档；其余 → chat。
	// 不配置 Models 时整体回退到顶层 base_url/api_key/model（作为 chat 档）。
	Models map[string]ModelTier `yaml:"models" mapstructure:"models"`
	// WakeRoutes 唤醒类型 → 档位名。缺省 chat→chat、background→background；
	// 自定义档位必须在这里显式路由才会被使用。
	WakeRoutes map[string]string `yaml:"wake_routes" mapstructure:"wake_routes"`
	// MaxAttempts 单次请求最多尝试的候选数（含首个），0=默认 2，上限 5。
	MaxAttempts int `yaml:"max_attempts" mapstructure:"max_attempts"`
	// Modules 工具板块启停（重启生效，无热插拔）：缺省键 = 启用；
	// 显式 false 的板块不加载（工具与文档都不出现）。
	Modules  map[string]bool `yaml:"modules" mapstructure:"modules"`
	Debounce time.Duration   `yaml:"debounce" mapstructure:"debounce"`
	// SelfWake 定时轮询间隔（P3.6 代码级唤醒兜底）：空串=默认 10 分钟；
	// "off"/"0"=关闭；其余必须为 ≥30s 的合法 duration（下限防误配成高频轮询）。
	SelfWake    string  `yaml:"self_wake" mapstructure:"self_wake"`
	Temperature float64 `yaml:"temperature" mapstructure:"temperature"`
	Debug       bool    `yaml:"debug" mapstructure:"debug"`
	// Multimodal 缺键(nil)视为 true——旧配置文件没有该字段时不应静默关闭。
	Multimodal    *bool        `yaml:"multimodal" mapstructure:"multimodal"`
	MaxToolRounds int          `yaml:"max_tool_rounds" mapstructure:"max_tool_rounds"`
	History       AgentHistory `yaml:"history" mapstructure:"history"`
	Context       AgentContext `yaml:"context" mapstructure:"context"`
}

// WakeChat 内置 chat 档名：唤醒路由与回退链的终点。
const WakeChat = "chat"

// AgentContext budgets the account-wide in-memory conversation.
// Token counts are estimates, not a provider-specific tokenizer.
type AgentContext struct {
	MaxInputTokens     int `yaml:"max_input_tokens" mapstructure:"max_input_tokens"`
	ReviewAfterWakes   int `yaml:"review_after_wakes" mapstructure:"review_after_wakes"`
	KeepRecentMessages int `yaml:"keep_recent_messages" mapstructure:"keep_recent_messages"`
	SummaryTokens      int `yaml:"summary_tokens" mapstructure:"summary_tokens"`
}

func (c AgentContext) WithDefaults() AgentContext {
	if c.MaxInputTokens == 0 {
		c.MaxInputTokens = 48000
	}
	if c.ReviewAfterWakes == 0 {
		c.ReviewAfterWakes = 8
	}
	if c.KeepRecentMessages == 0 {
		c.KeepRecentMessages = 12
	}
	if c.SummaryTokens == 0 {
		c.SummaryTokens = 1024
	}
	return c
}

// AgentHistory 上下文历史携带策略。
type AgentHistory struct {
	Max  int `yaml:"max" mapstructure:"max"`
	Trim int `yaml:"trim" mapstructure:"trim"`
}

func (c *Config) LoadConfig() *Config {
	v := viper.New()
	v.SetConfigName("Config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")
	err := v.ReadInConfig()
	if err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			slog.Warn("未找到配置文件，将创建默认文件")
			if r := makeNewConfig(); !r {
				return nil
			}
			err := v.ReadInConfig()
			if err != nil {
				slog.Error("读取配置文件失败", "err", err)
				return nil
			}
		} else {
			slog.Error("读取配置文件失败", "err", err)
			return nil
		}
	}

	cfg, err := decodeConfig(v)
	if err != nil {
		slog.Error("配置加载失败", "err", err)
		return nil
	}
	slog.Info("读取配置文件成功")
	return cfg
}

// decodeConfig 把已就绪的 viper 实例解码为 Config 并校验。
// LoadConfig（单文件）与 Store（分层合并）共用这一条解析+校验路径，
// 避免两套逻辑漂移；错误语义一致：解析失败或校验失败都返回 error。
func decodeConfig(v *viper.Viper) (*Config, error) {
	bot, netSetting, napcat, storageCfg, agentCfg, webuiCfg := initConfig(*v)
	if bot == nil || netSetting == nil || napcat == nil || storageCfg == nil || agentCfg == nil || webuiCfg == nil {
		return nil, fmt.Errorf("配置结构解码失败")
	}
	if err := validateConfig(netSetting, napcat, storageCfg, agentCfg, webuiCfg); err != nil {
		return nil, err
	}
	return &Config{
		Bot:        bot,
		NetSetting: netSetting,
		Napcat:     napcat,
		Storage:    storageCfg,
		Agent:      agentCfg,
		WebUI:      webuiCfg,
	}, nil
}

func makeNewConfig() bool {
	if _, err := os.Lstat("Config.yaml"); !os.IsNotExist(err) {
		if err == nil {
			err = os.ErrExist
		}
		slog.Error("创建配置文件失败", "err", err)
		return false
	}
	if err := os.WriteFile("Config.yaml", []byte(DefaultConfig), 0600); err != nil {
		slog.Error("写入配置文件失败", "err", err)
		return false
	}
	return true
}

func validateConfig(netSetting *NetSetting, napcat *Napcat, storage *Storage, agent *AgentConfig, webui *WebUIConfig) error {
	if err := validateHostPort("netsetting", netSetting.Host, netSetting.Port); err != nil {
		return err
	}
	pattern := netSetting.Pattern
	u, err := url.ParseRequestURI(pattern)
	if err != nil {
		return fmt.Errorf("netsetting.pattern: %w", err)
	}
	if !strings.HasPrefix(pattern, "/") || u.IsAbs() ||
		strings.ContainsAny(pattern, "{}*?#\\") || strings.ContainsAny(u.Path, "{}*?#\\") ||
		strings.IndexFunc(u.Path, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return fmt.Errorf("netsetting.pattern %q must be a plain absolute path without whitespace or wildcards", pattern)
	}
	if storage.Enable && strings.TrimSpace(storage.Path) == "" {
		return fmt.Errorf("storage.path must not be empty when storage is enabled")
	}
	if napcat.HTTP.Enable {
		if err := validateHostPort("napcat.http", napcat.HTTP.Host, napcat.HTTP.Port); err != nil {
			return err
		}
	}
	if agent.Enable {
		if err := validateAgentConfig(agent); err != nil {
			return err
		}
	}
	if webui.Enable {
		if strings.TrimSpace(webui.Token) == "" {
			return fmt.Errorf("webui.token must not be empty when webui is enabled")
		}
		if err := validateHostPort("webui", webui.Host, webui.Port); err != nil {
			return err
		}
	}
	return nil
}

// validateAgentConfig 校验启用的 agent 块。
func validateAgentConfig(agent *AgentConfig) error {
	baseURL := strings.TrimSpace(agent.BaseURL)
	u, err := url.ParseRequestURI(baseURL)
	if err != nil {
		return fmt.Errorf("agent.base_url: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("agent.base_url %q must be a valid http(s) URL", agent.BaseURL)
	}
	if strings.TrimSpace(agent.Model) == "" {
		return fmt.Errorf("agent.model must not be empty when agent is enabled")
	}
	if strings.TrimSpace(agent.APIKey) == "" {
		return fmt.Errorf("agent.api_key must not be empty when agent is enabled")
	}
	if strings.TrimSpace(agent.PersonaFile) == "" {
		return fmt.Errorf("agent.persona_file must not be empty when agent is enabled")
	}
	if strings.TrimSpace(agent.GuideFile) == "" {
		return fmt.Errorf("agent.guide_file must not be empty when agent is enabled")
	}
	if agent.Debounce <= 0 {
		return fmt.Errorf("agent.debounce must be a positive duration, got %s", agent.Debounce)
	}
	// models 分档：支持一档多候选，同时兼容旧的单模型写法。
	for name, tier := range agent.Models {
		switch tier.Strategy {
		case "", "priority":
		default:
			return fmt.Errorf("agent.models.%s.strategy %q is not supported yet (only priority)", name, tier.Strategy)
		}
		candidates := tier.EffectiveCandidates(name)
		if tier.Enabled() && len(candidates) == 0 {
			return fmt.Errorf("agent.models.%s must define at least one candidate", name)
		}
		seen := make(map[string]bool, len(candidates))
		for i, cand := range candidates {
			label := fmt.Sprintf("agent.models.%s.candidates[%d]", name, i)
			if strings.TrimSpace(cand.Name) == "" {
				return fmt.Errorf("%s.name must not be empty", label)
			}
			if seen[cand.Name] {
				return fmt.Errorf("%s.name %q is duplicated", label, cand.Name)
			}
			seen[cand.Name] = true
			if strings.TrimSpace(cand.Model) == "" {
				return fmt.Errorf("%s.model must not be empty", label)
			}
			if baseURL := strings.TrimSpace(cand.BaseURL); baseURL != "" {
				u, err := url.ParseRequestURI(baseURL)
				if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
					return fmt.Errorf("%s.base_url %q must be a valid http(s) URL", label, cand.BaseURL)
				}
			}
			if cand.APIKey != "" && cand.APIKeyEnv != "" {
				return fmt.Errorf("%s: api_key and api_key_env are mutually exclusive", label)
			}
			if cand.Cooldown != "" {
				d, err := time.ParseDuration(cand.Cooldown)
				if err != nil {
					return fmt.Errorf("%s.cooldown %q: %w", label, cand.Cooldown, err)
				}
				if d < 0 {
					return fmt.Errorf("%s.cooldown must not be negative", label)
				}
			}
			if cand.ContextWindow < 0 {
				return fmt.Errorf("%s.context_window must not be negative", label)
			}
			if cand.Weight < 0 {
				return fmt.Errorf("%s.weight must not be negative", label)
			}
		}
	}
	if agent.MaxAttempts < 0 || agent.MaxAttempts > 5 {
		return fmt.Errorf("agent.max_attempts must be between 0 and 5, got %d", agent.MaxAttempts)
	}
	for wake, target := range agent.WakeRoutes {
		if strings.TrimSpace(target) == "" {
			return fmt.Errorf("agent.wake_routes.%s must not be empty", wake)
		}
		if target != WakeChat {
			if _, ok := agent.Models[target]; !ok {
				return fmt.Errorf("agent.wake_routes.%s points to unknown tier %q", wake, target)
			}
		}
	}
	// self_wake P3.6：空串=默认 10 分钟，"off"/"0"=关闭；其余须解析成功且 ≥30s。
	if s := strings.TrimSpace(agent.SelfWake); s != "" && s != "off" && s != "0" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return fmt.Errorf("agent.self_wake %q: %w", agent.SelfWake, err)
		}
		if d < 30*time.Second {
			return fmt.Errorf("agent.self_wake %q must be at least 30s (empty = default 10m, \"off\"/\"0\" = disabled)", agent.SelfWake)
		}
	}
	if agent.MaxToolRounds <= 0 {
		return fmt.Errorf("agent.max_tool_rounds must be positive, got %d", agent.MaxToolRounds)
	}
	if agent.History.Trim <= 0 {
		return fmt.Errorf("agent.history.trim must be positive, got %d", agent.History.Trim)
	}
	if agent.History.Max < agent.History.Trim {
		return fmt.Errorf("agent.history.max %d must be >= agent.history.trim %d", agent.History.Max, agent.History.Trim)
	}
	raw := agent.Context
	if raw.MaxInputTokens < 0 || raw.ReviewAfterWakes < 0 || raw.KeepRecentMessages < 0 || raw.SummaryTokens < 0 {
		return fmt.Errorf("agent.context budgets must not be negative")
	}
	policy := raw.WithDefaults()
	if policy.MaxInputTokens < 2048 || policy.SummaryTokens >= policy.MaxInputTokens/2 || policy.KeepRecentMessages < 2 {
		return fmt.Errorf("agent.context requires max_input_tokens >= 2048, summary_tokens < half the input budget, and keep_recent_messages >= 2")
	}
	return nil
}

// MultimodalEnabled 缺键(nil)默认开启。
func (a *AgentConfig) MultimodalEnabled() bool {
	return a.Multimodal == nil || *a.Multimodal
}

func validateHostPort(name, host string, port int) error {
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("%s.host must not be empty", name)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s.port must be between 1 and 65535, got %d", name, port)
	}
	return nil
}

func initConfig(v viper.Viper) (*Bot, *NetSetting, *Napcat, *Storage, *AgentConfig, *WebUIConfig) {
	var bot Bot
	if err := v.UnmarshalKey("bot", &bot); err != nil {
		slog.Error("解析 bot 配置失败", "err", err)
		return nil, nil, nil, nil, nil, nil
	}
	var netSetting NetSetting
	if err := v.UnmarshalKey("netsetting", &netSetting); err != nil {
		slog.Error("解析 netsetting 配置失败", "err", err)
		return nil, nil, nil, nil, nil, nil
	}
	var napcat Napcat
	if err := v.UnmarshalKey("napcat", &napcat); err != nil {
		slog.Error("解析 napcat 配置失败", "err", err)
		return nil, nil, nil, nil, nil, nil
	}
	var storage Storage
	if err := v.UnmarshalKey("storage", &storage); err != nil {
		slog.Error("解析 storage 配置失败", "err", err)
		return nil, nil, nil, nil, nil, nil
	}
	var agent AgentConfig
	if err := v.UnmarshalKey("agent", &agent); err != nil {
		slog.Error("解析 agent 配置失败", "err", err)
		return nil, nil, nil, nil, nil, nil
	}
	var webui WebUIConfig
	if err := v.UnmarshalKey("webui", &webui); err != nil {
		slog.Error("解析 webui 配置失败", "err", err)
		return nil, nil, nil, nil, nil, nil
	}
	return &bot, &netSetting, &napcat, &storage, &agent, &webui
}
