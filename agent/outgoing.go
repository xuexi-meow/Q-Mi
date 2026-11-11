package agent

import (
	"encoding/json"
	"errors"
	"html"
	"regexp"
	"strings"
)

var (
	contextChatLine = regexp.MustCompile(`^(?:\[(?:[0-9]{2}-[0-9]{2} )?[0-9]{2}:[0-9]{2}(?::[0-9]{2})?\]\s*)?(?:\[(?:群|私聊):[0-9]+\]\s*<[^>\r\n]+>|<我>)`)
	internalTagLine = regexp.MustCompile(`(?i)^</?(?:context|analysis|think|thinking|reasoning)(?:\s[^>]*)?>`)
	contextMetaLine = regexp.MustCompile(`^(?:\[(?:思考笔记|/?(?:聊天习惯|当前状态快照|工作上下文摘要)|系统通知|定时轮询|定时提醒|后台任务)\]|当前(?:情绪状态|时间)\s*[:：])`)
	codeFenceLine   = regexp.MustCompile("^(`{3,}|~{3,})(.*)$")
)

// validateOutgoingText rejects copied context envelopes instead of guessing which
// part to delete. Explicit code blocks and blockquotes can still quote raw logs.
func validateOutgoingText(text string) error {
	envelope := copiedContextEnvelope(text)
	fence := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(html.UnescapeString(raw))
		if m := codeFenceLine.FindStringSubmatch(line); m != nil {
			if fence == "" {
				fence = m[1]
			} else if m[1][0] == fence[0] && len(m[1]) >= len(fence) && strings.TrimSpace(m[2]) == "" {
				fence = ""
			}
			continue
		}
		if fence != "" || strings.HasPrefix(line, ">") {
			continue
		}
		if envelope || copiedContextEnvelope(line) || contextChatLine.MatchString(line) || internalTagLine.MatchString(line) || contextMetaLine.MatchString(line) {
			return errors.New("消息正文混入上下文前缀或内部标签，未发送；请只保留给对方看的正文，不含时间/会话/发送者包装或思考过程。确需引用原始日志或标签时使用代码块或引用格式")
		}
	}
	return nil
}

func copiedContextEnvelope(text string) bool {
	var row struct {
		Kind     string          `json:"kind"`
		ChatType string          `json:"chat_type"`
		ChatID   json.RawMessage `json:"chat_id"`
		UserID   json.RawMessage `json:"user_id"`
		Text     *string         `json:"text"`
	}
	if json.Unmarshal([]byte(text), &row) != nil {
		return false
	}
	return (row.Kind == "qq_message" || row.Kind == "background_event") &&
		(row.ChatType == "group" || row.ChatType == "private") &&
		len(row.ChatID) > 0 && len(row.UserID) > 0 && row.Text != nil
}
