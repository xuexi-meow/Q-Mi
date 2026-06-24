package config

import (
	"log/slog"

	"github.com/spf13/viper"
)

type Config struct{}

func (c *Config) LoadConfig() {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	err := viper.ReadInConfig()
	if err != nil {
		slog.Error("读取配置文件失败 ", "error:", err)
	}

}
