package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNapcatGatewayCannotBypassPermissions(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return nil })
	tools, _, _ := testToolSet(t, nil)
	for _, action := range []string{
		"set_group_admin", "set_group_kick_members", "set_group_name",
		"delete_group_file", "set_qq_profile",
	} {
		t.Run(action, func(t *testing.T) {
			err := runToolErr(t, tools, "napcat_api", `{"action":"`+action+`","params":{}}`)
			if !strings.Contains(err.Error(), "管理员权限") {
				t.Fatalf("want authorization error before parameter validation, got %v", err)
			}
		})
	}
	for _, action := range []string{
		"get_cookies", "get_credentials", "get_clientkey", "get_csrf_token",
		"request_decrypt_key", "send_packet", ".send_packet", "bot_exit", "set_restart",
	} {
		t.Run(action, func(t *testing.T) {
			runToolErr(t, testToolSetAdmin(t), "napcat_api", `{"action":"`+action+`"}`)
		})
	}
	if nap.count() != 0 {
		t.Fatalf("forbidden requests reached NapCat: %d", nap.count())
	}
}

func TestNapcatGatewayValidatesSchemaBeforeSending(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return nil })
	tools, _, _ := testToolSet(t, nil)
	for _, params := range []string{
		`{}`, `null`, `[]`, `{"group_id":888}`, `{"group_id":0,"user_id":5}`,
		`{"group_id":888,"user_id":"5"}`, `{"group_id":888,"user_id":5,"duraton":60}`,
		`{"group_id":888,"user_id":5,"duration":2592001}`,
		`{"group_id":888,"user_id":5,"duration":-1}`,
	} {
		t.Run(params, func(t *testing.T) {
			runToolErr(t, tools, "napcat_api", `{"action":"set_group_ban","params":`+params+`}`)
		})
	}
	if nap.count() != 0 {
		t.Fatalf("invalid requests reached NapCat: %d", nap.count())
	}
}

func TestNapcatGatewayPreservesFalseZeroAndOfficialFields(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{"ok": true} })
	tools, _, _ := testToolSet(t, nil)
	runToolOK(t, tools, "napcat_api", `{"action":"set_group_add_request","params":{"flag":"req-1","sub_type":"invite","approve":false,"reason":""}}`)
	if b := nap.body(0); b["sub_type"] != "invite" || b["approve"] != false || b["reason"] != "" {
		t.Fatalf("parameters lost or changed: %#v", b)
	}
	runToolOK(t, tools, "napcat_api", `{"action":"set_group_ban","params":{"group_id":888,"user_id":5,"duration":0}}`)
	if b := nap.body(1); b["duration"] != float64(0) {
		t.Fatalf("explicit zero lost: %#v", b)
	}
	// This official action is not in the old typed registry.
	runToolOK(t, tools, "napcat_api", `{"action":"fetch_sys_faces"}`)
	if nap.path(2) != "/fetch_sys_faces" {
		t.Fatalf("official long-tail action not routed: %s", nap.path(2))
	}
}

func TestNapcatGatewayChecksEveryAuthoredMessage(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return nil })
	tools, _, _ := testToolSet(t, nil)
	for _, args := range []string{
		`{"action":"send_group_msg","params":{"group_id":888,"message":"[群:888] <小明> 不要复制"}}`,
		`{"action":"send_private_msg","params":{"user_id":5,"message":[{"type":"text","data":{"text":"[群:888] <小明> 不要复制"}}]}}`,
		`{"action":"send_msg","params":{"user_id":5,"message":[{"type":"text","data":{"text":"[群:"}},{"type":"text","data":{"text":"888] <小明> 不要复制"}}]}}`,
		`{"action":"send_group_forward_msg","params":{"group_id":888,"messages":[{"type":"node","data":{"user_id":"5","nickname":"小明","content":[{"type":"text","data":{"text":"[系统通知] 不要复制"}}]}}]}}`,
		`{"action":"send_group_forward_msg","params":{"group_id":888,"messages":[],"summary":"[思考笔记] 不要复制"}}`,
		`{"action":"send_qzone_msg","params":{"content":"<analysis>不要复制"}}`,
		`{"action":"comment_qzone_public_feed","params":{"feed":{"cell":"untouched"},"content":"[系统通知] 不要复制"}}`,
		`{"action":"send_group_ai_record","params":{"group_id":888,"character":"voice","text":"[系统通知] 不要复制"}}`,
	} {
		t.Run(args, func(t *testing.T) {
			err := runToolErr(t, tools, "napcat_api", args)
			if !strings.Contains(err.Error(), "正文") {
				t.Fatalf("want outgoing-text rejection, got %v", err)
			}
		})
	}
	runToolErr(t, tools, "post_qzone", `{"text":"[系统通知] 不要复制"}`)
	runToolErr(t, tools, "comment_qzone", `{"tid":"1","content":"[思考笔记] 不要复制"}`)
	if nap.count() != 0 {
		t.Fatalf("context prefixes escaped: %d", nap.count())
	}
	// Feed metadata is third-party input, not authored outgoing text.
	runToolOK(t, tools, "napcat_api", `{"action":"comment_qzone_public_feed","params":{"feed":{"text":"[系统通知] 他人的原文"},"content":"确实挺有意思"}}`)
	if nap.count() != 1 {
		t.Fatal("legitimate comment was not sent")
	}
}

func TestNapcatDocsSearchPaginationAndDetail(t *testing.T) {
	tools, _, _ := testToolSet(t, nil)
	var page struct {
		Actions []struct {
			Action string `json:"action"`
			Policy string `json:"policy"`
		} `json:"actions"`
		Total      int  `json:"total"`
		NextOffset *int `json:"next_offset"`
	}
	first := runToolOK(t, tools, "napcat_docs", `{"query":"qzone","limit":2}`)
	if err := json.Unmarshal([]byte(first), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Actions) != 2 || page.Total <= 2 || page.NextOffset == nil || *page.NextOffset != 2 {
		t.Fatalf("not a bounded searchable page: %s", first)
	}
	firstName := page.Actions[0].Action
	second := runToolOK(t, tools, "napcat_docs", `{"query":"qzone","offset":2,"limit":2}`)
	if err := json.Unmarshal([]byte(second), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Actions) != 2 || page.Actions[0].Action == firstName {
		t.Fatalf("pagination did not advance: %s", second)
	}
	for _, a := range page.Actions {
		if !strings.Contains(a.Action, "qzone") || a.Policy == "" {
			t.Fatalf("unexpected search result: %#v", a)
		}
	}
	detail := objOf(t, runToolOK(t, tools, "napcat_docs", `{"action":"set_group_add_request"}`))
	schema, ok := detail["input_schema"].(map[string]any)
	if !ok || schema["properties"].(map[string]any)["sub_type"] == nil || detail["examples"] == nil {
		t.Fatalf("missing official parameter schema or examples: %#v", detail)
	}
	if detail["policy"] != "allowed" {
		t.Fatalf("docs do not reflect local policy: %#v", detail)
	}
	secret := objOf(t, runToolOK(t, tools, "napcat_docs", `{"action":"get_cookies"}`))
	if secret["policy"] != "disabled" || secret["policy_reason"] == "" {
		t.Fatalf("sensitive API advertised as usable: %#v", secret)
	}
	runToolErr(t, tools, "napcat_docs", `{"offset":-1}`)
	runToolErr(t, tools, "napcat_docs", `{"limit":-1}`)
}

func TestNapcatUnknownActionErrorIsBounded(t *testing.T) {
	tools, _, _ := testToolSet(t, nil)
	err := runToolErr(t, tools, "napcat_api", `{"action":"no_such_action"}`)
	if len(err.Error()) > 400 || !strings.Contains(err.Error(), "napcat_docs") {
		t.Fatalf("unknown action dumped the registry: %v", err)
	}
}

func TestNapcatSensitiveWrappersUseGatewayPolicy(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return nil })
	tools, _, _ := testToolSet(t, nil)
	for _, name := range []string{"set_group_name", "send_group_notice"} {
		err := runToolErr(t, tools, name, `{"group_id":888,"name":"新名字","content":"新公告"}`)
		if !strings.Contains(err.Error(), "管理员权限") {
			t.Fatalf("%s does not share authorization: %v", name, err)
		}
	}
	if nap.count() != 0 {
		t.Fatal("unauthorized wrapper request was sent")
	}
}

func TestNapcatGatewayBoundsLargeResults(t *testing.T) {
	startP3NapCat(t, func(string, map[string]any) any {
		return map[string]any{"feeds": []string{strings.Repeat("文", 18000)}}
	})
	tools, _, _ := testToolSet(t, nil)
	out := runToolOK(t, tools, "napcat_api", `{"action":"get_qzone_public_feeds"}`)
	result := objOf(t, out)
	if result["truncated"] != true || len([]rune(out)) > 12500 || result["note"] == "" {
		t.Fatalf("oversized result not explicitly bounded: %d runes", len([]rune(out)))
	}
}

func TestNapcatGatewayAliasCanonicalizesAndKeepsRichContent(t *testing.T) {
	nap := startP3NapCat(t, func(string, map[string]any) any { return map[string]any{} })
	tools, _, _ := testToolSet(t, nil)
	runToolOK(t, tools, "napcat_api", `{"action":"/send_group_sign","params":{"group_id":888}}`)
	if nap.path(0) != "/set_group_sign" {
		t.Fatalf("alias not canonicalized: %s", nap.path(0))
	}
	runToolOK(t, tools, "napcat_api", `{"action":"comment_qzone_public_feed","params":{"feed":{"opaque":{"id":"9007199254740993"}},"content":[{"type":1,"text":"感谢分享"}]}}`)
	if b := nap.body(1); len(b["content"].([]any)) != 1 || b["feed"] == nil {
		t.Fatalf("opaque rich fields were dropped: %#v", b)
	}
	runToolErr(t, tools, "napcat_api", `{"action":"comment_qzone_public_feed","params":{"feed":{},"content":[{"text":"[系统通知] 泄漏"}]}}`)
	if nap.count() != 2 {
		t.Fatal("rich text leaked")
	}
}

func TestNapcatGatewayDefaultsToDenyUnreviewedActions(t *testing.T) {
	if err := authorizeNapcatAction("new_upstream_action_not_reviewed", true); err == nil {
		t.Fatal("adding a catalog entry must not automatically grant execution")
	}
}

func TestNapcatWrappersPreserveFalseDecisions(t *testing.T) {
	for _, tc := range []struct {
		tool string
		args string
		key  string
	}{
		{"handle_friend_request", `{"flag":"friend-1","approve":false}`, "approve"},
		{"handle_group_request", `{"flag":"group-1","sub_type":"invite","approve":false}`, "approve"},
		{"group_whole_ban", `{"group_id":888,"enable":false}`, "enable"},
		{"set_group_admin", `{"group_id":888,"user_id":5,"enable":false}`, "enable"},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			nap := startP3NapCat(t, func(string, map[string]any) any { return nil })
			runToolOK(t, testToolSetAdmin(t), tc.tool, tc.args)
			b := nap.body(0)
			if got, present := b[tc.key]; !present || got != false {
				t.Fatalf("explicit false must reach upstream, got %#v", b)
			}
		})
	}
}
