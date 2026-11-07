package main

import (
	"Q-Mi/app"
	"Q-Mi/core"
	"Q-Mi/core/server"
	"Q-Mi/event"
	"Q-Mi/storage"
	"Q-Mi/utils/log"
	"Q-Mi/utils/request"
	"Q-Mi/webui"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	// 注册所有事件处理函数(handler 包的 init 里调用 event.Register)
	_ "Q-Mi/handler"
	// agent 包在 init 里注册消息事件,把 AI 成员接入管线
	_ "Q-Mi/agent"
)

func main() {
	fmt.Println("欢迎使用Q-MI Bot")
	logHandler := log.NewCustomLogHandler(os.Stdout, slog.LevelInfo)
	slog.SetDefault(slog.New(logHandler))
	slog.Info("Q-MI Bot正在启动...")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx); err != nil {
		slog.Error("程序运行失败", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	appInstance := app.GetApp()
	if appInstance.Config == nil {
		return fmt.Errorf("配置加载失败，请检查 Config.yaml")
	}

	// 初始化 NapCat API 客户端(失败只告警,收事件不受影响,只是发不出消息)
	if err := request.Init(appInstance.Config.Napcat); err != nil {
		slog.Warn("NapCat API 客户端初始化失败,无法发送消息", "err", err)
	}

	// 打开消息记忆库(失败只告警,本次运行不落库)
	if appInstance.Config.Storage != nil && appInstance.Config.Storage.Enable {
		st, err := storage.Open(appInstance.Config.Storage.Path)
		if err != nil {
			slog.Warn("记忆库打开失败,本次运行不落库", "err", err)
		} else {
			appInstance.Store = st
			defer st.Close()
			slog.Info("记忆库已就绪", "path", appInstance.Config.Storage.Path)
		}
	}

	slog.Info("准备启动服务器")

	c := &core.Core{
		Server: &server.Server{
			Port:    appInstance.Config.NetSetting.Port,
			Host:    appInstance.Config.NetSetting.Host,
			Pattern: appInstance.Config.NetSetting.Pattern,
			Token:   appInstance.Config.NetSetting.Token,
		},
	}

	slog.Info("服务器配置完成，正在启动服务器...")
	if err := c.StartCore(); err != nil {
		return err
	}

	// W1 WebUI：agent 启用且 webui.enable 时启动控制台（独立端口）。
	// Start 非阻塞；退出时与其他服务一并 Shutdown。
	var webuiServer *http.Server
	if appInstance.Config.Agent != nil && appInstance.Config.Agent.Enable &&
		appInstance.Config.WebUI != nil && appInstance.Config.WebUI.Enable {
		srv, err := webui.Start(*appInstance.Config.WebUI)
		if err != nil {
			slog.Warn("WebUI 启动失败，不影响其他功能", "err", err)
		} else {
			webuiServer = srv
		}
	}

	<-ctx.Done()
	slog.Info("收到退出信号，正在等待事件处理完成...")
	if webuiServer != nil {
		if err := webuiServer.Shutdown(context.Background()); err != nil {
			slog.Warn("关闭 WebUI 失败", "err", err)
		}
	}
	if err := c.Server.Shutdown(context.Background()); err != nil {
		return fmt.Errorf("关闭 HTTP 服务失败: %w", err)
	}
	event.Wait()
	return nil
}
