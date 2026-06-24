package main

import (
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
}
