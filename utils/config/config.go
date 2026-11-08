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

// ModelTier 一个模型档位（model profile）：接入参数 + 能力标签。
// capabilities 自由标记（tools/vision/video/thinking…），路由与文档据此
// 判断该档能处理什么内容。
type ModelTier struct {
	BaseURL      string   `yaml:"base_url" mapstructure:"base_url"`
	APIKey       string   `yaml:"api_key" mapstructure:"api_key"`
	Model        string   `yaml:"model" mapstructure:"model"`
	Capabilities []string `yaml:"capabilities" mapstructure:"capabilities"`
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

	bot, netSetting, napcat, storageCfg, agentCfg, webuiCfg := initConfig(*v)
	if bot == nil || netSetting == nil || napcat == nil || storageCfg == nil || agentCfg == nil || webuiCfg == nil {
		return nil
	}
	if err := validateConfig(netSetting, napcat, storageCfg, agentCfg, webuiCfg); err != nil {
		slog.Error("配置校验失败", "err", err)
		return nil
	}

	slog.Info("读取配置文件成功")
	return &Config{
		Bot:        bot,
		NetSetting: netSetting,
		Napcat:     napcat,
		Storage:    storageCfg,
		Agent:      agentCfg,
		WebUI:      webuiCfg,
	}
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
func validateAgentConfig(agent *AgentConfig) error {	baseURL := strings.TrimSpace(agent.BaseURL)
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
	// models 分档：每个显式配置的档位必须有完整接入参数
	for name, tier := range agent.Models {
		if strings.TrimSpace(tier.BaseURL) == "" {
			return fmt.Errorf("agent.models.%s.base_url must not be empty", name)
		}
		if strings.TrimSpace(tier.APIKey) == "" {
			return fmt.Errorf("agent.models.%s.api_key must not be empty", name)
		}
		if strings.TrimSpace(tier.Model) == "" {
			return fmt.Errorf("agent.models.%s.model must not be empty", name)
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
