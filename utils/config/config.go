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
  debounce: 3s
  temperature: 0.8
  debug: false
  max_tool_rounds: 10
  history:
    max: 40
    trim: 20
`

type Config struct {
	Bot        *Bot
	NetSetting *NetSetting
	Napcat     *Napcat
	Storage    *Storage
	Agent      *AgentConfig
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

// AgentConfig AI 交互配置，与 Config.yaml 中 agent 块对应。
// 注意：viper UnmarshalKey 走 mapstructure，多单词键需 mapstructure 标签。
type AgentConfig struct {
	Enable        bool          `yaml:"enable" mapstructure:"enable"`
	BaseURL       string        `yaml:"base_url" mapstructure:"base_url"`
	APIKey        string        `yaml:"api_key" mapstructure:"api_key"`
	Model         string        `yaml:"model" mapstructure:"model"`
	PersonaFile   string        `yaml:"persona_file" mapstructure:"persona_file"`
	Debounce      time.Duration `yaml:"debounce" mapstructure:"debounce"`
	Temperature   float64       `yaml:"temperature" mapstructure:"temperature"`
	Debug         bool          `yaml:"debug" mapstructure:"debug"`
	MaxToolRounds int           `yaml:"max_tool_rounds" mapstructure:"max_tool_rounds"`
	History       AgentHistory  `yaml:"history" mapstructure:"history"`
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

	bot, netSetting, napcat, storageCfg, agentCfg := initConfig(*v)
	if bot == nil || netSetting == nil || napcat == nil || storageCfg == nil || agentCfg == nil {
		return nil
	}
	if err := validateConfig(netSetting, napcat, storageCfg, agentCfg); err != nil {
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

func validateConfig(netSetting *NetSetting, napcat *Napcat, storage *Storage, agent *AgentConfig) error {
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
	if agent.Debounce <= 0 {
		return fmt.Errorf("agent.debounce must be a positive duration, got %s", agent.Debounce)
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

func validateHostPort(name, host string, port int) error {
	if strings.TrimSpace(host) == "" {
		return fmt.Errorf("%s.host must not be empty", name)
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("%s.port must be between 1 and 65535, got %d", name, port)
	}
	return nil
}

func initConfig(v viper.Viper) (*Bot, *NetSetting, *Napcat, *Storage, *AgentConfig) {
	var bot Bot
	if err := v.UnmarshalKey("bot", &bot); err != nil {
		slog.Error("解析 bot 配置失败", "err", err)
		return nil, nil, nil, nil, nil
	}
	var netSetting NetSetting
	if err := v.UnmarshalKey("netsetting", &netSetting); err != nil {
		slog.Error("解析 netsetting 配置失败", "err", err)
		return nil, nil, nil, nil, nil
	}
	var napcat Napcat
	if err := v.UnmarshalKey("napcat", &napcat); err != nil {
		slog.Error("解析 napcat 配置失败", "err", err)
		return nil, nil, nil, nil, nil
	}
	var storage Storage
	if err := v.UnmarshalKey("storage", &storage); err != nil {
		slog.Error("解析 storage 配置失败", "err", err)
		return nil, nil, nil, nil, nil
	}
	var agent AgentConfig
	if err := v.UnmarshalKey("agent", &agent); err != nil {
		slog.Error("解析 agent 配置失败", "err", err)
		return nil, nil, nil, nil, nil
	}
	return &bot, &netSetting, &napcat, &storage, &agent
}
