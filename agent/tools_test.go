package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"Q-Mi/storage"
)

// agentStateFile state.json 的内容结构（与实现一致）。
type agentStateFile struct {
	Mood    string    `json:"mood"`
	Updated time.Time `json:"updated"`
}

func readState(t *testing.T, path string) (agentStateFile, error) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return agentStateFile{}, err
	}
	var s agentStateFile
	err = json.Unmarshal(data, &s)
	return s, err
}

func osStat(path string) (os.FileInfo, error) { return os.Stat(path) }

// testToolSet 用临时 notes/state 路径构造工具集，避免测试污染 agent/ 目录。
// 默认非管理员（isAdmin=false）；需要管理员视角用 testToolSetAdmin。
// 不带计时器与 chat 上下文（schedule/cancel_timer 应报「定时器不可用」）。
// images 为图片下载缓存（view_message_images 用），按需注入。
func testToolSet(t *testing.T, store *storage.Store) ([]Tool, string, string) {
	t.Helper()
	dir := t.TempDir()
	notesPath := filepath.Join(dir, "notes.json")
	statePath := filepath.Join(dir, "state.json")
	return newToolSet(store, notesPath, statePath, false, nil, nil, nil), notesPath, statePath
}

// testToolSetAdmin 管理员视角的工具集（危险工具放行）。
func testToolSetAdmin(t *testing.T) []Tool {
	t.Helper()
	dir := t.TempDir()
	return newToolSet(nil, filepath.Join(dir, "notes.json"), filepath.Join(dir, "state.json"), true, nil, nil, nil)
}

// testToolSetWithTimers 带真实 TimerStore（临时路径）与 chat 上下文 turn 的
// 工具集（P3.5 定时工具走通用）；返回工具集与计时器存储。
func testToolSetWithTimers(t *testing.T) ([]Tool, *TimerStore) {
	t.Helper()
	dir := t.TempDir()
	notesPath := filepath.Join(dir, "notes.json")
	statePath := filepath.Join(dir, "state.json")
	timer := NewTimerStore(filepath.Join(dir, "timers.json"), func(ScheduledTimer) {})
	turn := &Turn{ChatType: "group", ChatID: 888, SelfID: 999}
	return newToolSet(nil, notesPath, statePath, false, turn, timer, nil), timer
}

// testToolSetForViewImages 带 store、chat 上下文 turn 与图片存储的工具集
// （view_message_images / 查询自动带图测试用），返回工具集与工具会写入的 turn。
func testToolSetForViewImages(t *testing.T, store *storage.Store, images *imageStore) ([]Tool, *Turn) {
	t.Helper()
	dir := t.TempDir()
	turn := &Turn{ChatType: "group", ChatID: 888, SelfID: 999}
	return newToolSet(store, filepath.Join(dir, "notes.json"), filepath.Join(dir, "state.json"),
		false, turn, nil, images), turn
}

// findTool 按名字取工具。
func findTool(t *testing.T, tools []Tool, name string) Tool {
	t.Helper()
	for _, tl := range tools {
		if tl.Name == name {
			return tl
		}
	}
	t.Fatalf("工具集中没有 %q", name)
	return Tool{}
}

// runToolOK 执行工具并要求成功，返回输出。
func runToolOK(t *testing.T, tools []Tool, name, args string) string {
	t.Helper()
	tl := findTool(t, tools, name)
	if tl.Run == nil {
		t.Fatalf("工具 %q 没有 Run", name)
	}
	out, err := tl.Run(json.RawMessage(args))
	if err != nil {
		t.Fatalf("%s(%s) 应成功, got err: %v", name, args, err)
	}
	return out
}

// runToolErr 执行工具并要求返回 error。
func runToolErr(t *testing.T, tools []Tool, name, args string) error {
	t.Helper()
	tl := findTool(t, tools, name)
	out, err := tl.Run(json.RawMessage(args))
	if err == nil {
		t.Fatalf("%s(%s) 应报错, got output %q", name, args, out)
	}
	return err
}

// wantToolNames P2 核心工具 + P3-2 QQ 能力工具（顺序即工具集顺序，help 恒在末尾）。
var wantToolNames = []string{
	"get_recent_messages", "search_messages", "view_message_images",
	"send_message", "think", "update_mood",
	// 表情收藏与发送
	"save_sticker", "list_stickers", "delete_sticker", "collect_qq_sticker",
	// P3-5 定时器工具
	"schedule", "cancel_timer",
	"finish",
	// P3-2 人性化 QQ 工具
	"recall_message", "get_friend_list",
	"lookup_user", "lookup_groups", "lookup_group", "lookup_members", "lookup_member",
	"set_group_card", "set_group_special_title",
	"group_ban", "group_whole_ban", "kick_member", "set_group_admin",
	"set_group_name", "leave_group", "send_group_notice",
	"set_essence", "remove_essence",
	"poke", "send_like",
	"handle_friend_request", "handle_group_request",
	"get_msg_history", "get_forward_detail",
	"upload_group_file", "get_group_files",
	// P3-2 万能逃生舱
	"napcat_api", "napcat_docs",
	// P4 QQ 空间工具
	"post_qzone", "browse_qzone_feeds", "read_qzone_post",
	"qzone_like", "comment_qzone", "qzone_profile", "delete_qzone",
	// 收尾
	"help",
}

// dangerousToolNames 需要管理员权限的工具。
var dangerousToolNames = []string{"kick_member", "set_group_admin", "leave_group"}

// TestToolSetContainsAllTools 工具集包含全部工具，finish 无 Run（纯终止标记）。
func TestToolSetContainsAllTools(t *testing.T) {
	tools, _, _ := testToolSet(t, nil)
	got := make([]string, 0, len(tools))
	for _, tl := range tools {
		got = append(got, tl.Name)
		if tl.Description == "" {
			t.Errorf("工具 %q 缺少描述", tl.Name)
		}
		if tl.Parameters == nil {
			t.Errorf("工具 %q 缺少参数 schema", tl.Name)
		}
	}
	if !reflect.DeepEqual(got, wantToolNames) {
		t.Errorf("工具名 = %v, want %v", got, wantToolNames)
	}
	if findTool(t, tools, "finish").Run != nil {
		t.Error("finish 不应有 Run（只作循环终止标记）")
	}
}

// TestDangerousToolsBlockedForNonAdmin 非管理员调用危险工具直接报权限错误
// 且不发起 NapCat 请求；管理员视角（testToolSetAdmin）正常放行。
func TestDangerousToolsBlockedForNonAdmin(t *testing.T) {
	for _, name := range dangerousToolNames {
		t.Run(name, func(t *testing.T) {
			nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
			tools, _, _ := testToolSet(t, nil)
			err := runToolErr(t, tools, name, `{"group_id":888,"user_id":5,"enable":true,"reject_add_request":true}`)
			if !strings.Contains(err.Error(), "管理员权限") {
				t.Errorf("非管理员 %s 报错应含「管理员权限」, got %v", name, err)
			}
			if nap.count() != 0 {
				t.Errorf("非管理员 %s 不应发起 NapCat 请求, got %d 次", name, nap.count())
			}
			adminTools := testToolSetAdmin(t)
			runToolOK(t, adminTools, name, `{"group_id":888,"user_id":5,"enable":true,"reject_add_request":true}`)
			if nap.count() != 1 {
				t.Errorf("管理员调用 %s 应发出 1 次请求, got %d", name, nap.count())
			}
		})
	}
}

// TestHelpToolDescribesAllTools help 从工具定义生成全部工具的
// 名称+描述+参数 schema（单一源头），JSON 数组、无空白。
func TestHelpToolDescribesAllTools(t *testing.T) {
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "help", "")
	if strings.ContainsAny(out, "\n\t") || strings.Contains(out, ": ") {
		t.Errorf("help 输出应为紧凑 JSON: %q", out)
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(out), &items); err != nil {
		t.Fatalf("help 输出不是 JSON 数组: %v (%s)", err, out)
	}
	if len(items) != len(wantToolNames) {
		t.Fatalf("help 条目数 = %d, want %d", len(items), len(wantToolNames))
	}
	seen := map[string]bool{}
	for _, it := range items {
		name, _ := it["name"].(string)
		desc, _ := it["description"].(string)
		if name == "" || desc == "" {
			t.Errorf("help 条目缺 name/description: %v", it)
		}
		if _, ok := it["parameters"]; !ok {
			t.Errorf("help 条目 %q 缺 parameters", name)
		}
		seen[name] = true
	}
	for _, name := range wantToolNames {
		if !seen[name] {
			t.Errorf("help 未列出工具 %q", name)
		}
	}
}

// TestGetRecentMessagesTool 查询结果为紧凑 JSON 数组、按时间正序，
// 字段含 message_id/chat_type/chat_id/sender_name/text/time；支持
// chat_type/chat_id 过滤与 limit。
func TestGetRecentMessagesTool(t *testing.T) {
	s := openTestStore(t)
	tools, _, _ := testToolSet(t, s)

	t1 := time.Unix(1762400000, 0)
	putRecord(t, s, 1, "recv", "group", 888, 10001, "群友A", "第一条", t1.Unix())
	putRecord(t, s, 2, "recv", "private", 20002, 20002, "私聊B", "第二条", t1.Add(time.Minute).Unix())
	putRecord(t, s, 3, "recv", "group", 888, 10003, "群友C", "第三条", t1.Add(2*time.Minute).Unix())

	out := runToolOK(t, tools, "get_recent_messages", `{"limit":2}`)
	if strings.ContainsAny(out, "\n\t") || strings.Contains(out, ": ") {
		t.Errorf("输出应为紧凑 JSON: %q", out)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("输出不是 JSON 数组: %v (%s)", err, out)
	}
	if len(rows) != 2 {
		t.Fatalf("limit=2 应返回 2 条, got %d: %s", len(rows), out)
	}
	// 正序：最新的第 3 条在前? 不——正序 = 旧→新，取最近 2 条即第 2、3 条
	want := []struct {
		id     float64
		chat   string
		chatID float64
		name   string
		text   string
	}{
		{2, "private", 20002, "私聊B", "第二条"},
		{3, "group", 888, "群友C", "第三条"},
	}
	for i, w := range want {
		if rows[i]["message_id"] != w.id || rows[i]["chat_type"] != w.chat ||
			rows[i]["chat_id"] != w.chatID || rows[i]["sender_name"] != w.name ||
			rows[i]["text"] != w.text {
			t.Errorf("rows[%d] = %v, want id=%v chat=%v chat_id=%v name=%q text=%q",
				i, rows[i], w.id, w.chat, w.chatID, w.name, w.text)
		}
	}
	wantTime := t1.Add(time.Minute).Format("01-02 15:04")
	if rows[0]["time"] != wantTime {
		t.Errorf("rows[0].time = %v, want %q", rows[0]["time"], wantTime)
	}

	// chat_type 过滤
	out = runToolOK(t, tools, "get_recent_messages", `{"chat_type":"group","limit":10}`)
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("group 过滤应返回 2 条, got %d: %s", len(rows), out)
	}
	for _, r := range rows {
		if r["chat_type"] != "group" {
			t.Errorf("过滤后仍含非 group 行: %v", r)
		}
	}

	// 超上限 clamp 到 100（3 条全出，不报错）
	out = runToolOK(t, tools, "get_recent_messages", `{"limit":999}`)
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Errorf("limit=999 应 clamp 并返回全部 3 条, got %d", len(rows))
	}
}

// TestSearchMessagesTool 全文搜索走 FTS，空 query 报错。
func TestSearchMessagesTool(t *testing.T) {
	s := openTestStore(t)
	tools, _, _ := testToolSet(t, s)

	t1 := time.Unix(1762400000, 0)
	putRecord(t, s, 1, "recv", "group", 888, 10001, "群友A", "今晚一起打游戏吗", t1.Unix())
	putRecord(t, s, 2, "recv", "group", 888, 10003, "群友C", "不了我要写代码", t1.Add(time.Minute).Unix())

	out := runToolOK(t, tools, "search_messages", `{"query":"游戏","limit":10}`)
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("输出不是 JSON 数组: %v (%s)", err, out)
	}
	if len(rows) != 1 || rows[0]["text"] != "今晚一起打游戏吗" {
		t.Errorf("搜索结果 = %v, want 命中「今晚一起打游戏吗」", rows)
	}

	runToolErr(t, tools, "search_messages", `{"query":""}`)
}

// TestSendMessageTool 经 NapCat 发出并返回 {"message_id":N}；
// 参数缺失/非法报 error。
func TestSendMessageTool(t *testing.T) {
	tools, _, _ := testToolSet(t, nil)
	napcat := startFakeNapCat(t)

	out := runToolOK(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"text":"大家好"}`)
	if out != `{"message_id":1}` {
		t.Errorf("send_message 输出 = %q, want {\"message_id\":1}", out)
	}
	if napcat.count() != 1 || napcat.paths[0] != "/send_group_msg" {
		t.Fatalf("NapCat 应收到 1 次 /send_group_msg, got %v", napcat.paths)
	}
	if got := napcat.sentText(t, 0); got != "大家好" {
		t.Errorf("发送文本 = %q, want 大家好", got)
	}

	runToolOK(t, tools, "send_message", `{"chat_type":"private","chat_id":20002,"text":"在吗"}`)
	if napcat.paths[1] != "/send_private_msg" {
		t.Errorf("私聊应走 /send_private_msg, got %v", napcat.paths[1])
	}

	for _, bad := range []string{
		`{"chat_type":"group","chat_id":0,"text":"hi"}`,
		`{"chat_type":"channel","chat_id":888,"text":"hi"}`,
		`{"chat_type":"group","chat_id":888,"text":""}`,
		`{"chat_type":"group","chat_id":888}`,
		`{}`,
	} {
		runToolErr(t, tools, "send_message", bad)
	}
	if napcat.count() != 2 {
		t.Errorf("非法参数不应发消息, NapCat 收到 %d 次", napcat.count())
	}
}

// TestThinkToolAppendsNote think 把笔记追加到 notes.json；空 note 报错。
func TestThinkToolAppendsNote(t *testing.T) {
	tools, notesPath, _ := testToolSet(t, nil)

	runToolOK(t, tools, "think", `{"note":"他在开玩笑，别当真"}`)
	runToolOK(t, tools, "think", `{"note":"先查群名片"}`)

	notes, err := LoadNotes(notesPath)
	if err != nil {
		t.Fatalf("LoadNotes: %v", err)
	}
	if len(notes) != 2 || notes[0].Text != "他在开玩笑，别当真" || notes[1].Text != "先查群名片" {
		t.Errorf("笔记未按序落盘: %+v", notes)
	}

	runToolErr(t, tools, "think", `{"note":""}`)
	runToolErr(t, tools, "think", `{}`)
}

// TestUpdateMoodToolWritesStateAndSkipsSame mood 变化时写 state.json
// （含 updated 时间戳），mood 与现值相同则不写盘。
func TestUpdateMoodToolWritesStateAndSkipsSame(t *testing.T) {
	tools, _, statePath := testToolSet(t, nil)

	runToolOK(t, tools, "update_mood", `{"mood":"开心"}`)
	data1, err := readState(t, statePath)
	if err != nil {
		t.Fatalf("state.json 应已写入: %v", err)
	}
	if data1.Mood != "开心" || data1.Updated.IsZero() {
		t.Errorf("state 内容 = %+v, want mood=开心 且 updated 非零", data1)
	}

	// 相同 mood：不写盘（mtime 不变）
	info1, err := osStat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	runToolOK(t, tools, "update_mood", `{"mood":"开心"}`)
	info2, err := osStat(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !info2.ModTime().Equal(info1.ModTime()) {
		t.Error("相同 mood 不应重写 state.json")
	}

	// 不同 mood：更新
	runToolOK(t, tools, "update_mood", `{"mood":"困了"}`)
	data3, err := readState(t, statePath)
	if err != nil {
		t.Fatal(err)
	}
	if data3.Mood != "困了" {
		t.Errorf("mood = %q, want 困了", data3.Mood)
	}

	runToolErr(t, tools, "update_mood", `{"mood":""}`)
}

// TestToolErrorShape 工具报错路径：未知工具与非法参数都由调用方包成 {"error"}。
func TestToolErrorShape(t *testing.T) {
	tools, _, _ := testToolSet(t, nil)
	tl := findTool(t, tools, "get_recent_messages")
	out, err := tl.Run(json.RawMessage(`{"limit":"abc"}`))
	if err == nil {
		t.Fatalf("非法参数应报错, got %q", out)
	}
}

// napcatSegments 取假 NapCat 第 i 次请求的 message 段数组（type + data）。
func napcatSegments(t *testing.T, n *napcatRec, i int) []map[string]any {
	t.Helper()
	n.mu.Lock()
	defer n.mu.Unlock()
	raw, _ := n.bodies[i]["message"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, seg := range raw {
		m, ok := seg.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := m["type"].(string)
		d, _ := m["data"].(map[string]any)
		out = append(out, map[string]any{"type": typ, "data": d})
	}
	return out
}

// TestSendMessageSegmentsRich segments 富消息链：假 NapCat 断言收到的
// message 段数组与 data 字段；image 的 file 原样透传（不读本地文件转 base64）。
func TestSendMessageSegmentsRich(t *testing.T) {
	tools, _, _ := testToolSet(t, nil)
	napcat := startFakeNapCat(t)

	// at + text（群聊）
	runToolOK(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"segments":[`+
		`{"type":"at","data":{"qq":"10001"}},{"type":"text","data":{"text":" 你好啊"}}]}`)
	if napcat.count() != 1 || napcat.paths[0] != "/send_group_msg" {
		t.Fatalf("NapCat 应收到 1 次 /send_group_msg, got %v", napcat.paths)
	}
	segs := napcatSegments(t, napcat, 0)
	if len(segs) != 2 {
		t.Fatalf("at+text 应发出 2 段, got %v", segs)
	}
	if segs[0]["type"] != "at" {
		t.Errorf("段0 类型 = %v, want at", segs[0])
	}
	if d, _ := segs[0]["data"].(map[string]any); d["qq"] != "10001" {
		t.Errorf("段0 at.qq = %v, want 10001", d)
	}
	if segs[1]["type"] != "text" {
		t.Errorf("段1 类型 = %v, want text", segs[1])
	}
	if d, _ := segs[1]["data"].(map[string]any); d["text"] != " 你好啊" {
		t.Errorf("段1 text.text = %v, want ' 你好啊'", d)
	}

	// reply + text（私聊）
	runToolOK(t, tools, "send_message", `{"chat_type":"private","chat_id":20002,"segments":[`+
		`{"type":"reply","data":{"id":"54321"}},{"type":"text","data":{"text":"收到"}}]}`)
	if napcat.paths[1] != "/send_private_msg" {
		t.Errorf("私聊应走 /send_private_msg, got %v", napcat.paths[1])
	}
	segs = napcatSegments(t, napcat, 1)
	if len(segs) != 2 || segs[0]["type"] != "reply" || segs[1]["type"] != "text" {
		t.Fatalf("reply+text 应发出 [reply text], got %v", segs)
	}
	if d, _ := segs[0]["data"].(map[string]any); d["id"] != "54321" {
		t.Errorf("段0 reply.id = %v, want 54321", d)
	}
	if d, _ := segs[1]["data"].(map[string]any); d["text"] != "收到" {
		t.Errorf("段1 text.text = %v, want 收到", d)
	}

	// image：url + 可选 summary
	runToolOK(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"segments":[`+
		`{"type":"image","data":{"file":"https://example.com/a.jpg","summary":"看看"}}]}`)
	segs = napcatSegments(t, napcat, 2)
	if len(segs) != 1 || segs[0]["type"] != "image" {
		t.Fatalf("image 应发出 1 段 image, got %v", segs)
	}
	if d, _ := segs[0]["data"].(map[string]any); d["file"] != "https://example.com/a.jpg" || d["summary"] != "看看" {
		t.Errorf("image 段 data = %v, want file=url summary=看看", d)
	}

	// image：base64:// 与本地路径形态都原样透传
	runToolOK(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"segments":[`+
		`{"type":"image","data":{"file":"base64://AAAA"}}]}`)
	runToolOK(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"segments":[`+
		`{"type":"image","data":{"file":"E:/pics/a.png"}}]}`)
	segs = napcatSegments(t, napcat, 3)
	if d, _ := segs[0]["data"].(map[string]any); d["file"] != "base64://AAAA" {
		t.Errorf("base64:// 应原样透传, got %v", d)
	}
	segs = napcatSegments(t, napcat, 4)
	if d, _ := segs[0]["data"].(map[string]any); d["file"] != "E:/pics/a.png" {
		t.Errorf("本地路径应原样透传（AI 不该给本地路径, 交给 NapCat 报错）, got %v", d)
	}

	// face / dice / rps / json 全白名单
	runToolOK(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"segments":[`+
		`{"type":"face","data":{"id":"14"}},{"type":"dice","data":{"result":"6"}},`+
		`{"type":"rps","data":{"result":"1"}},{"type":"json","data":{"data":{"foo":"bar"}}}]}`)
	segs = napcatSegments(t, napcat, 5)
	wantTypes := []any{"face", "dice", "rps", "json"}
	for i, want := range wantTypes {
		if segs[i]["type"] != want {
			t.Errorf("段%d 类型 = %v, want %v", i, segs[i]["type"], want)
		}
	}
	if d, _ := segs[0]["data"].(map[string]any); d["id"] != "14" {
		t.Errorf("face.id = %v, want 14", d)
	}
	if d, _ := segs[1]["data"].(map[string]any); d["result"] != "6" {
		t.Errorf("dice.result = %v, want 6", d)
	}
	if d, _ := segs[2]["data"].(map[string]any); d["result"] != "1" {
		t.Errorf("rps.result = %v, want 1", d)
	}
	if d, _ := segs[3]["data"].(map[string]any); fmt.Sprint(d["data"]) != "map[foo:bar]" {
		t.Errorf("json.data = %v, want map[foo:bar]", d)
	}
}

// TestSendMessageSegmentsParamErrors 参数校验：text 与 segments 二选一、
// 白名单外类型、缺字段/字段类型错、text 超长都报 error 且不发消息；
// 恰好 8000 字符可以发。
func TestSendMessageSegmentsParamErrors(t *testing.T) {
	tools, _, _ := testToolSet(t, nil)
	napcat := startFakeNapCat(t)

	for _, bad := range []struct{ name, args string }{
		{"text 与 segments 同时给出", `{"chat_type":"group","chat_id":888,"text":"hi","segments":[{"type":"text","data":{"text":"x"}}]}`},
		{"text 与 segments 都缺", `{"chat_type":"group","chat_id":888}`},
		{"segments 为空数组", `{"chat_type":"group","chat_id":888,"segments":[]}`},
		{"白名单外类型", `{"chat_type":"group","chat_id":888,"segments":[{"type":"poke","data":{"id":"1"}}]}`},
		{"at 缺 qq", `{"chat_type":"group","chat_id":888,"segments":[{"type":"at","data":{}}]}`},
		{"at 的 qq 非字符串", `{"chat_type":"group","chat_id":888,"segments":[{"type":"at","data":{"qq":123}}]}`},
		{"text 段缺 text", `{"chat_type":"group","chat_id":888,"segments":[{"type":"text","data":{}}]}`},
		{"text 段为空串", `{"chat_type":"group","chat_id":888,"segments":[{"type":"text","data":{"text":""}}]}`},
		{"image 缺 file", `{"chat_type":"group","chat_id":888,"segments":[{"type":"image","data":{"summary":"s"}}]}`},
		{"image summary 非字符串", `{"chat_type":"group","chat_id":888,"segments":[{"type":"image","data":{"file":"https://e.com/a.jpg","summary":1}}]}`},
		{"json 缺 data", `{"chat_type":"group","chat_id":888,"segments":[{"type":"json","data":{}}]}`},
		{"text 参数超长", `{"chat_type":"group","chat_id":888,"text":"` + strings.Repeat("长", 8001) + `"}`},
		{"segments 内 text 累计超长", `{"chat_type":"group","chat_id":888,"segments":[` +
			`{"type":"text","data":{"text":"` + strings.Repeat("长", 4000) + `"}},` +
			`{"type":"text","data":{"text":"` + strings.Repeat("长", 4001) + `"}}]}`},
	} {
		err := runToolErr(t, tools, "send_message", bad.args)
		t.Logf("%s -> %v", bad.name, err)
	}
	// 关键错误信息可读（回填思维链后模型能自纠）
	err := runToolErr(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"segments":[{"type":"poke","data":{"id":"1"}}]}`)
	if !strings.Contains(err.Error(), "poke") || !strings.Contains(err.Error(), "不支持") {
		t.Errorf("白名单外类型报错应含类型名与提示: %v", err)
	}
	if err := runToolErr(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"text":"hi","segments":[{"type":"text","data":{"text":"x"}}]}`); !strings.Contains(err.Error(), "二选一") {
		t.Errorf("同时给出应报二选一: %v", err)
	}
	if err := runToolErr(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"text":"`+strings.Repeat("长", 8001)+`"}`); !strings.Contains(err.Error(), "8000") {
		t.Errorf("超长应提示上限 8000: %v", err)
	}

	// 恰好 8000 字符可以发
	runToolOK(t, tools, "send_message", `{"chat_type":"group","chat_id":888,"text":"`+strings.Repeat("长", 8000)+`"}`)
	if napcat.count() != 1 {
		t.Errorf("只有边界合法的那次应发出消息, NapCat 收到 %d 次", napcat.count())
	}
}

// TestToolDefsSchemaNoNulls 无参工具（finish/help 等）的参数 schema 不得包含
// null 字段——部分 OpenAI 兼容提供商（实测 400：null is not of type "array"）
// 要求 required 必须是数组或省略，properties 不能是 null。
func TestToolDefsSchemaNoNulls(t *testing.T) {
	tools, _, _ := testToolSet(t, nil)
	data, err := json.Marshal(toolDefs(tools))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "null") {
		t.Errorf("工具 schema 序列化含 null，会被提供商拒绝:\n%s", data)
	}
}

// ================= P3-5：定时器工具 =================

// TestScheduleToolSchedulesAndReturnsID schedule 工具经 TimerStore 落记录：
// 返回 {"id","fire_at"}，chat 上下文取自构造期捕获的 turn（group/888/999）；
// delay_seconds 越界与空 note 报错。
func TestScheduleToolSchedulesAndReturnsID(t *testing.T) {
	tools, timer := testToolSetWithTimers(t)

	out := runToolOK(t, tools, "schedule", `{"delay_seconds":60,"note":"10分钟后看看他回了没"}`)
	if strings.ContainsAny(out, "\n\t") {
		t.Errorf("输出应为紧凑 JSON: %q", out)
	}
	var res struct {
		ID     string `json:"id"`
		FireAt string `json:"fire_at"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("schedule 输出不是 JSON 对象: %v (%s)", err, out)
	}
	if res.ID == "" || res.FireAt == "" {
		t.Fatalf("schedule 输出应含 id 与 fire_at: %s", out)
	}
	if timer.Active() != 1 {
		t.Fatalf("调度后 Active = %d, want 1", timer.Active())
	}

	// chat 上下文来自 turn：group/888/self=999
	timer.mu.Lock()
	recs := make([]ScheduledTimer, 0, len(timer.records))
	for _, r := range timer.records {
		recs = append(recs, r)
	}
	timer.mu.Unlock()
	if len(recs) != 1 || recs[0].ID != res.ID || recs[0].Note != "10分钟后看看他回了没" ||
		recs[0].ChatType != "group" || recs[0].ChatID != 888 || recs[0].SelfID != 999 {
		t.Errorf("落库记录应捕获 turn 会话上下文: %+v", recs)
	}
	timer.Cancel(res.ID)

	// 参数校验
	for _, bad := range []string{
		`{"delay_seconds":0,"note":"x"}`,
		`{"delay_seconds":604801,"note":"x"}`,
		`{"delay_seconds":60,"note":""}`,
		`{"delay_seconds":60}`,
		`{"note":"x"}`,
	} {
		runToolErr(t, tools, "schedule", bad)
	}
	if timer.Active() != 0 {
		t.Errorf("非法参数不应留下计时, Active = %d", timer.Active())
	}
	// 边界 604800（7 天）合法
	out = runToolOK(t, tools, "schedule", `{"delay_seconds":604800,"note":"边界"}`)
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.ID == "" {
		t.Fatalf("7 天边界应成功: %v (%s)", err, out)
	}
	timer.Cancel(res.ID)
}

// TestCancelTimerTool cancel_timer 按 schedule 返回的 ID 取消：
// 成功 {"cancelled":true}、Active 归零；未知 ID 报 error。
func TestCancelTimerTool(t *testing.T) {
	tools, timer := testToolSetWithTimers(t)

	out := runToolOK(t, tools, "schedule", `{"delay_seconds":60,"note":"要取消的"}`)
	var res struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil || res.ID == "" {
		t.Fatalf("schedule 输出: %v (%s)", err, out)
	}

	got := runToolOK(t, tools, "cancel_timer", `{"timer_id":"`+res.ID+`"}`)
	if got != `{"cancelled":true}` {
		t.Errorf("cancel 输出 = %q, want {\"cancelled\":true}", got)
	}
	if timer.Active() != 0 {
		t.Errorf("取消后 Active = %d, want 0", timer.Active())
	}

	err := runToolErr(t, tools, "cancel_timer", `{"timer_id":"`+res.ID+`"}`)
	if !strings.Contains(err.Error(), res.ID) {
		t.Errorf("重复取消应报未找到: %v", err)
	}
	runToolErr(t, tools, "cancel_timer", `{"timer_id":"nonexistent"}`)
	runToolErr(t, tools, "cancel_timer", `{"timer_id":""}`)
}

// TestTimerToolsUnavailableWithoutStore timer 为 nil（未接线）时
// schedule / cancel_timer 均返回 error。
func TestTimerToolsUnavailableWithoutStore(t *testing.T) {
	tools, _, _ := testToolSet(t, nil) // turn=nil, timer=nil
	for _, name := range []string{"schedule", "cancel_timer"} {
		err := runToolErr(t, tools, name, `{"delay_seconds":60,"note":"x","timer_id":"x"}`)
		if !strings.Contains(err.Error(), "定时器不可用") {
			t.Errorf("%s 无计时器应报「定时器不可用」, got %v", name, err)
		}
	}
}

// ================= P4 QQ 空间工具 =================

// TestPostQzoneTool post_qzone 走 /send_qzone_msg：content 必填、images 可选
// （文档：NapCat 自动上传，无需额外上传步骤）；输出 tid/time。
func TestPostQzoneTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any {
		return map[string]any{"tid": "abc123", "time": 1700000000}
	})
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "post_qzone", `{"text":"今天天气不错"}`)
	if nap.count() != 1 || nap.path(0) != "/send_qzone_msg" {
		t.Fatalf("NapCat 应收到 1 次 /send_qzone_msg, got %v", nap.paths)
	}
	b := nap.body(0)
	if str(t, b, "content") != "今天天气不错" {
		t.Errorf("content 透传错误: %v", b)
	}
	if _, has := b["images"]; has {
		t.Errorf("无图时不应带 images 字段: %v", b)
	}
	if _, has := b["ugc_right"]; has {
		t.Errorf("不应默认带 ugc_right（留给 napcat_api 设置权限）: %v", b)
	}
	if got := objOf(t, out)["tid"]; got != "abc123" {
		t.Errorf("输出应含 tid, got %s", out)
	}

	// 带图：images 原样透传给 NapCat 自动上传
	runToolOK(t, tools, "post_qzone", `{"text":"带图","images":["https://a.example/x.jpg"]}`)
	imgs, ok := nap.body(1)["images"].([]any)
	if !ok || len(imgs) != 1 || imgs[0] != "https://a.example/x.jpg" {
		t.Errorf("images 透传错误: %v", nap.body(1))
	}

	// text 非空校验：报错且不触网
	runToolErr(t, tools, "post_qzone", `{"text":""}`)
	runToolErr(t, tools, "post_qzone", `{}`)
	if nap.count() != 2 {
		t.Errorf("非法参数不应发请求, got %d", nap.count())
	}
}

// TestBrowseQzoneFeedsTool browse_qzone_feeds 走 /get_qzone_feeds（SnowLuma 形态，
// 只传 page_num）：输出精简行（uin/author/time/summary/image_count/tid），
// 每条摘要限 100 字符、最多 10 条，has_more 透出。
func TestBrowseQzoneFeedsTool(t *testing.T) {
	feeds := make([]any, 0, 12)
	for i := 0; i < 12; i++ {
		feeds = append(feeds, map[string]any{
			"uin": 10000 + i, "nickname": fmt.Sprintf("好友%d", i),
			"time": 1700000000 + i*60, "tid": fmt.Sprintf("t%d", i),
			"content": fmt.Sprintf("动态内容%d", i),
			"images":  []any{"a.jpg", "b.jpg"}, "like_count": 1, "comment_count": 2,
		})
	}
	feeds[0].(map[string]any)["content"] = strings.Repeat("长", 150)
	nap := startP3NapCat(t, func(string, map[string]any) any {
		return map[string]any{"feeds": feeds, "has_more": true, "next_cursor": nil}
	})
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "browse_qzone_feeds", "")
	if nap.count() != 1 || nap.path(0) != "/get_qzone_feeds" {
		t.Fatalf("NapCat 应收到 1 次 /get_qzone_feeds, got %v", nap.paths)
	}
	if num(t, nap.body(0), "page_num") != 1 {
		t.Errorf("page_num 默认应为 1: %v", nap.body(0))
	}
	if _, has := nap.body(0)["format"]; has {
		t.Errorf("SnowLuma 形态不应传 format: %v", nap.body(0))
	}
	obj := objOf(t, out)
	rows, ok := obj["feeds"].([]any)
	if !ok || len(rows) != 10 {
		t.Fatalf("最多返回 10 条, got %d (%s)", len(rows), out)
	}
	if obj["has_more"] != true {
		t.Errorf("has_more 应透出: %s", out)
	}
	first := rows[0].(map[string]any)
	if n := len([]rune(first["summary"].(string))); n > 100 {
		t.Errorf("summary 未按 100 字符截断: %d", n)
	}
	if first["tid"] != "t0" || first["author"] != "好友0" || first["uin"] != float64(10000) {
		t.Errorf("首条字段错误: %v", first)
	}
	if first["image_count"] != float64(2) {
		t.Errorf("image_count 应为图片数, got %v", first["image_count"])
	}
	if first["time"] != time.Unix(1700000000, 0).Format(timeLayout) {
		t.Errorf("time 应格式化, got %v", first["time"])
	}

	// 翻页：page_num 透传
	runToolOK(t, tools, "browse_qzone_feeds", `{"page_num":2}`)
	if num(t, nap.body(1), "page_num") != 2 {
		t.Errorf("page_num 透传错误: %v", nap.body(1))
	}
	// page_num 非法值回退默认 1
	runToolOK(t, tools, "browse_qzone_feeds", `{"page_num":-3}`)
	if num(t, nap.body(2), "page_num") != 1 {
		t.Errorf("page_num 非法应回退 1: %v", nap.body(2))
	}
}

// TestReadQzonePostTool read_qzone_post 走 /get_qzone_msg_detail（SnowLuma 形态：
// tid+target_uin）：正文限 200 字、评论限 10 条、单条限 100 字，target_uin
// 缺省不带（默认自己的空间）。
func TestReadQzonePostTool(t *testing.T) {
	detail := map[string]any{
		"tid": "t1", "content": strings.Repeat("帖", 250), "comment_num": 2,
		"comments": []any{
			map[string]any{"uin": 10002, "name": "甲", "time": 1700000100, "content": "沙发",
				"comment_id": "c1", "replies": []any{
					map[string]any{"uin": 10003, "name": "乙", "time": 1700000200,
						"content": "板凳", "comment_id": "c2",
						"parent_comment_id": "c1", "parent_comment_uin": 10002},
				}},
			map[string]any{"uin": 10003, "name": "乙", "time": 1700000300,
				"content": "赞", "comment_id": "c3", "replies": []any{}},
		},
	}
	nap := startP3NapCat(t, func(path string, body map[string]any) any {
		if tid, _ := body["tid"].(string); tid == "many" {
			many := make([]any, 0, 12)
			for i := 0; i < 12; i++ {
				many = append(many, map[string]any{"uin": i, "name": "谁",
					"time": 1700000000, "content": "水", "comment_id": fmt.Sprintf("c%d", i), "replies": []any{}})
			}
			return map[string]any{"tid": "many", "content": "正文", "comment_num": 12, "comments": many}
		}
		return detail
	})
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "read_qzone_post", `{"tid":"t1"}`)
	if nap.count() != 1 || nap.path(0) != "/get_qzone_msg_detail" {
		t.Fatalf("NapCat 应收到 1 次 /get_qzone_msg_detail, got %v", nap.paths)
	}
	b := nap.body(0)
	if str(t, b, "tid") != "t1" {
		t.Errorf("tid 透传错误: %v", b)
	}
	if _, has := b["target_uin"]; has {
		t.Errorf("target_uin 缺省时不应出现（默认自己的空间）: %v", b)
	}
	if _, has := b["feed_ref"]; has {
		t.Errorf("SnowLuma 形态不应带 feed_ref: %v", b)
	}
	obj := objOf(t, out)
	if n := len([]rune(obj["content"].(string))); n > 200 {
		t.Errorf("正文未按 200 字截断: %d", n)
	}
	comments, ok := obj["comments"].([]any)
	if !ok || len(comments) != 2 {
		t.Fatalf("评论应原样精简返回 2 条, got %d (%s)", len(comments), out)
	}
	c0 := comments[0].(map[string]any)
	if c0["name"] != "甲" || c0["content"] != "沙发" {
		t.Errorf("评论字段错误: %v", c0)
	}
	replies, ok := c0["replies"].([]any)
	if !ok || len(replies) != 1 || replies[0].(map[string]any)["name"] != "乙" {
		t.Errorf("楼中楼回复错误: %v", c0["replies"])
	}

	// 指定作者空间：target_uin 透传
	runToolOK(t, tools, "read_qzone_post", `{"tid":"t1","target_uin":10002}`)
	if num(t, nap.body(1), "target_uin") != 10002 {
		t.Errorf("target_uin 透传错误: %v", nap.body(1))
	}

	// 评论多于 10 条时截断到 10
	out = runToolOK(t, tools, "read_qzone_post", `{"tid":"many"}`)
	comments = objOf(t, out)["comments"].([]any)
	if len(comments) != 10 {
		t.Errorf("评论应截断到 10 条, got %d", len(comments))
	}

	// tid 必填
	runToolErr(t, tools, "read_qzone_post", `{}`)
}

// TestQzoneLikeTool qzone_like 合并点赞/取消：enable 默认 true 走 /like_qzone，
// false 走 /unlike_qzone；SnowLuma 形态传 tid/target_uin（缺省省略）。
func TestQzoneLikeTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools, _, _ := testToolSet(t, nil)

	runToolOK(t, tools, "qzone_like", `{"tid":"t1","target_uin":10002}`)
	if nap.path(0) != "/like_qzone" {
		t.Errorf("默认应走 /like_qzone, got %s", nap.path(0))
	}
	b := nap.body(0)
	if str(t, b, "tid") != "t1" || num(t, b, "target_uin") != 10002 {
		t.Errorf("点赞参数错误: %v", b)
	}

	if out := runToolOK(t, tools, "qzone_like", `{"tid":"t1","enable":false}`); out != "{}" {
		t.Errorf("取消点赞输出 = %q, want {}", out)
	}
	if nap.path(1) != "/unlike_qzone" {
		t.Errorf("enable=false 应走 /unlike_qzone, got %s", nap.path(1))
	}
	if _, has := nap.body(1)["target_uin"]; has {
		t.Errorf("target_uin 缺省不应出现: %v", nap.body(1))
	}

	runToolErr(t, tools, "qzone_like", `{"target_uin":10002}`) // tid 必填
	if nap.count() != 2 {
		t.Errorf("非法参数不应发请求, got %d", nap.count())
	}
}

// TestCommentQzoneTool comment_qzone 走 /comment_qzone（SnowLuma 一级评论：
// tid+content+target_uin 可选），不暴露楼中楼参数；返回 comment_id。
func TestCommentQzoneTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any {
		return map[string]any{"comment_id": "c9"}
	})
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "comment_qzone", `{"tid":"t1","content":"拍得真好"}`)
	if nap.count() != 1 || nap.path(0) != "/comment_qzone" {
		t.Fatalf("NapCat 应收到 1 次 /comment_qzone, got %v", nap.paths)
	}
	b := nap.body(0)
	if str(t, b, "tid") != "t1" || str(t, b, "content") != "拍得真好" {
		t.Errorf("评论参数错误: %v", b)
	}
	if _, has := b["target_uin"]; has {
		t.Errorf("target_uin 缺省不应出现: %v", b)
	}
	if _, has := b["comment_id"]; has {
		t.Errorf("一级评论不应带楼中楼参数: %v", b)
	}
	if objOf(t, out)["comment_id"] != "c9" {
		t.Errorf("输出应含 comment_id: %s", out)
	}

	// 指定作者空间
	runToolOK(t, tools, "comment_qzone", `{"tid":"t1","content":"好","target_uin":10002}`)
	if num(t, nap.body(1), "target_uin") != 10002 {
		t.Errorf("target_uin 透传错误: %v", nap.body(1))
	}

	runToolErr(t, tools, "comment_qzone", `{"tid":"t1","content":""}`)
	runToolErr(t, tools, "comment_qzone", `{"tid":"t1"}`)
	if nap.count() != 2 {
		t.Errorf("非法参数不应发请求, got %d", nap.count())
	}
}

// TestQzoneProfileTool qzone_profile 走 /get_qzone_profile 且不带 target_uin
// （读取当前账号），输出 name/description。
func TestQzoneProfileTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any {
		return map[string]any{"name": "我的空间", "description": "记录生活"}
	})
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "qzone_profile", "")
	if nap.count() != 1 || nap.path(0) != "/get_qzone_profile" {
		t.Fatalf("NapCat 应收到 1 次 /get_qzone_profile, got %v", nap.paths)
	}
	if len(nap.body(0)) != 0 {
		t.Errorf("当前账号不应带 target_uin: %v", nap.body(0))
	}
	obj := objOf(t, out)
	if obj["name"] != "我的空间" || obj["description"] != "记录生活" {
		t.Errorf("输出错误: %s", out)
	}
}

// TestDeleteQzoneTool delete_qzone 是危险工具：非管理员报权限错误且不触网，
// 管理员放行走 /delete_qzone_msg；napcat_api 逃生舱同样把 delete_qzone_msg
// 列入危险集合。
func TestDeleteQzoneTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools, _, _ := testToolSet(t, nil) // 非管理员

	err := runToolErr(t, tools, "delete_qzone", `{"tid":"t1"}`)
	if !strings.Contains(err.Error(), "管理员权限") {
		t.Errorf("非管理员应报权限错误: %v", err)
	}
	if nap.count() != 0 {
		t.Errorf("非管理员不应发请求, got %d", nap.count())
	}

	// 逃生舱：delete_qzone_msg 同为危险动作
	err = runToolErr(t, tools, "napcat_api", `{"action":"delete_qzone_msg","params":{"tid":"t1"}}`)
	if !strings.Contains(err.Error(), "管理员权限") {
		t.Errorf("逃生舱危险动作应报权限错误: %v", err)
	}
	if nap.count() != 0 {
		t.Errorf("逃生舱危险动作不应发请求, got %d", nap.count())
	}

	admin := testToolSetAdmin(t)
	if out := runToolOK(t, admin, "delete_qzone", `{"tid":"t1"}`); out != "{}" {
		t.Errorf("管理员删除输出 = %q, want {}", out)
	}
	if nap.count() != 1 || nap.path(0) != "/delete_qzone_msg" {
		t.Fatalf("管理员应发 1 次 /delete_qzone_msg, got %v", nap.paths)
	}
	if str(t, nap.body(0), "tid") != "t1" {
		t.Errorf("tid 透传错误: %v", nap.body(0))
	}

	runToolErr(t, admin, "delete_qzone", `{"tid":""}`)
	runToolErr(t, admin, "delete_qzone", `{}`)
	if nap.count() != 1 {
		t.Errorf("tid 空不应发请求, got %d", nap.count())
	}
}

// ================= view_message_images：按需查看历史消息图片 =================

// putImageRecord 写入一条带 image 段（url+file）的消息记录。
func putImageRecord(t *testing.T, s *storage.Store, id int64, url, file string) {
	t.Helper()
	putRecordFull(t, s, &storage.MessageRecord{
		MessageID: id, Direction: "recv", ChatType: "group", ChatID: 888, UserID: 10001,
		SenderName: "小明",
		RawMessage: "[CQ:image,file=" + file + ",subType=0,url=" + url + "]",
		Segments:   []byte(`[{"type":"image","data":{"file":"` + file + `","url":"` + url + `"}}]`),
		Time:       1762400000,
	})
}

// deadImageHost 起一个全部请求 404 的假图床。
func deadImageHost(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	return srv
}

// TestViewMessageImagesDirectSuccess 直连成功：消息段 url 在假图床上可下载 →
// 工具返回 JSON（total/attached/note），turn.pendingImages 得到 data URL；
// 全程不调 NapCat（无 get_image 换链）。
func TestViewMessageImagesDirectSuccess(t *testing.T) {
	s := openTestStore(t)
	imgSrv, _ := servePNG(t, "image/png")
	putImageRecord(t, s, 7, imgSrv.URL+"/a.png", "ABC.image")
	napcat := startP3NapCat(t, func(path string, body map[string]any) any {
		t.Errorf("直连成功不应调用 NapCat, got %s", path)
		return nil
	})
	tools, turn := testToolSetForViewImages(t, s, newImageStore(t.TempDir(), s))

	out := runToolOK(t, tools, "view_message_images", `{"message_id":7}`)
	obj := objOf(t, out)
	if obj["total"] != float64(1) || obj["attached"] != float64(1) {
		t.Errorf("输出 total/attached = %v/%v, want 1/1: %s", obj["total"], obj["attached"], out)
	}
	note, _ := obj["note"].(string)
	if !strings.Contains(note, "已附在下一条消息") {
		t.Errorf("note 应注明图片附在下一条消息: %q", note)
	}
	if napcat.count() != 0 {
		t.Errorf("直连成功不应调 NapCat, got %d 次", napcat.count())
	}
	if len(turn.pendingImages) != 1 {
		t.Fatalf("pendingImages 应有 1 张, got %d", len(turn.pendingImages))
	}
	if !strings.HasPrefix(turn.pendingImages[0], "data:image/png;base64,") {
		t.Errorf("pendingImages[0] 应为 png data URL: %q", turn.pendingImages[0][:40])
	}
}

// TestViewMessageImagesGetImageChainSwitch 直连失败（原 url 死链）→ get_image
// 换链成功：NapCat /get_image 收到 file 参数，返回新 url，新 url 下载成功。
func TestViewMessageImagesGetImageChainSwitch(t *testing.T) {
	s := openTestStore(t)
	// 假图床：/old/* 死链，/new/* 提供新链（rkey 换新场景）
	mux := http.NewServeMux()
	mux.HandleFunc("/old/", http.NotFoundHandler().ServeHTTP)
	mux.HandleFunc("/new/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	})
	imgSrv := httptest.NewServer(mux)
	t.Cleanup(imgSrv.Close)
	putImageRecord(t, s, 8, imgSrv.URL+"/old/a.png", "ABC.image")

	napcat := startP3NapCat(t, func(path string, body map[string]any) any {
		if path == "/get_image" {
			if str(t, body, "file") != "ABC.image" {
				t.Errorf("get_image 请求 file = %v, want ABC.image", body["file"])
			}
			// NapCat 在 WSL：file 是本地路径读不了，url 是换新后的下载链接
			return map[string]any{"file": "/mnt/napcat/ABC.png", "url": imgSrv.URL + "/new/a.png"}
		}
		return nil
	})
	tools, turn := testToolSetForViewImages(t, s, newImageStore(t.TempDir(), s))

	out := runToolOK(t, tools, "view_message_images", `{"message_id":8}`)
	obj := objOf(t, out)
	if obj["total"] != float64(1) || obj["attached"] != float64(1) {
		t.Errorf("输出 total/attached = %v/%v, want 1/1: %s", obj["total"], obj["attached"], out)
	}
	if napcat.count() != 1 || napcat.path(0) != "/get_image" {
		t.Fatalf("应恰好调 1 次 /get_image, got %v", napcat.paths)
	}
	if len(turn.pendingImages) != 1 || !strings.HasPrefix(turn.pendingImages[0], "data:image/png;base64,") {
		t.Fatalf("pendingImages 应有 1 张换链成功的 data URL, got %v", turn.pendingImages)
	}
}

// TestViewMessageImagesDoubleFailureSkips 双失败跳过：原 url 死链且 get_image
// 只返回本地路径（url=file 同为 WSL 路径，Q-Mi 读不了）→ 该图跳过并在输出里
// 注明，pendingImages 不含该图；跳过不是报错（其他图可能成功）。
func TestViewMessageImagesDoubleFailureSkips(t *testing.T) {
	s := openTestStore(t)
	host := deadImageHost(t)
	putImageRecord(t, s, 9, host.URL+"/dead.png", "ABC.image")
	napcat := startP3NapCat(t, func(path string, body map[string]any) any {
		if path == "/get_image" {
			// 文档：url 缺省回退为本地 downloadPath，读不了
			return map[string]any{"file": "/mnt/napcat/ABC.png", "url": "/mnt/napcat/ABC.png"}
		}
		return nil
	})
	tools, turn := testToolSetForViewImages(t, s, newImageStore(t.TempDir(), s))

	out := runToolOK(t, tools, "view_message_images", `{"message_id":9}`)
	obj := objOf(t, out)
	if obj["total"] != float64(1) || obj["attached"] != float64(0) {
		t.Errorf("输出 total/attached = %v/%v, want 1/0: %s", obj["total"], obj["attached"], out)
	}
	skipped, ok := obj["skipped"].([]any)
	if !ok || len(skipped) != 1 {
		t.Fatalf("双失败应输出 1 条 skipped 注明: %s", out)
	}
	if napcat.count() != 1 {
		t.Errorf("应调 1 次 get_image 换链, got %d", napcat.count())
	}
	if len(turn.pendingImages) != 0 {
		t.Errorf("双失败不应有 pendingImages, got %v", turn.pendingImages)
	}
}

// TestViewMessageImagesNoImageErrors 消息里没有 image 段 → 报错（不是空结果）。
func TestViewMessageImagesNoImageErrors(t *testing.T) {
	s := openTestStore(t)
	putRecordFull(t, s, &storage.MessageRecord{
		MessageID: 10, Direction: "recv", ChatType: "group", ChatID: 888, UserID: 10001,
		SenderName: "小明", RawMessage: "纯文本消息", Time: 1762400000,
	})
	startP3NapCat(t, func(string, map[string]any) any { return nil })
	tools, _ := testToolSetForViewImages(t, s, newImageStore(t.TempDir(), s))

	err := runToolErr(t, tools, "view_message_images", `{"message_id":10}`)
	if !strings.Contains(err.Error(), "没有图片") {
		t.Errorf("无图应报「没有图片」: %v", err)
	}
	// message_id 缺失 / 未知消息同样报错
	runToolErr(t, tools, "view_message_images", `{}`)
	runToolErr(t, tools, "view_message_images", `{"message_id":404}`)
}

// ================= 查询工具自动带图 =================

// TestQueryMessageRowsAutoAttachesImages 查询结果行带 images 张数标记，最新
// 消息的图片自动解析并附进 turn.pendingImages（封顶 maxAutoAttachImages 张），
// 附上的行有 images_attached 标记。
func TestQueryMessageRowsAutoAttachesImages(t *testing.T) {
	s := openTestStore(t)
	imgSrv, _ := servePNG(t, "image/png")
	ims := newImageStore(t.TempDir(), s)

	base := int64(1762400000)
	// 3 条消息 × 每条 2 张图（同 URL，内容寻址去重）= 6 张，附带应封顶 5 张
	for i := int64(1); i <= 3; i++ {
		u := imgSrv.URL + "/a.png"
		putRecordFull(t, s, &storage.MessageRecord{
			MessageID: i, Direction: "recv", ChatType: "group", ChatID: 888, UserID: 10001,
			SenderName: "小明",
			RawMessage: "[CQ:image][CQ:image]",
			Segments:   []byte(`[{"type":"image","data":{"url":"` + u + `"}},{"type":"image","data":{"url":"` + u + `"}}]`),
			Time:       float64(base + i),
		})
	}
	turn := &Turn{ChatType: "group", ChatID: 888, SelfID: 999}
	out, err := queryMessageRows(s, ims, turn, storage.MessageFilter{Limit: 50})
	if err != nil {
		t.Fatalf("queryMessageRows: %v", err)
	}

	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("输出应为 JSON 数组: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("应返回 3 行, got %d", len(rows))
	}
	for _, row := range rows {
		if row["images"] != float64(2) {
			t.Errorf("行 images = %v, want 2", row["images"])
		}
	}
	if len(turn.pendingImages) != maxAutoAttachImages {
		t.Fatalf("pendingImages 应封顶 %d 张, got %d", maxAutoAttachImages, len(turn.pendingImages))
	}
	// 最新一条（message_id=3）整行 2 张都附上；次新行 2 张；最旧行只附上第
	// 5 张（封顶截断，6 张里只带 5）——三行都有 images_attached 标记
	if rows[2]["message_id"] != float64(3) || rows[2]["images_attached"] != true {
		t.Errorf("最新行应整行附图: %v", rows[2])
	}
	if rows[1]["message_id"] != float64(2) || rows[1]["images_attached"] != true {
		t.Errorf("次新行应附第 5 张: %v", rows[1])
	}
	if rows[0]["message_id"] != float64(1) || rows[0]["images_attached"] != true {
		t.Errorf("最旧行应附第 5 张（封顶截断）: %v", rows[0])
	}
	for _, du := range turn.pendingImages {
		if !strings.HasPrefix(du, "data:image/png;base64,") {
			t.Errorf("pendingImages 应为 png data URL: %q", du[:40])
		}
	}
}
