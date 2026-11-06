// agent/notes.go 思考笔记存储（spec §3.5）：think 工具把笔记落盘为
// JSON 数组文件，只保留最近 maxNotes 条；下一轮思考由 manager 读出并
// 经 Turn 注入最终 user 消息头部（不进 system，保缓存前缀）。
package agent

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// maxNotes 笔记文件保留的最近条数上限。
const maxNotes = 50

// Note 一条思考笔记。
type Note struct {
	Time time.Time `json:"time"`
	Text string    `json:"text"`
}

// LoadNotes 读取笔记文件；文件不存在视为无笔记，返回空。
func LoadNotes(path string) ([]Note, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var notes []Note
	if err := json.Unmarshal(data, &notes); err != nil {
		return nil, err
	}
	return notes, nil
}

// AppendNote 追加一条笔记并只保留最近 maxNotes 条；父目录不存在自动创建。
func AppendNote(path, text string) error {
	notes, err := LoadNotes(path)
	if err != nil {
		return err
	}
	notes = append(notes, Note{Time: time.Now(), Text: text})
	if len(notes) > maxNotes {
		notes = notes[len(notes)-maxNotes:]
	}
	data, err := json.Marshal(notes)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// FormatNotes 把笔记格式化为注入上下文的文本块：
// "[思考笔记]\n[MM-DD HH:MM] 内容..." 每条一行；无笔记返回空串。
func FormatNotes(notes []Note) string {
	if len(notes) == 0 {
		return ""
	}
	lines := make([]string, 0, len(notes)+1)
	lines = append(lines, "[思考笔记]")
	for _, n := range notes {
		lines = append(lines, "["+n.Time.Format(timeLayout)+"] "+n.Text)
	}
	return strings.Join(lines, "\n")
}
