// config.go WebUI 配置页后端（spec 2026-11-09 §8）：
// 只读生效值（密钥脱敏）+ 覆盖层写入 + 试算校验 + 手动重载 + 模型连通性测试。
//
// 写入只落 Config.webui.yaml；需重启才能生效的字段在这里直接被拒，
// 避免出现「改了但没反应」的黑洞。
package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	yaml "go.yaml.in/yaml/v3"

	"Q-Mi/agent"
	"Q-Mi/app"
	"Q-Mi/utils/config"
)

// configStoreProvider 配置仓库接缝：app.GetApp().ConfigStore（可能为 nil）。
var configStoreProvider = func() *config.Store {
	a := app.GetApp()
	if a == nil {
		return nil
	}
	return a.ConfigStore
}

// modelTestTimeout 连通性测试超时（只发 1 token，慢就是不通）。
const modelTestTimeout = 10 * time.Second

// 需重启才能生效的字段（WebUI 只读，写入请求会被拒）。
var restartFields = []string{
	"netsetting", "napcat", "storage", "webui",
	"bot.qq_number", "agent.enable", "agent.browser_executable",
	"agent.workspace_dir", "agent.image_dir",
}

// 热生效字段（页面提示用）。
var hotFields = []string{
	"agent.models", "agent.wake_routes", "agent.max_attempts", "agent.modules",
	"agent.temperature", "agent.max_tool_rounds", "agent.debounce", "agent.self_wake",
	"agent.context", "agent.history", "agent.persona_file", "agent.guide_file",
	"bot.admin_number",
}

type configView struct {
	Effective     map[string]any `json:"effective"`
	Overlay       map[string]any `json:"overlay"`
	Paths         config.Paths   `json:"paths"`
	RestartFields []string       `json:"restart_fields"`
	HotFields     []string       `json:"hot_fields"`
}

// handleConfigGet GET /api/config：生效配置（脱敏）+ 覆盖层 + 路径与分级提示。
func handleConfigGet(w http.ResponseWriter, r *http.Request) {
	store := configStoreProvider()
	if store == nil {
		http.Error(w, "配置仓库不可用", http.StatusServiceUnavailable)
		return
	}
	writeConfigView(w, store)
}

func writeConfigView(w http.ResponseWriter, store *config.Store) {
	snap := store.Snapshot()
	effective, err := configToMap(snap)
	if err != nil {
		slog.Warn("WebUI 配置视图序列化失败", "err", err)
		http.Error(w, "配置序列化失败", http.StatusInternalServerError)
		return
	}
	maskSecrets(effective)
	overlay := store.Overlay()
	maskSecrets(overlay)
	writeJSON(w, configView{
		Effective:     effective,
		Overlay:       overlay,
		Paths:         store.Paths(),
		RestartFields: restartFields,
		HotFields:     hotFields,
	})
}

// handleConfigPut PUT /api/config：把补丁深度合并进覆盖层，校验通过后落盘并热重载。
func handleConfigPut(w http.ResponseWriter, r *http.Request) {
	store := configStoreProvider()
	if store == nil {
		http.Error(w, "配置仓库不可用", http.StatusServiceUnavailable)
		return
	}
	patch, err := decodePatch(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := guardRestartFields(patch); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	stripMasked(patch)
	if err := store.ValidatePatch(patch); err != nil {
		http.Error(w, "配置校验失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	if err := store.UpdateOverlay(func(root map[string]any) {
		config.ApplyPatch(root, patch)
	}); err != nil {
		http.Error(w, "写入配置失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	writeConfigView(w, store)
}

// handleConfigValidate POST /api/config/validate：只试算不落盘。
func handleConfigValidate(w http.ResponseWriter, r *http.Request) {
	store := configStoreProvider()
	if store == nil {
		http.Error(w, "配置仓库不可用", http.StatusServiceUnavailable)
		return
	}
	patch, err := decodePatch(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := guardRestartFields(patch); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	stripMasked(patch)
	if err := store.ValidatePatch(patch); err != nil {
		http.Error(w, "配置校验失败: "+err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// handleConfigReload POST /api/config/reload：手动重读两层配置文件。
func handleConfigReload(w http.ResponseWriter, r *http.Request) {
	store := configStoreProvider()
	if store == nil {
		http.Error(w, "配置仓库不可用", http.StatusServiceUnavailable)
		return
	}
	if err := store.Reload(); err != nil {
		http.Error(w, "重载失败（继续使用上一份有效配置）: "+err.Error(), http.StatusBadRequest)
		return
	}
	writeConfigView(w, store)
}

// handleModelTest POST /api/models/test：对已保存的候选发一次 1-token 请求。
func handleModelTest(w http.ResponseWriter, r *http.Request) {
	store := configStoreProvider()
	if store == nil {
		http.Error(w, "配置仓库不可用", http.StatusServiceUnavailable)
		return
	}
	var req struct {
		Tier string `json:"tier"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		http.Error(w, "请求体格式错误", http.StatusBadRequest)
		return
	}
	snap := store.Snapshot()
	if snap == nil || snap.Agent == nil {
		http.Error(w, "agent 未配置", http.StatusBadRequest)
		return
	}
	tier, ok := snap.Agent.Models[req.Tier]
	if !ok {
		http.Error(w, "档位不存在: "+req.Tier, http.StatusNotFound)
		return
	}
	var chosen *config.ModelCandidate
	cands := tier.EffectiveCandidates(req.Tier)
	for i := range cands {
		if cands[i].Name == req.Name {
			chosen = &cands[i]
			break
		}
	}
	if chosen == nil {
		http.Error(w, "候选不存在: "+req.Name, http.StatusNotFound)
		return
	}
	baseURL, key, model := chosen.BaseURL, chosen.APIKey, chosen.Model
	if key == "" && chosen.APIKeyEnv != "" {
		key = os.Getenv(chosen.APIKeyEnv)
	}
	if baseURL == "" {
		baseURL, key = snap.Agent.BaseURL, snap.Agent.APIKey
	}
	if baseURL == "" || key == "" || model == "" {
		http.Error(w, "候选接入参数不完整（base_url / api_key / model）", http.StatusBadRequest)
		return
	}

	client := agent.NewClient(baseURL, key, modelTestTimeout)
	start := time.Now()
	_, err := client.Chat(r.Context(), agent.ChatRequest{
		Model: model, MaxTokens: 1,
		Messages: []agent.ChatMessage{{Role: "user", Content: "ping"}},
	})
	out := map[string]any{
		"ok":         err == nil,
		"tier":       req.Tier,
		"name":       req.Name,
		"model":      model,
		"latency_ms": time.Since(start).Milliseconds(),
	}
	if err != nil {
		out["error"] = err.Error()
	}
	writeJSON(w, out)
}

// configToMap 走 YAML 往返把结构体转成与配置文件同名的 map（yaml 标签为准）。
func configToMap(cfg *config.Config) (map[string]any, error) {
	out := map[string]any{}
	if cfg == nil {
		return out, nil
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// isSecretKey 需要脱敏的键名。
func isSecretKey(key string) bool {
	switch strings.ToLower(key) {
	case "api_key", "token":
		return true
	}
	return false
}

// maskSecrets 递归替换密钥值，只保留首尾便于辨识。
func maskSecrets(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if isSecretKey(key) {
				if s, ok := item.(string); ok {
					v[key] = maskSecret(s)
				}
				continue
			}
			maskSecrets(item)
		}
	case []any:
		for _, item := range v {
			maskSecrets(item)
		}
	}
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:3] + "****" + s[len(s)-4:]
}

// decodePatch 解析补丁体：保留数字精度（UseNumber），限制 1MB。
func decodePatch(r *http.Request) (map[string]any, error) {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.UseNumber()
	var patch map[string]any
	if err := dec.Decode(&patch); err != nil {
		return nil, fmt.Errorf("请求体不是合法 JSON 对象: %w", err)
	}
	if patch == nil {
		return nil, fmt.Errorf("请求体不能为空")
	}
	return normalizeNumbers(patch).(map[string]any), nil
}

// normalizeNumbers json.Number → int64/float64，避免覆盖层把整数写成浮点。
func normalizeNumbers(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			v[key] = normalizeNumbers(item)
		}
		return v
	case []any:
		for i, item := range v {
			v[i] = normalizeNumbers(item)
		}
		return v
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return i
		}
		if f, err := v.Float64(); err == nil {
			return f
		}
		return v.String()
	default:
		return value
	}
}

// guardRestartFields 拒绝修改需重启的字段（嵌套路径用 . 连接）。
func guardRestartFields(patch map[string]any) error {
	for _, field := range restartFields {
		if pathPresent(patch, strings.Split(field, ".")) {
			return fmt.Errorf("字段 %s 需要重启才能生效，WebUI 不支持修改", field)
		}
	}
	return nil
}

// pathPresent 判断补丁里是否出现该路径（父节点是 map 且含键）。
func pathPresent(root map[string]any, path []string) bool {
	cur := root
	for i, key := range path {
		next, ok := cur[key]
		if !ok {
			return false
		}
		if i == len(path)-1 {
			return true
		}
		child, ok := next.(map[string]any)
		if !ok {
			return false
		}
		cur = child
	}
	return false
}

// stripMasked 删除值为掩码的键：客户端回传 "sk-****abcd" 表示「不修改」，
// 删掉后由基础层/旧覆盖层继续提供原值。
func stripMasked(value any) {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if s, ok := item.(string); ok && strings.Contains(s, "****") {
				delete(v, key)
				continue
			}
			stripMasked(item)
		}
	case []any:
		for _, item := range v {
			stripMasked(item)
		}
	}
}
