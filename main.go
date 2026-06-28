package main

import (
	"Q-Mi/app"
	"Q-Mi/core"
	"Q-Mi/core/server"
	"Q-Mi/utils/log"
	"fmt"
	"log/slog"
	"os"
)

func main() {
	fmt.Println("欢迎使用Q-MI Bot")
	logHandler := log.NewCustomLogHandler(os.Stdout, slog.LevelInfo)
	slog.SetDefault(slog.New(logHandler))
	slog.Info("Q-MI Bot正在启动...")

	appInstance := app.GetApp()

	c := &core.Core{
		Server: &server.Server{
			Port:    appInstance.Config.NetSetting.Port,
			Host:    appInstance.Config.NetSetting.Host,
			Pattern: appInstance.Config.NetSetting.Pattern,
		},
	}
	c.StartCore()
}
