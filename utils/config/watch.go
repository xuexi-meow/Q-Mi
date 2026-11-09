// watch.go 配置文件监听（spec 2026-11-09 §3/§4）：监听两层配置所在目录并按
// 文件名过滤，兼容编辑器「写临时文件 + rename 覆盖」的保存方式；500ms 防抖后
// 自动 Reload，坏配置由 reportErr 保留旧快照。
package config

import (
	"errors"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch 启动文件监听（幂等）。重复调用无副作用。
func (s *Store) Watch() error {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	select {
	case <-s.done:
		return errors.New("配置 Store 已关闭")
	default:
	}
	if s.watcher != nil {
		return nil
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	added := map[string]bool{}
	for _, path := range []string{s.paths.Base, s.paths.Overlay} {
		dir := filepath.Dir(path)
		if dir == "" {
			dir = "."
		}
		if added[dir] {
			continue
		}
		if err := w.Add(dir); err != nil {
			w.Close()
			return err
		}
		added[dir] = true
	}
	s.watcher = w
	go s.watchLoop(w)
	slog.Info("配置热重载已启用", "base", s.paths.Base, "overlay", s.paths.Overlay)
	return nil
}

// watchLoop 目录事件 → 文件名过滤 → 防抖 → Reload。
func (s *Store) watchLoop(w *fsnotify.Watcher) {
	baseName := filepath.Base(s.paths.Base)
	overlayName := filepath.Base(s.paths.Overlay)
	fire := make(chan struct{}, 1)
	var timer *time.Timer
	schedule := func() {
		if timer == nil {
			timer = time.AfterFunc(s.debounce, func() {
				select {
				case fire <- struct{}{}:
				default:
				}
			})
			return
		}
		timer.Reset(s.debounce)
	}
	for {
		select {
		case <-s.done:
			if timer != nil {
				timer.Stop()
			}
			return
		case event, ok := <-w.Events:
			if !ok {
				return
			}
			if event.Op&fsnotify.Chmod == fsnotify.Chmod && event.Op == fsnotify.Chmod {
				continue // 纯权限变更不影响内容
			}
			name := filepath.Base(event.Name)
			if name != baseName && name != overlayName {
				continue
			}
			// 编辑器保存、rename 覆盖、删除重建都按「文件可能变了」处理。
			schedule()
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			slog.Warn("配置监听错误", "err", err)
		case <-fire:
			if timer != nil {
				timer.Stop()
				timer = nil
			}
			_ = s.Reload() // 失败已由 reportErr 记录并保留旧配置
		}
	}
}

// Close 停止监听并释放资源；可重复调用。
func (s *Store) Close() error {
	s.watchMu.Lock()
	defer s.watchMu.Unlock()
	s.closeOne.Do(func() { close(s.done) })
	if s.watcher == nil {
		return nil
	}
	err := s.watcher.Close()
	s.watcher = nil
	return err
}
