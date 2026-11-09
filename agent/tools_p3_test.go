package agent

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"Q-Mi/utils/config"
	"Q-Mi/utils/request"
)

// P3-2 QQ 能力工具与逃生舱的测试：
// httptest 假 NapCat（按 path 定制 data），断言 path/参数/返回提取；
// 逃生舱与文档资源；管理员门槛已在 tools_test.go 覆盖工具级门槛，
// 此处覆盖 napcat_api 的危险动作门槛。

// p3napcat 记录 path 与请求体，按注册的 respond 函数生成 data 字段。
type p3napcat struct {
	mu     sync.Mutex
	paths  []string
	bodies []map[string]any
}

// startP3NapCat 启动假 NapCat 并把全局 request client 指向它。
// respond 按 (path, body) 返回 data 的值（非整个信封）。
func startP3NapCat(t *testing.T, respond func(path string, body map[string]any) any) *p3napcat {
	t.Helper()
	rec := &p3napcat{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		rec.mu.Lock()
		rec.paths = append(rec.paths, r.URL.Path)
		rec.bodies = append(rec.bodies, body)
		fn := respond
		rec.mu.Unlock()
		data := fn(r.URL.Path, body)
		payload, _ := json.Marshal(map[string]any{"status": "ok", "retcode": 0, "data": data})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	t.Cleanup(srv.Close)
	if err := request.Init(&config.Napcat{HTTP: config.NapcatConn{
		Enable: true, Host: srv.Listener.Addr().(*net.TCPAddr).IP.String(),
		Port: srv.Listener.Addr().(*net.TCPAddr).Port,
	}}); err != nil {
		t.Fatal(err)
	}
	return rec
}

func (n *p3napcat) count() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.paths)
}

func (n *p3napcat) path(i int) string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.paths[i]
}

func (n *p3napcat) body(i int) map[string]any {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.bodies[i]
}

// num 断言 body 里 key 为数字并返回 float64。
func num(t *testing.T, body map[string]any, key string) float64 {
	t.Helper()
	v, ok := body[key].(float64)
	if !ok {
		t.Fatalf("请求体缺少数字字段 %s: %v", key, body)
	}
	return v
}

// str 断言 body 里 key 为字符串并返回。
func str(t *testing.T, body map[string]any, key string) string {
	t.Helper()
	v, ok := body[key].(string)
	if !ok {
		t.Fatalf("请求体缺少字符串字段 %s: %v", key, body)
	}
	return v
}

// boolv 断言 body 里 key 为布尔并返回。
func boolv(t *testing.T, body map[string]any, key string) bool {
	t.Helper()
	v, ok := body[key].(bool)
	if !ok {
		t.Fatalf("请求体缺少布尔字段 %s: %v", key, body)
	}
	return v
}

// rowsOf 把工具输出解析成 JSON 数组。
func rowsOf(t *testing.T, out string) []map[string]any {
	t.Helper()
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("输出不是 JSON 数组: %v (%s)", err, out)
	}
	return rows
}

// objOf 把工具输出解析成 JSON 对象。
func objOf(t *testing.T, out string) map[string]any {
	t.Helper()
	var obj map[string]any
	if err := json.Unmarshal([]byte(out), &obj); err != nil {
		t.Fatalf("输出不是 JSON 对象: %v (%s)", err, out)
	}
	return obj
}

// ---------- 消息 ----------

func TestRecallMessageTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "recall_message", `{"message_id":12345}`)
	if out != "{}" {
		t.Errorf("recall_message 输出 = %q, want {}", out)
	}
	if nap.count() != 1 || nap.path(0) != "/delete_msg" {
		t.Fatalf("NapCat 应收到 1 次 /delete_msg, got %v", nap.paths)
	}
	if num(t, nap.body(0), "message_id") != 12345 {
		t.Errorf("message_id 透传错误: %v", nap.body(0))
	}
	runToolErr(t, tools, "recall_message", `{"message_id":0}`)
	if nap.count() != 1 {
		t.Errorf("非法参数不应发请求, got %d", nap.count())
	}
}

func TestGetMsgHistoryTool(t *testing.T) {
	hist := map[string]any{"messages": []any{
		map[string]any{"message_id": 1, "raw_message": "a"},
		map[string]any{"message_id": 2, "raw_message": "b"},
	}}
	nap := startP3NapCat(t, func(string, map[string]any) any { return hist })
	tools, _, _ := testToolSet(t, nil)

	// 群：走 /get_group_msg_history
	out := runToolOK(t, tools, "get_msg_history", `{"chat_type":"group","chat_id":888,"message_seq":5,"count":10}`)
	if nap.path(0) != "/get_group_msg_history" {
		t.Errorf("群历史 path = %s", nap.path(0))
	}
	b := nap.body(0)
	if num(t, b, "group_id") != 888 || num(t, b, "count") != 10 || num(t, b, "message_seq") != 5 {
		t.Errorf("群历史参数错误: %v", b)
	}
	if got := objOf(t, out)["messages"]; got == nil {
		t.Errorf("messages 未透传: %s", out)
	}

	// 私聊：走 /get_friend_msg_history，chat_id 映射 user_id
	runToolOK(t, tools, "get_msg_history", `{"chat_type":"private","chat_id":20002,"count":10}`)
	if nap.path(1) != "/get_friend_msg_history" {
		t.Errorf("私聊历史 path = %s", nap.path(1))
	}
	if num(t, nap.body(1), "user_id") != 20002 {
		t.Errorf("私聊 user_id 错误: %v", nap.body(1))
	}

	// count 上限 20、默认 20
	runToolOK(t, tools, "get_msg_history", `{"chat_type":"group","chat_id":888,"count":50}`)
	if num(t, nap.body(2), "count") != 20 {
		t.Errorf("count=50 应 clamp 到 20, got %v", nap.body(2)["count"])
	}
	runToolOK(t, tools, "get_msg_history", `{"chat_type":"group","chat_id":888}`)
	if num(t, nap.body(3), "count") != 20 {
		t.Errorf("count 缺省应 20, got %v", nap.body(3)["count"])
	}

	runToolErr(t, tools, "get_msg_history", `{"chat_type":"channel","chat_id":888}`)
}

func TestGetForwardDetailTool(t *testing.T) {
	fwd := map[string]any{"messages": []any{
		map[string]any{"message_id": "node1", "raw_message": "转发内容"},
	}}
	nap := startP3NapCat(t, func(string, map[string]any) any { return fwd })
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "get_forward_detail", `{"forward_id":"fwd-abc"}`)
	if nap.path(0) != "/get_forward_msg" {
		t.Errorf("path = %s, want /get_forward_msg", nap.path(0))
	}
	// 文档：参数为字符串形态的 message_id
	if got := str(t, nap.body(0), "message_id"); got != "fwd-abc" {
		t.Errorf("message_id = %q, want fwd-abc", got)
	}
	if len(objOf(t, out)["messages"].([]any)) != 1 {
		t.Errorf("messages 未透传: %s", out)
	}
	runToolErr(t, tools, "get_forward_detail", `{"forward_id":""}`)
}

// ---------- 查询 ----------

func TestGetFriendListTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any {
		return []any{
			map[string]any{"user_id": 10001, "nickname": "甲", "remark": "备注甲", "level": 3, "extra": "略"},
			map[string]any{"user_id": 10002, "nickname": "乙", "remark": "", "level": 0},
		}
	})
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "get_friend_list", "")
	if nap.path(0) != "/get_friend_list" {
		t.Errorf("path = %s", nap.path(0))
	}
	rows := rowsOf(t, out)
	if len(rows) != 2 {
		t.Fatalf("应提取 2 个好友, got %s", out)
	}
	if rows[0]["user_id"] != float64(10001) || rows[0]["nickname"] != "甲" || rows[0]["remark"] != "备注甲" {
		t.Errorf("好友[0] = %v", rows[0])
	}
	if _, has := rows[0]["level"]; has {
		t.Errorf("应只提取 user_id/nickname/remark, got %v", rows[0])
	}
}

func TestLookupUserTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any {
		return map[string]any{"user_id": 10001, "nick": "路人甲", "age": 18, "level": 1}
	})
	tools, _, _ := testToolSet(t, nil)

	runToolErr(t, tools, "lookup_user", `{"qq":0}`)
	runToolErr(t, tools, "lookup_user", `{}`)

	out := runToolOK(t, tools, "lookup_user", `{"qq":10001}`)
	if nap.path(0) != "/get_stranger_info" {
		t.Errorf("path = %s", nap.path(0))
	}
	if num(t, nap.body(0), "user_id") != 10001 {
		t.Errorf("user_id 透传错误: %v", nap.body(0))
	}
	obj := objOf(t, out)
	if obj["nick"] != "路人甲" {
		t.Errorf("data 未透传: %s", out)
	}
}

func TestLookupGroupsTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any {
		return []any{
			map[string]any{"group_id": 888, "group_name": "测试群", "member_count": 10, "max_member_count": 500},
		}
	})
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "lookup_groups", "")
	if nap.path(0) != "/get_group_list" {
		t.Errorf("path = %s", nap.path(0))
	}
	rows := rowsOf(t, out)
	if len(rows) != 1 || rows[0]["group_id"] != float64(888) ||
		rows[0]["group_name"] != "测试群" || rows[0]["member_count"] != float64(10) {
		t.Errorf("群列表提取错误: %s", out)
	}
}

func TestLookupGroupTool(t *testing.T) {
	nap := startP3NapCat(t, func(path string, _ map[string]any) any {
		switch path {
		case "/get_group_info":
			return map[string]any{"group_id": 888, "group_name": "测试群", "member_count": 10, "max_member_count": 500}
		case "/get_group_info_ex":
			return map[string]any{"group_id": 888, "group_name": "测试群", "create_time": 1600000000, "shutup_timestamp": 0}
		}
		return map[string]any{}
	})
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "lookup_group", `{"group_id":888}`)
	if nap.count() != 2 {
		t.Fatalf("应合并 get_group_info + get_group_info_ex 两次调用, got %v", nap.paths)
	}
	obj := objOf(t, out)
	if obj["member_count"] != float64(10) {
		t.Errorf("get_group_info 数据缺失: %s", out)
	}
	if obj["create_time"] != float64(1600000000) {
		t.Errorf("get_group_info_ex 数据缺失: %s", out)
	}
	if obj["group_name"] != "测试群" {
		t.Errorf("合并后 group_name = %v", obj["group_name"])
	}
	runToolErr(t, tools, "lookup_group", `{"group_id":0}`)
}

func TestLookupMembersTool(t *testing.T) {
	mk := func(n int) []any {
		out := make([]any, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, map[string]any{
				"user_id": 10000 + i, "nickname": "成员", "card": "名片", "role": "member",
				"join_time": 0, "last_sent_time": 0, "level": 1,
			})
		}
		return out
	}
	nap := startP3NapCat(t, func(string, map[string]any) any { return mk(102) })
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "lookup_members", `{"group_id":888}`)
	if nap.path(0) != "/get_group_member_list" {
		t.Errorf("path = %s", nap.path(0))
	}
	obj := objOf(t, out)
	if obj["total"] != float64(102) || obj["returned"] != float64(100) || obj["truncated"] != true {
		t.Errorf("截断信息错误: %s", out)
	}
	members := obj["members"].([]any)
	if len(members) != 100 {
		t.Errorf("应截断至 100 条, got %d", len(members))
	}
	m0 := members[0].(map[string]any)
	for _, k := range []string{"user_id", "nickname", "card", "role"} {
		if _, ok := m0[k]; !ok {
			t.Errorf("成员条目缺少 %s: %v", k, m0)
		}
	}
	if _, ok := m0["join_time"]; ok {
		t.Errorf("应只提取关键字段, got %v", m0)
	}

	// 未超上限：不截断
	nap2 := startP3NapCat(t, func(string, map[string]any) any { return mk(2) })
	tools2, _, _ := testToolSet(t, nil)
	out = runToolOK(t, tools2, "lookup_members", `{"group_id":888}`)
	obj = objOf(t, out)
	if obj["truncated"] != false || obj["total"] != float64(2) {
		t.Errorf("小列表不应截断: %s", out)
	}
	if nap2.path(0) != "/get_group_member_list" {
		t.Errorf("path = %s", nap2.path(0))
	}
}

func TestLookupMemberTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any {
		return map[string]any{"user_id": 10001, "card": "名片", "role": "admin", "level": 5}
	})
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "lookup_member", `{"group_id":888,"user_id":10001}`)
	if nap.path(0) != "/get_group_member_info" {
		t.Errorf("path = %s", nap.path(0))
	}
	b := nap.body(0)
	if num(t, b, "group_id") != 888 || num(t, b, "user_id") != 10001 {
		t.Errorf("参数透传错误: %v", b)
	}
	if objOf(t, out)["role"] != "admin" {
		t.Errorf("data 未透传: %s", out)
	}
	runToolErr(t, tools, "lookup_member", `{"group_id":888}`)
	runToolErr(t, tools, "lookup_member", `{"user_id":10001}`)
}

func TestGetGroupFilesTool(t *testing.T) {
	files := map[string]any{"files": []any{map[string]any{"file_id": "f1", "name": "a.txt"}}, "folders": []any{}}
	nap := startP3NapCat(t, func(string, map[string]any) any { return files })
	tools, _, _ := testToolSet(t, nil)

	// 无 folder_id → 根目录
	out := runToolOK(t, tools, "get_group_files", `{"group_id":888}`)
	if nap.path(0) != "/get_group_root_files" {
		t.Errorf("根目录 path = %s", nap.path(0))
	}
	if objOf(t, out)["files"] == nil {
		t.Errorf("data 未透传: %s", out)
	}

	// 有 folder_id → 子目录
	runToolOK(t, tools, "get_group_files", `{"group_id":888,"folder_id":"abc"}`)
	if nap.path(1) != "/get_group_files_by_folder" {
		t.Errorf("子目录 path = %s", nap.path(1))
	}
	if got := str(t, nap.body(1), "folder_id"); got != "abc" {
		t.Errorf("folder_id = %q", got)
	}
	runToolErr(t, tools, "get_group_files", `{"group_id":0}`)
}

// ---------- 群管理 ----------

func TestSetGroupCardTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "set_group_card", `{"group_id":888,"user_id":10001,"card":"新名片"}`)
	if out != "{}" {
		t.Errorf("输出 = %q", out)
	}
	if nap.path(0) != "/set_group_card" {
		t.Errorf("path = %s", nap.path(0))
	}
	b := nap.body(0)
	if num(t, b, "group_id") != 888 || num(t, b, "user_id") != 10001 || str(t, b, "card") != "新名片" {
		t.Errorf("参数错误: %v", b)
	}
	runToolErr(t, tools, "set_group_card", `{"group_id":888}`)
}

func TestSetGroupSpecialTitleTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools, _, _ := testToolSet(t, nil)

	runToolOK(t, tools, "set_group_special_title", `{"group_id":888,"user_id":10001,"title":"头衔王"}`)
	if nap.path(0) != "/set_group_special_title" {
		t.Errorf("path = %s", nap.path(0))
	}
	b := nap.body(0)
	if num(t, b, "group_id") != 888 || num(t, b, "user_id") != 10001 || str(t, b, "special_title") != "头衔王" {
		t.Errorf("参数错误: %v", b)
	}
	runToolErr(t, tools, "set_group_special_title", `{"group_id":888,"user_id":10001}`)
}

func TestGroupBanTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools, _, _ := testToolSet(t, nil)

	runToolOK(t, tools, "group_ban", `{"group_id":888,"user_id":10001,"duration":600}`)
	if nap.path(0) != "/set_group_ban" {
		t.Errorf("path = %s", nap.path(0))
	}
	b := nap.body(0)
	if num(t, b, "group_id") != 888 || num(t, b, "user_id") != 10001 || num(t, b, "duration") != 600 {
		t.Errorf("参数错误: %v", b)
	}

	// 超上限 / 负数：报错且不发请求
	for _, bad := range []string{
		`{"group_id":888,"user_id":10001,"duration":2592001}`,
		`{"group_id":888,"user_id":10001,"duration":-5}`,
	} {
		err := runToolErr(t, tools, "group_ban", bad)
		if !strings.Contains(err.Error(), "2592000") {
			t.Errorf("禁言时长校验应提示上限 2592000: %v", err)
		}
	}
	if nap.count() != 1 {
		t.Errorf("非法时长不应发请求, got %d", nap.count())
	}
}

func TestGroupWholeBanTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools, _, _ := testToolSet(t, nil)

	runToolOK(t, tools, "group_whole_ban", `{"group_id":888,"enable":true}`)
	if nap.path(0) != "/set_group_whole_ban" {
		t.Errorf("path = %s", nap.path(0))
	}
	if num(t, nap.body(0), "group_id") != 888 || !boolv(t, nap.body(0), "enable") {
		t.Errorf("参数错误: %v", nap.body(0))
	}
	runToolOK(t, tools, "group_whole_ban", `{"group_id":888,"enable":false}`)
	if nap.count() != 2 {
		t.Errorf("enable=false 也应可调用, got %d", nap.count())
	}
}

func TestKickMemberTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools := testToolSetAdmin(t)

	runToolOK(t, tools, "kick_member", `{"group_id":888,"user_id":10001,"reject_add_request":true}`)
	if nap.path(0) != "/set_group_kick" {
		t.Errorf("path = %s", nap.path(0))
	}
	b := nap.body(0)
	if num(t, b, "group_id") != 888 || num(t, b, "user_id") != 10001 || !boolv(t, b, "reject_add_request") {
		t.Errorf("参数错误: %v", b)
	}
}

func TestSetGroupAdminTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools := testToolSetAdmin(t)

	runToolOK(t, tools, "set_group_admin", `{"group_id":888,"user_id":10001,"enable":false}`)
	if nap.path(0) != "/set_group_admin" {
		t.Errorf("path = %s", nap.path(0))
	}
	b := nap.body(0)
	if num(t, b, "group_id") != 888 || num(t, b, "user_id") != 10001 {
		t.Errorf("参数错误: %v", b)
	}
	if v, ok := b["enable"]; ok && v != false {
		t.Errorf("enable=false 不应发送 true, got %v", b)
	}
}

func TestSetGroupNameTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools := testToolSetAdmin(t)

	runToolOK(t, tools, "set_group_name", `{"group_id":888,"name":"新群名"}`)
	if nap.path(0) != "/set_group_name" {
		t.Errorf("path = %s", nap.path(0))
	}
	if num(t, nap.body(0), "group_id") != 888 || str(t, nap.body(0), "group_name") != "新群名" {
		t.Errorf("参数错误: %v", nap.body(0))
	}
	runToolErr(t, tools, "set_group_name", `{"group_id":888,"name":""}`)
}

func TestLeaveGroupTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools := testToolSetAdmin(t)

	runToolOK(t, tools, "leave_group", `{"group_id":888}`)
	if nap.path(0) != "/set_group_leave" {
		t.Errorf("path = %s", nap.path(0))
	}
	if num(t, nap.body(0), "group_id") != 888 {
		t.Errorf("参数错误: %v", nap.body(0))
	}
}

func TestSendGroupNoticeTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools := testToolSetAdmin(t)

	out := runToolOK(t, tools, "send_group_notice", `{"group_id":888,"content":"本周六聚餐"}`)
	if out != "{}" {
		t.Errorf("输出 = %q", out)
	}
	if nap.path(0) != "/_send_group_notice" {
		t.Errorf("path = %s, want /_send_group_notice（文档路径）", nap.path(0))
	}
	b := nap.body(0)
	if num(t, b, "group_id") != 888 || str(t, b, "content") != "本周六聚餐" {
		t.Errorf("参数错误: %v", b)
	}
	// 文档必填默认值：pinned=0 type=1 confirm_required=1 is_show_edit_card=0 tip_window_type=0
	if b["pinned"] != float64(0) || b["type"] != float64(1) ||
		b["confirm_required"] != float64(1) || b["is_show_edit_card"] != float64(0) ||
		b["tip_window_type"] != float64(0) {
		t.Errorf("公告默认参数错误: %v", b)
	}
	if _, ok := b["image"]; ok {
		t.Errorf("未传 image 不应输出: %v", b)
	}

	// 带图片
	runToolOK(t, tools, "send_group_notice", `{"group_id":888,"content":"看图","image":"https://e.com/a.jpg"}`)
	if str(t, nap.body(1), "image") != "https://e.com/a.jpg" {
		t.Errorf("image 未透传: %v", nap.body(1))
	}

	// content 与 image 都缺：报错
	runToolErr(t, tools, "send_group_notice", `{"group_id":888}`)
	if nap.count() != 2 {
		t.Errorf("无内容公告不应发请求, got %d", nap.count())
	}
}

func TestEssenceTools(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools, _, _ := testToolSet(t, nil)

	runToolOK(t, tools, "set_essence", `{"message_id":777}`)
	if nap.path(0) != "/set_essence_msg" || num(t, nap.body(0), "message_id") != 777 {
		t.Errorf("set_essence 错误: %v %v", nap.paths, nap.body(0))
	}
	runToolOK(t, tools, "remove_essence", `{"message_id":778}`)
	if nap.path(1) != "/delete_essence_msg" || num(t, nap.body(1), "message_id") != 778 {
		t.Errorf("remove_essence 错误: %v %v", nap.paths, nap.body(1))
	}
	runToolErr(t, tools, "set_essence", `{"message_id":0}`)
}

// ---------- 互动 ----------

func TestPokeTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools, _, _ := testToolSet(t, nil)

	runToolOK(t, tools, "poke", `{"chat_type":"group","chat_id":888,"user_id":10001}`)
	if nap.path(0) != "/group_poke" {
		t.Errorf("群戳一戳 path = %s", nap.path(0))
	}
	b := nap.body(0)
	if num(t, b, "group_id") != 888 || num(t, b, "user_id") != 10001 {
		t.Errorf("群戳一戳参数错误: %v", b)
	}

	runToolOK(t, tools, "poke", `{"chat_type":"private","chat_id":20002,"user_id":20002}`)
	if nap.path(1) != "/friend_poke" {
		t.Errorf("好友戳一戳 path = %s", nap.path(1))
	}
	if num(t, nap.body(1), "user_id") != 20002 {
		t.Errorf("好友戳一戳参数错误: %v", nap.body(1))
	}

	runToolErr(t, tools, "poke", `{"chat_type":"channel","chat_id":888,"user_id":1}`)
	runToolErr(t, tools, "poke", `{"chat_type":"group","chat_id":888,"user_id":0}`)
}

func TestSendLikeTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools, _, _ := testToolSet(t, nil)

	runToolOK(t, tools, "send_like", `{"qq":10001}`)
	if nap.path(0) != "/send_like" {
		t.Errorf("path = %s", nap.path(0))
	}
	if num(t, nap.body(0), "user_id") != 10001 || num(t, nap.body(0), "times") != 1 {
		t.Errorf("默认 times 应为 1: %v", nap.body(0))
	}

	// times clamp 到 10
	runToolOK(t, tools, "send_like", `{"qq":10001,"times":99}`)
	if num(t, nap.body(1), "times") != 10 {
		t.Errorf("times=99 应 clamp 到 10, got %v", nap.body(1)["times"])
	}
	runToolErr(t, tools, "send_like", `{"qq":0}`)
}

func TestHandleFriendRequestTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools, _, _ := testToolSet(t, nil)

	runToolOK(t, tools, "handle_friend_request", `{"flag":"flag_1","approve":true}`)
	if nap.path(0) != "/set_friend_add_request" {
		t.Errorf("path = %s", nap.path(0))
	}
	b := nap.body(0)
	if str(t, b, "flag") != "flag_1" || !boolv(t, b, "approve") {
		t.Errorf("参数错误: %v", b)
	}
	runToolErr(t, tools, "handle_friend_request", `{"approve":true}`)
}

func TestHandleGroupRequestTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools, _, _ := testToolSet(t, nil)

	runToolOK(t, tools, "handle_group_request", `{"flag":"flag_2","sub_type":"add","approve":false}`)
	if nap.path(0) != "/set_group_add_request" {
		t.Errorf("path = %s", nap.path(0))
	}
	b := nap.body(0)
	if str(t, b, "flag") != "flag_2" || str(t, b, "sub_type") != "add" {
		t.Errorf("参数错误: %v", b)
	}
	// approve=false 也不报错；sub_type 非法报错
	runToolErr(t, tools, "handle_group_request", `{"flag":"f","sub_type":"addd","approve":true}`)
	runToolErr(t, tools, "handle_group_request", `{"flag":"","sub_type":"add","approve":true}`)
}

// ---------- 文件 ----------

func TestUploadGroupFileTool(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any {
		return map[string]any{"file_id": "file-uuid-1"}
	})
	tools, _, _ := testToolSet(t, nil)

	out := runToolOK(t, tools, "upload_group_file", `{"group_id":888,"file":"C:/data/a.txt","name":"a.txt"}`)
	if nap.path(0) != "/upload_group_file" {
		t.Errorf("path = %s", nap.path(0))
	}
	b := nap.body(0)
	if num(t, b, "group_id") != 888 || str(t, b, "file") != "/mnt/c/data/a.txt" || str(t, b, "name") != "a.txt" {
		t.Errorf("参数错误（Windows 路径应映射 NapCat 侧）: %v", b)
	}
	if !boolv(t, b, "upload_file") {
		t.Errorf("upload_file 应为 true: %v", b)
	}
	if objOf(t, out)["file_id"] != "file-uuid-1" {
		t.Errorf("data 未透传: %s", out)
	}

	// http / https / base64 前缀直接拒绝，不发请求
	for _, bad := range []string{
		`{"group_id":888,"file":"http://e.com/a.txt","name":"a.txt"}`,
		`{"group_id":888,"file":"https://e.com/a.txt","name":"a.txt"}`,
		`{"group_id":888,"file":"base64://AAAA","name":"a.txt"}`,
	} {
		err := runToolErr(t, tools, "upload_group_file", bad)
		if !strings.Contains(err.Error(), "本地路径") {
			t.Errorf("http/base64 应提示仅本地路径: %v", err)
		}
	}
	if nap.count() != 1 {
		t.Errorf("http/base64 不应发请求, got %d", nap.count())
	}
	runToolErr(t, tools, "upload_group_file", `{"group_id":888,"file":"C:/a.txt","name":""}`)
}

// ---------- 万能逃生舱 ----------

func TestNapcatAPITool(t *testing.T) {
	nap := startP3NapCat(t, func(path string, _ map[string]any) any {
		switch path {
		case "/get_version_info":
			return map[string]any{"app_name": "NapCat", "version": "4.8.99"}
		case "/get_status":
			return map[string]any{"online": true, "good": true}
		default:
			return map[string]any{}
		}
	})
	tools, _, _ := testToolSet(t, nil)

	// 正常调用：无参动作
	out := runToolOK(t, tools, "napcat_api", `{"action":"get_version_info"}`)
	if nap.path(0) != "/get_version_info" {
		t.Errorf("path = %s", nap.path(0))
	}
	if !strings.Contains(out, "NapCat") {
		t.Errorf("data 未透传: %s", out)
	}

	// "/" 前缀可带可不带
	runToolOK(t, tools, "napcat_api", `{"action":"/get_status"}`)
	if nap.path(1) != "/get_status" {
		t.Errorf("path = %s", nap.path(1))
	}

	// 带参数对象
	runToolOK(t, tools, "napcat_api", `{"action":"set_group_ban","params":{"group_id":888,"user_id":10001,"duration":60}}`)
	if nap.path(2) != "/set_group_ban" {
		t.Errorf("path = %s", nap.path(2))
	}
	b := nap.body(2)
	if num(t, b, "group_id") != 888 || num(t, b, "user_id") != 10001 || num(t, b, "duration") != 60 {
		t.Errorf("params 未透传: %v", b)
	}

	// 未知动作：短提示导向可搜索目录，避免一次灌入完整动作列表。
	err := runToolErr(t, tools, "napcat_api", `{"action":"no_such_action"}`)
	if !strings.Contains(err.Error(), "no_such_action") || !strings.Contains(err.Error(), "napcat_docs") {
		t.Errorf("未知动作报错应含动作名与目录提示: %v", err)
	}

	// params 非对象：报错
	runToolErr(t, tools, "napcat_api", `{"action":"get_login_info","params":"oops"}`)
	runToolErr(t, tools, "napcat_api", `{"action":""}`)

	// 危险动作非管理员：报错且不发请求
	err = runToolErr(t, tools, "napcat_api", `{"action":"set_group_kick","params":{"group_id":888,"user_id":5}}`)
	if !strings.Contains(err.Error(), "管理员权限") {
		t.Errorf("危险动作非管理员应报权限错误: %v", err)
	}
	if nap.count() != 3 {
		t.Errorf("危险动作不应发请求, got %d", nap.count())
	}

	// 危险动作管理员放行
	adminTools := testToolSetAdmin(t)
	runToolOK(t, adminTools, "napcat_api", `{"action":"/set_group_kick","params":{"group_id":888,"user_id":5}}`)
	if nap.path(3) != "/set_group_kick" {
		t.Errorf("管理员逃生舱 path = %s", nap.path(3))
	}
}

func TestNapcatDocsTool(t *testing.T) {
	tools, _, _ := testToolSet(t, nil)
	out := runToolOK(t, tools, "napcat_docs", "")

	var docs struct {
		Actions    []map[string]any `json:"actions"`
		Note       string           `json:"note"`
		Categories map[string]int   `json:"categories"`
		Total      int              `json:"total"`
	}
	if err := json.Unmarshal([]byte(out), &docs); err != nil {
		t.Fatalf("napcat_docs 输出不是合法 JSON: %v (%s)", err, out)
	}
	if len(docs.Actions) == 0 || len(docs.Actions) > 25 || docs.Total <= len(docs.Actions) {
		t.Errorf("默认应只返回有界首屏: %d / %d", len(docs.Actions), docs.Total)
	}
	for _, a := range docs.Actions {
		name, _ := a["action"].(string)
		desc, _ := a["desc"].(string)
		if name == "" || desc == "" {
			t.Errorf("动作条目缺 action/desc: %v", a)
		}
	}
	if docs.Note == "" {
		t.Error("应附按需查询说明")
	}
	if docs.Categories["空间"] == 0 || docs.Categories["群文件"] == 0 {
		t.Errorf("默认应提供分类导航: %#v", docs.Categories)
	}
	// 代表性动作通过按需详情查询，不要求常驻首屏。
	for _, want := range []string{"get_login_info", "set_group_ban", "upload_group_file", "get_forward_msg"} {
		out := runToolOK(t, tools, "napcat_docs", `{"action":"`+want+`"}`)
		if objOf(t, out)["action"] != want {
			t.Errorf("napcat_docs 找不到动作 %q", want)
		}
	}
}

func TestActionRegistryCoversAllActions(t *testing.T) {
	// 全量动作注册表：与 api 包 196 个动作一一对应（192 个 P3-1 + api.go 4 个）
	if len(actionRegistry) != 196 {
		t.Errorf("动作注册表数量 = %d, want 196", len(actionRegistry))
	}
	for name, typ := range actionRegistry {
		if name == "" || strings.HasPrefix(name, "/") {
			t.Errorf("注册表键非法: %q", name)
		}
		req := reflect.New(typ).Interface().(interface{ Path() string })
		if strings.TrimPrefix(req.Path(), "/") != name {
			t.Errorf("注册表键 %q 与 Path() 不一致", name)
		}
	}
	for _, want := range []string{"send_group_msg", "delete_msg", "get_friend_list", "upload_group_file", "set_group_kick"} {
		if _, ok := actionRegistry[want]; !ok {
			t.Errorf("注册表缺动作 %q", want)
		}
	}
}
