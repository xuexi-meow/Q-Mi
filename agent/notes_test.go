package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// TestLoadNotesMissingFileReturnsEmpty notes 文件不存在时返回空笔记且不报错。
func TestLoadNotesMissingFileReturnsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.json")

	notes, err := LoadNotes(path)
	if err != nil {
		t.Fatalf("LoadNotes(不存在文件) 应返回 nil 错误, got %v", err)
	}
	if len(notes) != 0 {
		t.Fatalf("LoadNotes(不存在文件) 应返回空笔记, got %+v", notes)
	}
}

// TestAppendNotePersistsAndLoads 追加笔记后可从磁盘读回，时间与文本一致。
func TestAppendNotePersistsAndLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "notes.json") // 父目录不存在也应创建

	before := time.Now()
	if err := AppendNote(path, "先查一下群名片"); err != nil {
		t.Fatalf("AppendNote: %v", err)
	}
	if err := AppendNote(path, "他刚才在开玩笑"); err != nil {
		t.Fatalf("AppendNote: %v", err)
	}

	notes, err := LoadNotes(path)
	if err != nil {
		t.Fatalf("LoadNotes: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("笔记条数 = %d, want 2: %+v", len(notes), notes)
	}
	if notes[0].Text != "先查一下群名片" || notes[1].Text != "他刚才在开玩笑" {
		t.Errorf("笔记文本不符: %+v", notes)
	}
	for i, n := range notes {
		if n.Time.Before(before.Add(-time.Second)) || n.Time.After(time.Now()) {
			t.Errorf("notes[%d].Time 应为写入时刻附近, got %v", i, n.Time)
		}
	}
}

// TestAppendNoteKeepsOnlyLast50 超过 50 条时只保留最近 50 条。
func TestAppendNoteKeepsOnlyLast50(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.json")
	base := time.Unix(1762400000, 0)
	// 直接落盘 55 条旧笔记
	old := make([]Note, 55)
	for i := range old {
		old[i] = Note{Time: base.Add(time.Duration(i) * time.Minute), Text: "n" + strconv.Itoa(i)}
	}
	data, err := json.Marshal(old)
	if err != nil {
		t.Fatalf("序列化旧笔记: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("写入旧笔记文件: %v", err)
	}

	if err := AppendNote(path, "最新一条"); err != nil {
		t.Fatalf("AppendNote: %v", err)
	}

	notes, err := LoadNotes(path)
	if err != nil {
		t.Fatalf("LoadNotes: %v", err)
	}
	if len(notes) != 50 {
		t.Fatalf("截断后笔记条数 = %d, want 50", len(notes))
	}
	if notes[0].Text != "n6" {
		t.Errorf("最旧一条应为原第 7 条(n6), got %q", notes[0].Text)
	}
	if notes[len(notes)-1].Text != "最新一条" {
		t.Errorf("最新一条应保留在末尾, got %q", notes[len(notes)-1].Text)
	}
}

// TestFormatNotesEmptyReturnsEmpty 空笔记格式化结果为空串。
func TestFormatNotesEmptyReturnsEmpty(t *testing.T) {
	if got := FormatNotes(nil); got != "" {
		t.Errorf("FormatNotes(nil) = %q, want 空串", got)
	}
	if got := FormatNotes([]Note{}); got != "" {
		t.Errorf("FormatNotes([]) = %q, want 空串", got)
	}
}

// TestFormatNotesMultiLine 多条笔记格式化为 "[思考笔记]" 头 + 每条一行。
func TestFormatNotesMultiLine(t *testing.T) {
	t1 := time.Unix(1762400000, 0).Local() // → 11-06 07:33 (本地时区跟随)
	t2 := t1.Add(28 * time.Minute)
	notes := []Note{
		{Time: t1, Text: "先查群名片再回"},
		{Time: t2, Text: "他在开玩笑，别当真"},
	}

	want := "[思考笔记]\n[" + t1.Format("01-02 15:04") + "] 先查群名片再回\n[" +
		t2.Format("01-02 15:04") + "] 他在开玩笑，别当真"
	if got := FormatNotes(notes); got != want {
		t.Errorf("FormatNotes = %q, want %q", got, want)
	}
}
