package context

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// State state.json 的内容结构。
type State struct {
	Mood    string    `json:"mood"`
	Updated time.Time `json:"updated"`
}

// ReadState 读取状态文件；文件不存在返回零值状态。
func ReadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{}, nil
		}
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, err
	}
	return s, nil
}

// WriteState 写入情绪状态；mood 与现值相同则不写盘（避免无谓的 mtime 抖动）。
func WriteState(path, mood string) error {
	cur, err := ReadState(path)
	if err != nil {
		return fmt.Errorf("读取状态文件失败: %w", err)
	}
	if cur.Mood == mood {
		return nil
	}
	data, err := json.Marshal(State{Mood: mood, Updated: time.Now()})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
