package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"Q-Mi/storage"
	"time"
)

// testComputerCtx 带临时工作区的 ToolContext（围栏测试统一入口）。
func testComputerCtx(t *testing.T, mutate func(*ToolContext)) (*ToolContext, string) {
	t.Helper()
	dir := t.TempDir()
	ctx := &ToolContext{
		Workspace: dir,
		NotesPath: filepath.Join(dir, "notes.json"),
		StatePath: filepath.Join(dir, "state.json"),
		Admin:     true,
		Approvals: NewApprovalManager(nil, nil),
	}
	if mutate != nil {
		mutate(ctx)
	}
	return ctx, dir
}

// TestComputerToolsRegistered computer 板块的 7 个工具都在注册表里。
func TestComputerToolsRegistered(t *testing.T) {
	tools := buildTools(&ToolContext{Workspace: t.TempDir()})
	for _, name := range []string{
		"read_file", "write_file", "list_dir", "delete_file",
		"download_file", "run_command", "save_message_media",
	} {
		findTool(t, tools, name)
	}
}

// TestWriteReadListDeleteFile 文件四件套：写入→读取→列目录→删除；
// 相对路径相对工作区解析。
func TestWriteReadListDeleteFile(t *testing.T) {
	ctx, dir := testComputerCtx(t, nil)
	tools := buildTools(ctx)

	runToolOK(t, tools, "write_file", `{"path":"sub/a.txt","content":"你好工作区"}`)
	out := runToolOK(t, tools, "read_file", `{"path":"sub/a.txt"}`)
	if !strings.Contains(out, "你好工作区") {
		t.Errorf("read_file 内容缺失: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub", "a.txt")); err != nil {
		t.Fatalf("文件应落在工作区内: %v", err)
	}
	list := runToolOK(t, tools, "list_dir", `{"path":"sub"}`)
	if !strings.Contains(list, "a.txt") {
		t.Errorf("list_dir 缺文件: %s", list)
	}
	runToolOK(t, tools, "delete_file", `{"path":"sub/a.txt"}`)
	if _, err := os.Stat(filepath.Join(dir, "sub", "a.txt")); !os.IsNotExist(err) {
		t.Errorf("delete_file 后文件应消失")
	}
}

// TestComputerOutsideWorkspaceNeedsApproval 工作区外路径 → approvalNeeded 信号；
// Approved ctx（管理员已同意）直接放行。
func TestComputerOutsideWorkspaceNeedsApproval(t *testing.T) {
	ctx, _ := testComputerCtx(t, nil)
	tools := buildTools(ctx)

	err := runToolErr(t, tools, "read_file", `{"path":"C:/Windows/win.ini"}`)
	if _, ok := err.(approvalNeeded); !ok {
		t.Fatalf("工作区外读取应返回 approvalNeeded, got %v", err)
	}
	// write 同理
	if err := runToolErr(t, tools, "write_file", `{"path":"C:/tmp/x.txt","content":"x"}`); !isApproval(err) {
		t.Fatalf("工作区外写入应返回 approvalNeeded, got %v", err)
	}
	// 符号链接/相对路径穿越也拦住：..\..\outside.txt
	if err := runToolErr(t, tools, "write_file", `{"path":"../../outside.txt","content":"x"}`); !isApproval(err) {
		t.Fatalf("相对路径穿越应返回 approvalNeeded, got %v", err)
	}
	// Approved ctx 放行
	approved := *ctx
	approved.Approved = true
	approvedTools := buildTools(&approved)
	runToolOK(t, approvedTools, "read_file", `{"path":"C:/Windows/win.ini","max_bytes":64}`)
}

func isApproval(err error) bool {
	_, ok := err.(approvalNeeded)
	return ok
}

// TestRunCommandAutoAndDeny 非危险命令自动执行（真实 PowerShell echo）；
// 危险黑名单命中转审批。
func TestRunCommandAutoAndDeny(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过真实 shell")
	}
	ctx, dir := testComputerCtx(t, nil)
	tools := buildTools(ctx)

	out := runToolOK(t, tools, "run_command", `{"command":"echo qmi-test-ok"}`)
	if !strings.Contains(out, "qmi-test-ok") {
		t.Errorf("echo 输出缺失: %s", out)
	}
	if !strings.Contains(out, `"exit_code":0`) {
		t.Errorf("退出码应为 0: %s", out)
	}
	// 危险黑名单 → 审批
	err := runToolErr(t, tools, "run_command", `{"command":"shutdown /s /t 0"}`)
	if !isApproval(err) {
		t.Fatalf("危险命令应转审批, got %v", err)
	}
	// cwd 越界 → 审批
	err = runToolErr(t, tools, "run_command", `{"command":"echo hi","cwd":"C:/"}`)
	if !isApproval(err) {
		t.Fatalf("越界 cwd 应转审批, got %v", err)
	}
	_ = dir
}

// TestRunCommandTimeout 超时杀进程并返回已有输出 + timeout 标记。
func TestRunCommandTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过真实 shell")
	}
	ctx, _ := testComputerCtx(t, nil)
	tools := buildTools(ctx)
	out := runToolOK(t, tools, "run_command", `{"command":"Start-Sleep -Seconds 30","timeout_seconds":1}`)
	if !strings.Contains(out, `"timeout":true`) {
		t.Errorf("超时应有 timeout 标记: %s", out)
	}
}

// TestDownloadFile 下载落工作区（相对 URL 文件名推断），返回双路径；
// 超 200MB 上限拒绝（用 Content-Length 伪装）。
func TestDownloadFile(t *testing.T) {
	ctx, dir := testComputerCtx(t, nil)
	tools := buildTools(ctx)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("file-body-123"))
	}))
	t.Cleanup(srv.Close)

	out := runToolOK(t, tools, "download_file", `{"url":"`+srv.URL+`/data.bin","filename":"d.bin"}`)
	if !strings.Contains(out, `"napcat_path":"/mnt/`) && !strings.Contains(out, `"napcat_path":"`) {
		t.Errorf("应返回 napcat_path: %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "d.bin")); err != nil {
		t.Fatalf("文件应落工作区: %v", err)
	}

	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "999999999999")
		w.Write([]byte("x"))
	}))
	t.Cleanup(big.Close)
	if err := runToolErr(t, tools, "download_file", `{"url":"`+big.URL+`/big"}`); err == nil || strings.Contains(err.Error(), "approvalNeeded") {
		t.Errorf("超大文件应报错, got %v", err)
	}
}

// TestApprovalFlowEndToEnd 审批全链路：工具返回审批回执 → 通知管理员 →
// 「同意 1」→ 后台执行 → 结果投递；「拒绝 2」→ 拒绝投递。
func TestApprovalFlowEndToEnd(t *testing.T) {
	var (
		mu      sync.Mutex
		notices []string
	)
	deliveries := make(chan Approval, 4)
	m := NewTaskManager(nil)
	approvals := NewApprovalManager(
		func(text string) {
			mu.Lock()
			notices = append(notices, text)
			mu.Unlock()
		},
		func(a Approval, result string, runErr error) {
			deliveries <- a
		},
	)
	ctx := &ToolContext{Workspace: t.TempDir(), Approvals: approvals, Turn: &Turn{ChatType: "group", ChatID: 1, SelfID: 2}}

	// 用 download_file 伪造一个"越界"操作：直接构造 approvalNeeded 的工具
	tool := Tool{
		Name: "danger_op", Description: "测试", Parameters: objectParams(map[string]any{"x": intProp("x")}, nil),
		Run: func(args json.RawMessage) (string, error) {
			return "{}", approvalNeeded{"演示用危险操作"}
		},
	}
	tools := []Tool{tool}
	mgr := &Manager{tasks: m, approvals: approvals}
	receipt, err := mgr.execToolCall(ctx, tools, ToolCall{
		ID: "c1", Function: ToolFunction{Name: "danger_op", Arguments: `{"x":1}`},
	})
	if err != nil {
		t.Fatalf("审批回执应成功: %v", err)
	}
	if !strings.Contains(receipt, `"approval_id":1`) || !strings.Contains(receipt, `"status":"pending"`) {
		t.Fatalf("回执不符: %s", receipt)
	}
	mu.Lock()
	notified := len(notices)
	mu.Unlock()
	if notified != 1 || !strings.Contains(notices[0], "[操作审批 #1] danger_op") {
		t.Fatalf("管理员通知缺失: %v", notices)
	}

	// 拒绝 #1（无 run 闭包的 onSettled 路径）
	if err := approvals.Resolve(1, false); err != nil {
		t.Fatalf("拒绝应成功: %v", err)
	}
	select {
	case a := <-deliveries:
		if a.Status != ApprovalRejected {
			t.Errorf("拒绝状态不符: %s", a.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("拒绝应投递")
	}

	// 同意路径：带 run 的审批单
	ran := make(chan string, 1)
	id, err := approvals.Request(Approval{
		Tool: "danger_op", Args: `{}`, Reason: "test",
		run: func(bool) (string, error) { ran <- "executed"; return "{}", nil },
	})
	if err != nil {
		t.Fatalf("登记失败: %v", err)
	}
	if err := approvals.Resolve(id, true); err != nil {
		t.Fatalf("同意应成功: %v", err)
	}
	select {
	case <-ran:
	case <-time.After(2 * time.Second):
		t.Fatal("同意后应执行")
	}
	select {
	case a := <-deliveries:
		if a.Status != ApprovalApproved {
			t.Errorf("同意状态不符: %s", a.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("同意应投递")
	}
	_ = mgr
}

// TestParseApprovalReply 审批回复解析：同意/拒绝/容错。
func TestParseApprovalReply(t *testing.T) {
	id, approve, ok := parseApprovalReply("同意 1")
	if !ok || !approve || id != 1 {
		t.Errorf("同意 1 = %d/%v/%v", id, approve, ok)
	}
	if id, approve, ok := parseApprovalReply("拒绝　2"); !ok || approve || id != 2 {
		t.Errorf("拒绝 2（全角空格）= %d/%v/%v", id, approve, ok)
	}
	if _, _, ok := parseApprovalReply("在吗"); ok {
		t.Errorf("普通消息不应命中审批解析")
	}
	if _, _, ok := parseApprovalReply("同意"); ok {
		t.Errorf("缺 id 不应命中")
	}
}

// TestSaveMessageMediaFromImage 消息图片 → 工作区文件（走 imageStore 缓存字节）。
func TestSaveMessageMediaFromImage(t *testing.T) {
	s := openTestStore(t)
	imgSrv, _ := servePNG(t, "image/png")
	dir := t.TempDir()
	ims := newImageStore(dir, s)
	putRecordFull(t, s, &storage.MessageRecord{
		MessageID: 55, Direction: "recv", ChatType: "group", ChatID: 888, UserID: 10001,
		SenderName: "小明",
		RawMessage: "[CQ:image]",
		Segments:   []byte(`[{"type":"image","data":{"url":"` + imgSrv.URL + `/a.png"}}]`),
		Time:       1762400000,
	})
	ctx := &ToolContext{Store: s, Images: ims, Workspace: t.TempDir()}
	tools := buildTools(ctx)
	out := runToolOK(t, tools, "save_message_media", `{"message_id":55}`)
	if !strings.Contains(out, `"kind":"image"`) || !strings.Contains(out, "napcat_path") {
		t.Errorf("保存图片输出不符: %s", out)
	}
	// 文件真实存在且字节 == pngBytes
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("输出应为 JSON: %v", err)
	}
	path, _ := obj["path"].(string)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != string(pngBytes) {
		t.Errorf("落盘内容不符: %v", err)
	}
}

// TestNapcatFilePathMapping 路径映射：盘符路径 → /mnt；URL/base64 原样。
func TestNapcatFilePathMapping(t *testing.T) {
	cases := map[string]string{
		`F:\Go\Q-Mi\agent\workspace\a.png`: "/mnt/f/Go/Q-Mi/agent/workspace/a.png",
		"E:/pics/a.png":                    "/mnt/e/pics/a.png",
		"https://e.com/a.png":              "https://e.com/a.png",
		"base64://AAAA":                    "base64://AAAA",
	}
	for in, want := range cases {
		got, err := napcatFilePath(t.TempDir(), in)
		if err != nil {
			t.Fatalf("%s: %v", in, err)
		}
		if got != want {
			t.Errorf("napcatFilePath(%s) = %s, want %s", in, got, want)
		}
	}
	// 相对路径落工作区
	dir := t.TempDir()
	got, err := napcatFilePath(dir, "sub/a.png")
	if err != nil || !strings.HasPrefix(got, "/mnt/") || !strings.HasSuffix(got, "/sub/a.png") {
		t.Errorf("相对路径映射 = %s, err=%v", got, err)
	}
}
