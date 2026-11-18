package qq_test

import (
	"encoding/json"
	"strings"
	"testing"

	"Q-Mi/agent/tools/qq"
	"Q-Mi/api"
)

func TestGatewayDeniesAdminActionBeforeTransport(t *testing.T) {
	calls := 0
	deps := &qq.Dependencies{Call: func(api.Request) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{}`), nil
	}}
	for _, item := range qq.Tools(deps) {
		if item.Name != "napcat_api" {
			continue
		}
		_, err := item.Run(json.RawMessage(`{"action":"set_group_admin","params":{}}`))
		if err == nil || !strings.Contains(err.Error(), "管理员权限") || calls != 0 {
			t.Fatalf("authorization = %v, transport calls = %d", err, calls)
		}
		return
	}
	t.Fatal("missing gateway")
}

func TestGatewayRetainsExplicitFalseAndZero(t *testing.T) {
	var payload []byte
	deps := &qq.Dependencies{Call: func(req api.Request) (json.RawMessage, error) {
		var err error
		payload, err = json.Marshal(req)
		return json.RawMessage(`{}`), err
	}}
	for _, item := range qq.Tools(deps) {
		if item.Name != "napcat_api" {
			continue
		}
		_, err := item.Run(json.RawMessage(`{"action":"set_group_ban","params":{"group_id":8,"user_id":9,"duration":0}}`))
		if err != nil || !strings.Contains(string(payload), `"duration":0`) {
			t.Fatalf("explicit zero = %s, %v", payload, err)
		}
		_, err = item.Run(json.RawMessage(`{"action":"set_group_add_request","params":{"flag":"req","sub_type":"invite","approve":false}}`))
		if err != nil || !strings.Contains(string(payload), `"approve":false`) {
			t.Fatalf("explicit false = %s, %v", payload, err)
		}
		return
	}
	t.Fatal("missing gateway")
}
