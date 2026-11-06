package request

import (
	"context"
	"encoding/json"
	"testing"

	"Q-Mi/api"
	apimsg "Q-Mi/api/message"
)

// TestCall 断言通用 Call 的路径、鉴权与参数透传
func TestCall(t *testing.T) {
	addr, gotPath, gotAuth, gotBody := startMockNapCat(t,
		`{"status":"ok","retcode":0,"data":{"echo":"pong"},"message":"","wording":"","stream":"normal-action"}`)
	initTestClient(t, addr, "call-token")

	resp, err := Call(context.Background(), &SetGroupBanForCallTest{GroupID: 42, UserID: 7, Duration: 60})
	if err != nil {
		t.Fatalf("Call 失败: %v", err)
	}

	if *gotPath != "/set_group_ban" {
		t.Errorf("请求路径 = %s, want /set_group_ban", *gotPath)
	}
	if *gotAuth != "Bearer call-token" {
		t.Errorf("鉴权头 = %q, want %q", *gotAuth, "Bearer call-token")
	}
	if int64ToFloat((*gotBody)["group_id"]) != 42 || int64ToFloat((*gotBody)["duration"]) != 60 {
		t.Errorf("参数透传错误: %v", *gotBody)
	}

	// data 应原样透传为 RawMessage
	if string(resp.Data) != `{"echo":"pong"}` {
		t.Errorf("resp.Data = %s, want {\"echo\":\"pong\"}", resp.Data)
	}
	if resp.Retcode != 0 || resp.Status != "ok" {
		t.Errorf("响应信封错误: %+v", resp)
	}
}

// SetGroupBanForCallTest 测试专用请求结构体,复用 set_group_ban 路径
type SetGroupBanForCallTest struct {
	GroupID  int64 `json:"group_id"`
	UserID   int64 `json:"user_id"`
	Duration int64 `json:"duration"`
}

func (r *SetGroupBanForCallTest) Path() string { return "/set_group_ban" }

// TestCallRetcodeError retcode 非 0 时返回错误
func TestCallRetcodeError(t *testing.T) {
	addr, _, _, _ := startMockNapCat(t,
		`{"status":"failed","retcode":1400,"data":null,"message":"err","wording":"err","stream":"normal-action"}`)
	initTestClient(t, addr, "")

	_, err := Call(context.Background(), &SetGroupBanForCallTest{})
	if err == nil {
		t.Fatal("retcode=1400 应返回错误")
	}
}

// TestSendForwardMsgConvenience 合并转发便利函数:返回结构化 message_id
func TestSendForwardMsgConvenience(t *testing.T) {
	addr, gotPath, _, gotBody := startMockNapCat(t,
		`{"status":"ok","retcode":0,"data":{"message_id":888,"forward_id":"fwd-1"},"message":"","wording":"","stream":"normal-action"}`)
	initTestClient(t, addr, "")

	resp, err := SendForwardMsg(context.Background(),
		&api.SendForwardMsg{GroupID: 9, Message: []*apimsg.Message{apimsg.Text("n1")}})
	if err != nil {
		t.Fatalf("SendForwardMsg 失败: %v", err)
	}
	if *gotPath != "/send_forward_msg" {
		t.Errorf("请求路径 = %s, want /send_forward_msg", *gotPath)
	}
	if int64ToFloat((*gotBody)["group_id"]) != 9 {
		t.Errorf("group_id = %v, want 9", (*gotBody)["group_id"])
	}
	if resp.Data.MessageID != 888 || resp.Data.ForwardID == nil || *resp.Data.ForwardID != "fwd-1" {
		t.Errorf("返回数据错误: %+v", resp.Data)
	}
}

// TestGetMsgHistoryConvenience 历史消息便利函数:解析 messages 数组
func TestGetMsgHistoryConvenience(t *testing.T) {
	addr, gotPath, _, _ := startMockNapCat(t,
		`{"status":"ok","retcode":0,"data":{"messages":[{"message_id":1,"raw_message":"a"},{"message_id":2,"raw_message":"b"}]},"message":"","wording":"","stream":"normal-action"}`)
	initTestClient(t, addr, "")

	resp, err := GetGroupMsgHistory(context.Background(), &api.GetGroupMsgHistory{GroupID: 3, Count: 20})
	if err != nil {
		t.Fatalf("GetGroupMsgHistory 失败: %v", err)
	}
	if *gotPath != "/get_group_msg_history" {
		t.Errorf("请求路径 = %s, want /get_group_msg_history", *gotPath)
	}
	if len(resp.Data.Messages) != 2 || resp.Data.Messages[1].RawMessage != "b" {
		t.Errorf("messages 解析错误: %+v", resp.Data)
	}
}

// TestGetFriendListConvenience 好友列表便利函数
func TestGetFriendListConvenience(t *testing.T) {
	addr, _, _, _ := startMockNapCat(t,
		`{"status":"ok","retcode":0,"data":[{"user_id":1,"nickname":"a","remark":"","level":0}],"message":"","wording":"","stream":"normal-action"}`)
	initTestClient(t, addr, "")

	resp, err := GetFriendList(context.Background(), &api.GetFriendList{})
	if err != nil {
		t.Fatalf("GetFriendList 失败: %v", err)
	}
	if len(resp.Data) != 1 || resp.Data[0].UserID != 1 {
		t.Errorf("好友列表解析错误: %+v", json.RawMessage("见上"))
	}
}
