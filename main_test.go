package main

import (
	"context"
	"net"
	"testing"

	"Q-Mi/app"
	"Q-Mi/utils/config"
)

func TestRunRejectsMissingConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	a := app.GetApp()
	saved := a.Config
	a.Config = nil
	t.Cleanup(func() { a.Config = saved })
	if err := run(context.Background()); err == nil {
		t.Fatal("无有效配置时应返回启动错误")
	}
}

func TestRunReturnsListenFailure(t *testing.T) {
	t.Chdir(t.TempDir())
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	a := app.GetApp()
	saved := a.Config
	a.Config = &config.Config{
		NetSetting: &config.NetSetting{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Pattern: "/Q-Mi"},
		Napcat:     &config.Napcat{},
	}
	t.Cleanup(func() { a.Config = saved })
	if err := run(context.Background()); err == nil {
		t.Fatal("监听失败时应返回错误，而不是继续等待退出信号")
	}
}
