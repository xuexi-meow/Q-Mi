package app

import (
	"log/slog"
	"sync"

	"Q-Mi/storage"
	"Q-Mi/utils/config"
)

var (
	appInstance *App
	once        sync.Once
)

// App 进程级单例。Config 为当前生效配置快照（热重载后由订阅回调整体替换），
// ConfigStore 提供分层加载与重载能力。需重启才能生效的字段以启动时的值为准。
type App struct {
	mu          sync.RWMutex
	Config      *config.Config
	ConfigStore *config.Store
	Store       *storage.Store // nil 表示记忆库未启用
}

// GetApp 惰性构造单例：加载分层配置（Config.yaml + Config.webui.yaml），
// 并订阅重载以更新 Config 指针。加载失败时 Config 为 nil，由 main 判定为启动失败。
func GetApp() *App {
	once.Do(func() {
		a := &App{}
		store, err := config.LoadOrInit(config.Paths{
			Base:    "Config.yaml",
			Overlay: config.DefaultOverlayPath,
		})
		if err != nil {
			slog.Error("配置加载失败", "err", err)
			appInstance = a
			return
		}
		a.ConfigStore = store
		a.Config = store.Snapshot()
		store.Subscribe(func(_, next *config.Config) {
			if next == nil {
				return
			}
			a.mu.Lock()
			a.Config = next
			a.mu.Unlock()
		})
		slog.Info("读取配置文件成功")
		appInstance = a
	})
	return appInstance
}

// Current 返回当前生效配置快照：配置热重载后无需重启即可读到新值。
// 返回值只读，调用方不得修改。
func (a *App) Current() *config.Config {
	if a == nil {
		return nil
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.Config
}
