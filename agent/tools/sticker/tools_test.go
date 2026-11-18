package sticker_test

import (
	"encoding/json"
	"testing"

	"Q-Mi/agent/tools/sticker"
	"Q-Mi/api"
)

func TestSendQQFaceRetainsTargetAndReply(t *testing.T) {
	var request api.Request
	deps := &sticker.Dependencies{Call: func(req api.Request) (json.RawMessage, error) {
		request = req
		return json.RawMessage(`{"message_id":42}`), nil
	}}
	for _, item := range sticker.Tools(deps) {
		if item.Name != "send_qq_face" {
			continue
		}
		out, err := item.Run(json.RawMessage(`{"chat_type":"group","chat_id":8,"emoji_id":"face-1","reply_to":10}`))
		if err != nil || !json.Valid([]byte(out)) {
			t.Fatalf("send result = %s, %v", out, err)
		}
		req, ok := request.(*api.SendCustomFace)
		if !ok || req.GroupID != 8 || req.UserID != 0 || req.ReplyTo != 10 || req.EmojiID != "face-1" {
			t.Fatalf("send target = %#v", request)
		}
		return
	}
	t.Fatal("missing send tool")
}
