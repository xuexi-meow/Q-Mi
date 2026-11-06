package core

import (
	"Q-Mi/core/server"
	"log/slog"
)

type Core struct {
	Server *server.Server
}

func (c *Core) StartCore() error {
	slog.Info("核心正在运行StartServer...")
	return c.Server.StartServer()
}
