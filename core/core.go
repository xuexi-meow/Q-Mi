package core

import "Q-Mi/core/server"

type Core struct{
	Server *server.Server
}

func (c *Core)StartCore() {
	c.Server.StartServer()
}