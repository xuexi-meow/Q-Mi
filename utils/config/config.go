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
`

type Config struct {
	Bot        *Bot
	NetSetting *NetSetting
}

type Bot struct {
	QQ_Number    int   `yaml:"qq_numner"`
	Admin_Number []int `yaml:"admin_number"`
}

type NetSetting struct {
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
	Pattern string `yaml:"pattern"`
	Token   string `yaml:"token"`
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
				slog.Error("读取配置文件失败:", err)
				return nil
			}
		} else {
			slog.Error("读取配置文件失败:", err)
			return nil
		}
	}

	bot, netSetting := initConfig(*v)
	if bot == nil || netSetting == nil {
		return nil
	}

	slog.Info("读取配置文件成功")
	return &Config{
		Bot:        bot,
		NetSetting: netSetting,
	}
}

func makeNewConfig() bool {
	if _, err := os.OpenFile("Config.yaml", os.O_CREATE, 0777); err != nil {
		slog.Error("创建配置文件失败:", err)
		return false
	}
	if err := os.WriteFile("Config.yaml", []byte(DefaultConfig), 0777); err != nil {
		slog.Error("写入配置文件失败:", err)
		return false
	}
	return true
}

func initConfig(v viper.Viper) (*Bot, *NetSetting) {
	var bot Bot
	if err := v.UnmarshalKey("bot", &bot); err != nil {
		slog.Error("解析 bot 配置失败:", err)
		return nil, nil
	}
	var netSetting NetSetting
	if err := v.UnmarshalKey("netsetting", &netSetting); err != nil {
		slog.Error("解析 netsetting 配置失败:", err)
		return nil, nil
	}
	return &bot, &netSetting
}
