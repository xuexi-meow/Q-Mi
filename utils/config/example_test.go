package config

import (
	"path/filepath"
	"testing"
)

// TestExampleConfigLoads 守卫 Config.example.yaml：字段名/结构漂移会让示例
// 配置在复制后直接启动失败，这里提前拦住。
func TestExampleConfigLoads(t *testing.T) {
	base := filepath.Join("..", "..", "Config.example.yaml")
	store, err := NewStore(Paths{Base: base, Overlay: filepath.Join(t.TempDir(), "Config.webui.yaml")})
	if err != nil {
		t.Fatalf("Config.example.yaml 无法通过加载校验: %v", err)
	}
	defer store.Close()

	cfg := store.Snapshot()
	if cfg == nil || cfg.Bot == nil || cfg.NetSetting == nil || cfg.Agent == nil || cfg.WebUI == nil {
		t.Fatal("示例配置解析结果不完整")
	}
	if cfg.Agent.Context.WithDefaults().MaxInputTokens < 2048 {
		t.Fatal("示例配置的 context 默认值异常")
	}
}
