package config

import (
	"log/slog"

	"os"

	"github.com/spf13/viper"
)

const DefaultConfig = `bot:
  qq_number: 0
  admin_number:
	- 1234
netsetting:
  host: "0.0.0.0"
  port: 11451
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

	slog.Info("读取配置文件成功")
	return &Config{
		Bot:        bot,
		NetSetting: netSetting,
		Napcat:     napcat,
		Storage:    storageCfg,
	}
}

func makeNewConfig() bool {
	if _, err := os.OpenFile("Config.yaml", os.O_CREATE, 0777); err != nil {
		slog.Error("创建配置文件失败", "err", err)
		return false
	}
	if err := os.WriteFile("Config.yaml", []byte(DefaultConfig), 0777); err != nil {
		slog.Error("写入配置文件失败", "err", err)
		return false
	}
	return true
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
