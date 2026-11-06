package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strings"
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
`

type Config struct {
	Bot        *Bot
	NetSetting *NetSetting
	Napcat     *Napcat
	Storage    *Storage
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

	bot, netSetting, napcat, storageCfg := initConfig(*v)
	if bot == nil || netSetting == nil || napcat == nil || storageCfg == nil {
		return nil
	}
	if err := validateConfig(netSetting, napcat, storageCfg); err != nil {
		slog.Error("配置校验失败", "err", err)
		return nil
	}

	slog.Info("读取配置文件成功")
	return &Config{
		Bot:        bot,
		NetSetting: netSetting,
		Napcat:     napcat,
		Storage:    storageCfg,
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

func validateConfig(netSetting *NetSetting, napcat *Napcat, storage *Storage) error {
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

func initConfig(v viper.Viper) (*Bot, *NetSetting, *Napcat, *Storage) {
	var bot Bot
	if err := v.UnmarshalKey("bot", &bot); err != nil {
		slog.Error("解析 bot 配置失败", "err", err)
		return nil, nil, nil, nil
	}
	var netSetting NetSetting
	if err := v.UnmarshalKey("netsetting", &netSetting); err != nil {
		slog.Error("解析 netsetting 配置失败", "err", err)
		return nil, nil, nil, nil
	}
	var napcat Napcat
	if err := v.UnmarshalKey("napcat", &napcat); err != nil {
		slog.Error("解析 napcat 配置失败", "err", err)
		return nil, nil, nil, nil
	}
	var storage Storage
	if err := v.UnmarshalKey("storage", &storage); err != nil {
		slog.Error("解析 storage 配置失败", "err", err)
		return nil, nil, nil, nil
	}
	return &bot, &netSetting, &napcat, &storage
}
