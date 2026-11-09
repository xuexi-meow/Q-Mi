// store.go 配置分层与快照（spec 2026-11-09 §2/§3/§4）：
// Config.yaml（手写基础层）+ Config.webui.yaml（机器写入的覆盖层）深度合并，
// 解码校验后整体替换不可变快照。读侧永远拿 Snapshot()，写侧先校验再交换；
// 内容未变化不通知，重载失败保留 last-good。
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	yaml "go.yaml.in/yaml/v3"
)

// DefaultOverlayPath WebUI 覆盖层默认文件名（与基础配置同目录，进 .gitignore）。
const DefaultOverlayPath = "Config.webui.yaml"

// defaultReloadDebounce 文件变更防抖：编辑器保存会连续触发多次写事件。
const defaultReloadDebounce = 500 * time.Millisecond

// overlayHeader 覆盖层文件头。文件由程序维护，手改会在下次 WebUI 提交时被覆盖。
const overlayHeader = "# 由 WebUI 写入的配置覆盖层，不要手改；删除本文件即恢复 Config.yaml 的值。\n" +
	"# 值为 null 表示删除该键；数组整体替换，不按索引合并。\n"

// errFileMissing 标记文件不存在：基础层缺失是错误，覆盖层缺失是合法的空覆盖。
var errFileMissing = errors.New("配置文件不存在")

// Paths 配置来源路径。Overlay 为空时使用 DefaultOverlayPath。
type Paths struct {
	Base    string
	Overlay string
}

// Store 配置快照仓库：加载、分层合并、校验、原子替换、变更通知与文件监听。
//
// 并发约定：
//   - Snapshot() 返回的 *Config 视为不可变，禁止修改；需要变更走副本 + 整体替换
//   - Subscribe 回调按注册顺序串行执行，且不得调用 Reload（会死锁）；
//     需要在回调里拿最新配置时用 Snapshot()
//   - 内容未变化不触发回调
type Store struct {
	paths Paths

	cur atomic.Pointer[Config] // 当前有效快照

	mu      sync.RWMutex
	base    map[string]any // 最近一次成功加载的基础层原文
	overlay map[string]any // 最近一次成功加载的覆盖层原文
	subs    []func(old, new *Config)
	onErr   func(error)

	reloadMu sync.Mutex // 串行化「读盘→校验→交换→通知」
	writeMu  sync.Mutex // 串行化覆盖层写入

	watchMu  sync.Mutex
	watcher  *fsnotify.Watcher
	done     chan struct{}
	closeOne sync.Once
	debounce time.Duration
}

// NewStore 按路径加载并校验配置。基础层缺失或校验失败返回 error（启动失败语义）；
// 覆盖层缺失视为空覆盖层。
func NewStore(paths Paths) (*Store, error) {
	if paths.Base == "" {
		return nil, errors.New("配置基础路径为空")
	}
	if paths.Overlay == "" {
		paths.Overlay = DefaultOverlayPath
	}
	s := &Store{
		paths:    paths,
		done:     make(chan struct{}),
		debounce: defaultReloadDebounce,
	}
	base, err := readYAMLFile(paths.Base)
	if err != nil {
		if errors.Is(err, errFileMissing) {
			return nil, fmt.Errorf("基础配置 %s 不存在", paths.Base)
		}
		return nil, fmt.Errorf("读取基础配置 %s 失败: %w", paths.Base, err)
	}
	overlay, err := readYAMLFile(paths.Overlay)
	if err != nil && !errors.Is(err, errFileMissing) {
		return nil, fmt.Errorf("读取覆盖配置 %s 失败: %w", paths.Overlay, err)
	}
	if errors.Is(err, errFileMissing) {
		overlay = map[string]any{}
	}
	cfg, err := configFromMaps(base, overlay)
	if err != nil {
		return nil, err
	}
	s.base, s.overlay = base, overlay
	s.cur.Store(cfg)
	return s, nil
}

// LoadOrInit 确保基础配置存在（缺失时按默认模板创建），再构造 Store。
// 保留旧 LoadConfig 的「首次运行自动生成 Config.yaml」行为。
func LoadOrInit(paths Paths) (*Store, error) {
	if paths.Base == "" {
		paths.Base = "Config.yaml"
	}
	if paths.Overlay == "" {
		paths.Overlay = DefaultOverlayPath
	}
	if _, err := os.Lstat(paths.Base); errors.Is(err, fs.ErrNotExist) {
		slog.Warn("未找到配置文件，将创建默认文件")
		if err := writeFileAtomic(paths.Base, []byte(DefaultConfig), 0o600); err != nil {
			return nil, fmt.Errorf("创建配置文件失败: %w", err)
		}
	}
	return NewStore(paths)
}

// Snapshot 返回当前有效配置快照。返回值只读，调用方不得修改。
func (s *Store) Snapshot() *Config {
	if s == nil {
		return nil
	}
	return s.cur.Load()
}

// Reload 从磁盘重新读取两层配置：合并 → 校验 → 替换快照 → 通知订阅者。
// 任一环节失败都保留旧快照并返回 error（同时触发 OnError 回调）。
func (s *Store) Reload() error {
	s.reloadMu.Lock()

	base, err := readYAMLFile(s.paths.Base)
	if err != nil {
		s.reloadMu.Unlock()
		if errors.Is(err, errFileMissing) {
			return s.reportErr(fmt.Errorf("基础配置 %s 不存在", s.paths.Base))
		}
		return s.reportErr(fmt.Errorf("读取基础配置失败: %w", err))
	}
	overlay, err := readYAMLFile(s.paths.Overlay)
	if err != nil && !errors.Is(err, errFileMissing) {
		s.reloadMu.Unlock()
		return s.reportErr(fmt.Errorf("读取覆盖配置失败: %w", err))
	}
	if errors.Is(err, errFileMissing) {
		overlay = map[string]any{}
	}
	cfg, err := configFromMaps(base, overlay)
	if err != nil {
		s.reloadMu.Unlock()
		return s.reportErr(err)
	}

	old := s.cur.Load()
	changed := old == nil || !reflect.DeepEqual(old, cfg)

	s.mu.Lock()
	s.base, s.overlay = base, overlay
	subs := append([]func(*Config, *Config){}, s.subs...)
	s.mu.Unlock()

	if changed {
		s.cur.Store(cfg)
	}
	// 回调在 reloadMu 内串行执行：保证通知顺序与快照替换顺序一致。
	s.reloadMu.Unlock()

	if changed {
		for _, fn := range subs {
			fn(old, cfg)
		}
		slog.Info("配置已重新加载")
	}
	return nil
}

// UpdateOverlay 读覆盖层 → 应用变更 → 校验 → 原子落盘 → 重载。
// 校验失败不落盘、不替换快照，返回 error；mutate 收到的是覆盖层可变副本。
func (s *Store) UpdateOverlay(mutate func(map[string]any)) error {
	if mutate == nil {
		return errors.New("缺少覆盖层变更函数")
	}
	s.writeMu.Lock()
	overlay, err := readYAMLFile(s.paths.Overlay)
	if errors.Is(err, errFileMissing) {
		overlay, err = map[string]any{}, nil
	}
	if err != nil {
		s.writeMu.Unlock()
		return s.reportErr(fmt.Errorf("读取覆盖配置失败: %w", err))
	}
	if overlay == nil {
		overlay = map[string]any{}
	}
	mutate(overlay)

	// 先校验（用当前基础层），通过后再落盘，避免坏配置写进文件。
	if _, err := configFromMaps(s.baseCopy(), overlay); err != nil {
		s.writeMu.Unlock()
		return err
	}
	data, err := yaml.Marshal(overlay)
	if err != nil {
		s.writeMu.Unlock()
		return fmt.Errorf("序列化覆盖配置失败: %w", err)
	}
	body := append([]byte(overlayHeader), data...)
	if err := writeFileAtomic(s.paths.Overlay, body, 0o600); err != nil {
		s.writeMu.Unlock()
		return fmt.Errorf("写入覆盖配置失败: %w", err)
	}
	s.writeMu.Unlock()
	return s.Reload()
}

// Overlay 返回覆盖层深拷贝（WebUI 展示「哪些键被覆盖」用）。
func (s *Store) Overlay() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return deepCopyMap(s.overlay)
}

// Base 返回基础层深拷贝（WebUI 展示「文件里写了什么」用）。
func (s *Store) Base() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return deepCopyMap(s.base)
}

// ApplyPatch 把补丁深度合并进 dst（数组整体替换、null 删除），返回 dst。
// 供 WebUI 在 UpdateOverlay 的 mutate 回调里使用。
func ApplyPatch(dst, patch map[string]any) map[string]any {
	return mergeMaps(dst, patch)
}

// ValidatePatch 用「当前覆盖层 + 补丁」试算一次完整配置并校验，不落盘。
func (s *Store) ValidatePatch(patch map[string]any) error {
	s.mu.RLock()
	base, overlay := deepCopyMap(s.base), deepCopyMap(s.overlay)
	s.mu.RUnlock()
	_, err := configFromMaps(base, mergeMaps(overlay, patch))
	return err
}

// Subscribe 注册配置变更回调；返回取消函数。
func (s *Store) Subscribe(fn func(old, new *Config)) (cancel func()) {
	if fn == nil {
		return func() {}
	}
	s.mu.Lock()
	s.subs = append(s.subs, fn)
	index := len(s.subs) - 1
	s.mu.Unlock()
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if index < len(s.subs) {
			s.subs[index] = nil
		}
	}
}

// OnError 注册重载失败回调（坏配置被拒绝时触发）；回调不得调用 Reload。
func (s *Store) OnError(fn func(error)) {
	s.mu.Lock()
	s.onErr = fn
	s.mu.Unlock()
}

// Paths 返回当前使用的配置路径。
func (s *Store) Paths() Paths { return s.paths }

// baseCopy 基础层深拷贝（UpdateOverlay 校验用，避免持锁读）。
func (s *Store) baseCopy() map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return deepCopyMap(s.base)
}

func (s *Store) reportErr(err error) error {
	if err == nil {
		return nil
	}
	slog.Warn("配置重载失败，继续使用上一份有效配置", "err", err)
	s.mu.RLock()
	fn := s.onErr
	s.mu.RUnlock()
	if fn != nil {
		fn(err)
	}
	return err
}

// readYAMLFile 读取并解析 YAML 为通用 map。文件不存在返回 errFileMissing；
// 空文件视为空 map。
func readYAMLFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, errFileMissing
		}
		return nil, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

// configFromMaps 合并两层后交给 viper 解码 + 现有校验，避免第二套解析逻辑。
func configFromMaps(base, overlay map[string]any) (*Config, error) {
	merged := mergeMaps(deepCopyMap(base), overlay)
	v := viper.New()
	if len(merged) > 0 {
		if err := v.MergeConfigMap(merged); err != nil {
			return nil, fmt.Errorf("合并配置失败: %w", err)
		}
	}
	return decodeConfig(v)
}

// mergeMaps 深度合并：map 递归，标量与数组整体替换，显式 null 删除键。
// dst 会被修改并返回（调用方负责传入副本）。
func mergeMaps(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = make(map[string]any, len(src))
	}
	for key, value := range src {
		if value == nil {
			delete(dst, key)
			continue
		}
		if srcMap, ok := value.(map[string]any); ok {
			if dstMap, ok := dst[key].(map[string]any); ok {
				dst[key] = mergeMaps(dstMap, srcMap)
				continue
			}
			dst[key] = mergeMaps(make(map[string]any, len(srcMap)), srcMap)
			continue
		}
		dst[key] = value
	}
	return dst
}

// deepCopyMap 深拷贝 map（数组按元素复制，避免覆盖层与快照共享底层数组）。
func deepCopyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	out := make(map[string]any, len(src))
	for key, value := range src {
		out[key] = deepCopyValue(value)
	}
	return out
}

func deepCopyValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		return deepCopyMap(v)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = deepCopyValue(item)
		}
		return out
	default:
		return v
	}
}

// writeFileAtomic 同目录临时文件 + rename 原子替换（读侧永远看到完整文件）。
func writeFileAtomic(path string, data []byte, perm fs.FileMode) error {
	dir := filepath.Dir(path)
	if dir == "" {
		dir = "."
	}
	tmp, err := os.CreateTemp(dir, ".qmi-config-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // rename 成功后为 no-op
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
