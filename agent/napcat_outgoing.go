package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"Q-Mi/api"
)

// Validate authored fields only. In particular, opaque feed/comment objects
// obtained from other people must survive unchanged for Qzone interactions.
func validateNapcatOutgoing(req api.Request) error {
	var fields []string
	switch strings.TrimPrefix(req.Path(), "/") {
	case "send_msg", "send_group_msg", "send_private_msg",
		"send_forward_msg", "send_group_forward_msg", "send_private_forward_msg",
		"upload_forward_msg", "upload_foward_msg":
		fields = []string{"message", "messages", "source", "summary", "prompt", "news"}
	case "send_qzone_msg", "comment_qzone", "comment_qzone_public_feed", "reply_qzone_public_feed",
		"do_group_album_comment", "_send_group_notice":
		fields = []string{"content"}
	case "forward_qzone_public_feed":
		fields = []string{"reason"}
	case "get_ai_record", "send_group_ai_record":
		fields = []string{"text"}
	case "send_tuwen_ark":
		fields = []string{"title", "desc", "summary"}
	case "set_qq_profile":
		fields = []string{"nickname", "personal_note"}
	case "set_self_longnick":
		fields = []string{"longNick", "long_nick"}
	case "set_qzone_profile":
		fields = []string{"name", "description"}
	case "set_group_name":
		fields = []string{"group_name"}
	case "set_group_card":
		fields = []string{"card"}
	case "set_group_special_title":
		fields = []string{"special_title"}
	default:
		return nil
	}
	raw, err := json.Marshal(req)
	if err != nil {
		return err
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return err
	}
	for _, field := range fields {
		if err := validateAuthoredValue(params[field], 0); err != nil {
			return err
		}
	}
	return nil
}

func validateAuthoredValue(value any, depth int) error {
	if depth > 32 {
		return fmt.Errorf("消息正文嵌套过深，未发送")
	}
	switch v := value.(type) {
	case string:
		return validateOutgoingText(v)
	case []any:
		// Adjacent text segments can split an envelope across segment boundaries.
		var text strings.Builder
		for _, item := range v {
			seg, _ := item.(map[string]any)
			if seg["type"] == "text" {
				data, _ := seg["data"].(map[string]any)
				if s, ok := data["text"].(string); ok {
					text.WriteString(s)
					continue
				}
			}
			if err := validateAuthoredValue(item, depth+1); err != nil {
				return err
			}
		}
		return validateOutgoingText(text.String())
	case map[string]any:
		for _, child := range v {
			if err := validateAuthoredValue(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}
