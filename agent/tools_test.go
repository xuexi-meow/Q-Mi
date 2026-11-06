package agent

import (
	"encoding/json"
	"fmt"
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
func testToolSet(t *testing.T, store *storage.Store) ([]Tool, string, string) {
	t.Helper()
	dir := t.TempDir()
	notesPath := filepath.Join(dir, "notes.json")
	statePath := filepath.Join(dir, "state.json")
	return newToolSet(store, notesPath, statePath, false), notesPath, statePath
}

// testToolSetAdmin 管理员视角的工具集（危险工具放行）。
func testToolSetAdmin(t *testing.T) []Tool {
	t.Helper()
	dir := t.TempDir()
	return newToolSet(nil, filepath.Join(dir, "notes.json"), filepath.Join(dir, "state.json"), true)
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
	"get_recent_messages", "search_messages",
	"send_message", "think", "update_mood", "finish",
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
