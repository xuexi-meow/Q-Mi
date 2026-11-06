package bot

import (
	"strings"
	"sync"
)

// Command 定义一个命令:Name 即触发词,业务 handler 在 event.RegisterTyped 里
// 调 MatchCommand 命中后自行执行,配合 Reply 回复。
type Command struct {
	Name     string // 触发词,如 "ping" 或 "状态"
	Prefixed bool   // true=强调以 "/" 调用(裸词也能命中);false=裸词命令
	Admin    bool   // 仅管理员可用(由调用方用 IsAdmin 校验)
}

var (
	commands   = make(map[string]*Command)
	commandsMu sync.RWMutex
)

// RegisterCommand 注册命令,重复注册视为编程错误直接 panic。
func RegisterCommand(cmd Command) {
	if strings.TrimSpace(cmd.Name) == "" {
		panic("命令名不能为空")
	}
	commandsMu.Lock()
	defer commandsMu.Unlock()
	if _, ok := commands[cmd.Name]; ok {
		panic("命令重复注册: " + cmd.Name)
	}
	commands[cmd.Name] = &cmd
}

// MatchCommand 从消息文本中匹配命令,返回命令与剩余参数(原文保留,不做规整)。
// 触发前缀由命令定义:Prefixed=true 只认 "/名字",Prefixed=false 只认裸词。
// 未命中返回 nil, ""。
func MatchCommand(text string) (*Command, string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, ""
	}
	prefixed := strings.HasPrefix(trimmed, "/")
	if prefixed {
		trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, "/"))
		if trimmed == "" {
			return nil, ""
		}
	}
	name, args := trimmed, ""
	if i := strings.IndexAny(name, " \t"); i >= 0 {
		args = strings.TrimSpace(name[i+1:])
		name = name[:i]
	}

	commandsMu.RLock()
	defer commandsMu.RUnlock()
	cmd, ok := commands[name]
	if !ok || cmd.Prefixed != prefixed {
		return nil, ""
	}
	return cmd, args
}
