package app

import (
	"Q-Mi/utils/config"
	"sync"
)

var (
	appInstance *App
	once        sync.Once
)

type App struct {
	Config *config.Config
}

func makeConfig() *config.Config {
	cfg := &config.Config{}
	result := cfg.LoadConfig()
	if result == nil {
		return nil
	}
	return result
}

func GetApp() *App {
	once.Do(func() {
		appInstance = &App{
			Config: makeConfig(),
		}
	})
	return appInstance
}
