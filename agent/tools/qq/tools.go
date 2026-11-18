package qq

import (
	"Q-Mi/agent/tool"
	"Q-Mi/agent/workspacepath"
	"Q-Mi/api"
	"encoding/json"
	"fmt"
	"strings"
)

// qqTools admin 板块：QQ 能力工具（群/好友/文件/请求处理）与 napcat 逃生舱。
// ctx.Admin 决定危险工具是否放行；ctx.Workspace 用于本地文件路径映射。
func Tools(ctx *Dependencies) []Tool {
	admin := ctx.Admin
	// requireAdmin 危险工具的统一门槛。
	requireAdmin := func(action string) error {
		return AuthorizeAction(action, admin)
	}

	return []Tool{
		{
			Name:        "recall_message",
			Description: "撤回一条消息（群消息需要自己是管理员或发送者）。传消息的 message_id。",
			Parameters: tool.ObjectParams(map[string]any{
				"message_id": tool.IntProp("要撤回的消息 ID"),
			}, []string{"message_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					MessageID int64 `json:"message_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.MessageID == 0 {
					return "", fmt.Errorf("message_id 不能为 0")
				}
				return emptyOK(ctx.call(&api.DeleteMsg{MessageID: p.MessageID}))
			},
		},
		{
			Name:        "get_friend_list",
			Description: "获取好友列表（user_id/nickname/remark）。",
			Parameters:  tool.ObjectParams(nil, nil),
			Run: func(json.RawMessage) (string, error) {
				data, err := ctx.call(&api.GetFriendList{})
				if err != nil {
					return "", err
				}
				var friends []struct {
					UserID   int64  `json:"user_id"`
					Nickname string `json:"nickname"`
					Remark   string `json:"remark"`
				}
				if err := json.Unmarshal(data, &friends); err != nil {
					return "", fmt.Errorf("解析好友列表失败: %w", err)
				}
				rows := make([]map[string]any, 0, len(friends))
				for _, f := range friends {
					rows = append(rows, map[string]any{
						"user_id": f.UserID, "nickname": f.Nickname, "remark": f.Remark,
					})
				}
				return marshalCompact(rows)
			},
		},
		{
			Name:        "lookup_user",
			Description: "查一个 QQ 的资料（昵称、年龄、等级等）。传对方 QQ 号。",
			Parameters: tool.ObjectParams(map[string]any{
				"qq": tool.IntProp("对方 QQ 号"),
			}, []string{"qq"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					QQ int64 `json:"qq"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.QQ == 0 {
					return "", fmt.Errorf("缺少必填参数 qq（且不能为 0）")
				}
				return passthrough(ctx.call(&api.GetStrangerInfo{UserID: p.QQ}))
			},
		},
		{
			Name:        "lookup_groups",
			Description: "获取自己加入的群列表（group_id/group_name/member_count）。",
			Parameters:  tool.ObjectParams(nil, nil),
			Run: func(json.RawMessage) (string, error) {
				data, err := ctx.call(&api.GetGroupList{})
				if err != nil {
					return "", err
				}
				var groups []struct {
					GroupID     int64  `json:"group_id"`
					GroupName   string `json:"group_name"`
					MemberCount int    `json:"member_count"`
				}
				if err := json.Unmarshal(data, &groups); err != nil {
					return "", fmt.Errorf("解析群列表失败: %w", err)
				}
				rows := make([]map[string]any, 0, len(groups))
				for _, g := range groups {
					rows = append(rows, map[string]any{
						"group_id": g.GroupID, "group_name": g.GroupName, "member_count": g.MemberCount,
					})
				}
				return marshalCompact(rows)
			},
		},
		{
			Name:        "lookup_group",
			Description: "查一个群的详细信息（基本信息 + 扩展信息合并）。",
			Parameters: tool.ObjectParams(map[string]any{
				"group_id": tool.IntProp("群号"),
			}, []string{"group_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID int64 `json:"group_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				info, err := ctx.call(&api.GetGroupInfo{GroupID: p.GroupID})
				if err != nil {
					return "", err
				}
				ex, err := ctx.call(&api.GetGroupInfoEx{GroupID: p.GroupID})
				if err != nil {
					return "", err
				}
				base := map[string]any{}
				if err := json.Unmarshal(info, &base); err != nil {
					return "", fmt.Errorf("解析群信息失败: %w", err)
				}
				extra := map[string]any{}
				if err := json.Unmarshal(ex, &extra); err != nil {
					return "", fmt.Errorf("解析群扩展信息失败: %w", err)
				}
				for k, v := range extra { // ex 补充 info 没有的键，info 优先
					if _, ok := base[k]; !ok {
						base[k] = v
					}
				}
				return marshalCompact(base)
			},
		},
		{
			Name:        "lookup_members",
			Description: "获取群成员列表（user_id/nickname/card/role），最多返回 100 条并注明是否截断。",
			Parameters: tool.ObjectParams(map[string]any{
				"group_id": tool.IntProp("群号"),
			}, []string{"group_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID int64 `json:"group_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				data, err := ctx.call(&api.GetGroupMemberList{GroupID: p.GroupID})
				if err != nil {
					return "", err
				}
				var members []map[string]any
				if err := json.Unmarshal(data, &members); err != nil {
					return "", fmt.Errorf("解析群成员列表失败: %w", err)
				}
				rows := make([]map[string]any, 0, len(members))
				for _, m := range members {
					rows = append(rows, map[string]any{
						"user_id": m["user_id"], "nickname": m["nickname"],
						"card": m["card"], "role": m["role"],
					})
				}
				total := len(rows)
				truncated := total > maxMembersReturned
				if truncated {
					rows = rows[:maxMembersReturned]
				}
				return marshalCompact(map[string]any{
					"total": total, "returned": len(rows), "truncated": truncated, "members": rows,
				})
			},
		},
		{
			Name:        "lookup_member",
			Description: "查群成员的详细资料（名片、角色、等级、入群时间等）。",
			Parameters: tool.ObjectParams(map[string]any{
				"group_id": tool.IntProp("群号"),
				"user_id":  tool.IntProp("成员 QQ 号"),
			}, []string{"group_id", "user_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID int64 `json:"group_id"`
					UserID  int64 `json:"user_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 || p.UserID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id / user_id（且不能为 0）")
				}
				return passthrough(ctx.call(&api.GetGroupMemberInfo{GroupID: p.GroupID, UserID: p.UserID}))
			},
		},
		{
			Name:        "set_group_card",
			Description: "设置群成员的群名片（管理员能力）。",
			Parameters: tool.ObjectParams(map[string]any{
				"group_id": tool.IntProp("群号"),
				"user_id":  tool.IntProp("成员 QQ 号"),
				"card":     tool.StringProp("新群名片（空串表示清除）"),
			}, []string{"group_id", "user_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID int64  `json:"group_id"`
					UserID  int64  `json:"user_id"`
					Card    string `json:"card"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 || p.UserID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id / user_id（且不能为 0）")
				}
				return emptyOK(ctx.call(&api.SetGroupCard{GroupID: p.GroupID, UserID: p.UserID, Card: p.Card}))
			},
		},
		{
			Name:        "set_group_special_title",
			Description: "设置群成员的专属头衔（管理员能力）。",
			Parameters: tool.ObjectParams(map[string]any{
				"group_id": tool.IntProp("群号"),
				"user_id":  tool.IntProp("成员 QQ 号"),
				"title":    tool.StringProp("专属头衔（空串表示清除）"),
			}, []string{"group_id", "user_id", "title"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID int64  `json:"group_id"`
					UserID  int64  `json:"user_id"`
					Title   string `json:"title"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 || p.UserID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id / user_id（且不能为 0）")
				}
				if p.Title == "" {
					return "", fmt.Errorf("缺少必填参数 title（清除头衔传空格等占位即可）")
				}
				return emptyOK(ctx.call(&api.SetGroupSpecialTitle{
					GroupID: p.GroupID, UserID: p.UserID, SpecialTitle: p.Title,
				}))
			},
		},
		{
			Name:        "group_ban",
			Description: "禁言群成员一段时间（管理员能力）。duration 单位秒，最长 30 天。",
			Parameters: tool.ObjectParams(map[string]any{
				"group_id": tool.IntProp("群号"),
				"user_id":  tool.IntProp("成员 QQ 号"),
				"duration": tool.IntProp("禁言时长（秒），0 表示解除禁言，上限 2592000"),
			}, []string{"group_id", "user_id", "duration"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID  int64 `json:"group_id"`
					UserID   int64 `json:"user_id"`
					Duration int64 `json:"duration"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 || p.UserID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id / user_id（且不能为 0）")
				}
				if p.Duration < 0 || p.Duration > maxBanDuration {
					return "", fmt.Errorf("duration 需在 0 ~ %d 秒之间（30 天），got %d", maxBanDuration, p.Duration)
				}
				return emptyOK(ctx.call(&api.SetGroupBan{GroupID: p.GroupID, UserID: p.UserID, Duration: p.Duration}))
			},
		},
		{
			Name:        "group_whole_ban",
			Description: "开启/关闭群全员禁言（管理员能力）。",
			Parameters: tool.ObjectParams(map[string]any{
				"group_id": tool.IntProp("群号"),
				"enable":   map[string]any{"type": "boolean", "description": "true 开启全员禁言，false 关闭"},
			}, []string{"group_id", "enable"}),
			Run: func(args json.RawMessage) (string, error) {
				var raw struct {
					GroupID int64 `json:"group_id"`
					Enable  *bool `json:"enable"`
				}
				if err := decodeArgs(args, &raw); err != nil {
					return "", err
				}
				if raw.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				if raw.Enable == nil {
					return "", fmt.Errorf("缺少必填参数 enable（true/false）")
				}
				return emptyOK(ctx.call(&api.SetGroupWholeBan{GroupID: raw.GroupID, Enable: *raw.Enable}))
			},
		},
		{
			Name:        "kick_member",
			Description: "把成员踢出群聊（管理员能力，危险操作，需要管理员权限）。",
			Parameters: tool.ObjectParams(map[string]any{
				"group_id":           tool.IntProp("群号"),
				"user_id":            tool.IntProp("成员 QQ 号"),
				"reject_add_request": map[string]any{"type": "boolean", "description": "是否拒绝此人再次加群，默认 false"},
			}, []string{"group_id", "user_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if err := requireAdmin("set_group_kick"); err != nil {
					return "", err
				}
				var p struct {
					GroupID          int64 `json:"group_id"`
					UserID           int64 `json:"user_id"`
					RejectAddRequest bool  `json:"reject_add_request"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 || p.UserID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id / user_id（且不能为 0）")
				}
				return emptyOK(ctx.call(&api.SetGroupKick{
					GroupID: p.GroupID, UserID: p.UserID, RejectAddRequest: p.RejectAddRequest,
				}))
			},
		},
		{
			Name:        "set_group_admin",
			Description: "设置/取消群管理员（危险操作，需要管理员权限）。",
			Parameters: tool.ObjectParams(map[string]any{
				"group_id": tool.IntProp("群号"),
				"user_id":  tool.IntProp("成员 QQ 号"),
				"enable":   map[string]any{"type": "boolean", "description": "true 设为管理员，false 取消"},
			}, []string{"group_id", "user_id", "enable"}),
			Run: func(args json.RawMessage) (string, error) {
				if err := requireAdmin("set_group_admin"); err != nil {
					return "", err
				}
				var raw struct {
					GroupID int64 `json:"group_id"`
					UserID  int64 `json:"user_id"`
					Enable  *bool `json:"enable"`
				}
				if err := decodeArgs(args, &raw); err != nil {
					return "", err
				}
				if raw.GroupID == 0 || raw.UserID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id / user_id（且不能为 0）")
				}
				if raw.Enable == nil {
					return "", fmt.Errorf("缺少必填参数 enable（true/false）")
				}
				return emptyOK(ctx.call(&api.SetGroupAdmin{GroupID: raw.GroupID, UserID: raw.UserID, Enable: *raw.Enable}))
			},
		},
		{
			Name:        "set_group_name",
			Description: "修改群名称，需要管理员权限。",
			Parameters: tool.ObjectParams(map[string]any{
				"group_id": tool.IntProp("群号"),
				"name":     tool.StringProp("新群名称"),
			}, []string{"group_id", "name"}),
			Run: func(args json.RawMessage) (string, error) {
				if err := requireAdmin("set_group_name"); err != nil {
					return "", err
				}
				var p struct {
					GroupID int64  `json:"group_id"`
					Name    string `json:"name"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				if p.Name == "" {
					return "", fmt.Errorf("name 不能为空")
				}
				return emptyOK(ctx.call(&api.SetGroupName{GroupID: p.GroupID, GroupName: p.Name}))
			},
		},
		{
			Name:        "leave_group",
			Description: "退出指定群聊（危险操作，需要管理员权限；群主解散群请通过逃生舱 is_dismiss）。",
			Parameters: tool.ObjectParams(map[string]any{
				"group_id": tool.IntProp("要退出的群号"),
			}, []string{"group_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if err := requireAdmin("set_group_leave"); err != nil {
					return "", err
				}
				var p struct {
					GroupID int64 `json:"group_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				return emptyOK(ctx.call(&api.SetGroupLeave{GroupID: p.GroupID}))
			},
		},
		{
			Name:        "send_group_notice",
			Description: "在群里发布一条公告，需要管理员权限。content（文字）与 image（图片 URL/路径）至少提供一个。",
			Parameters: tool.ObjectParams(map[string]any{
				"group_id": tool.IntProp("群号"),
				"content":  tool.StringProp("公告文字内容"),
				"image":    tool.StringProp("可选，公告图片（路径或 URL）"),
			}, []string{"group_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if err := requireAdmin("_send_group_notice"); err != nil {
					return "", err
				}
				var p struct {
					GroupID int64  `json:"group_id"`
					Content string `json:"content"`
					Image   string `json:"image"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				if p.Content == "" && p.Image == "" {
					return "", fmt.Errorf("content 与 image 至少提供一个")
				}
				return emptyOK(ctx.call(&api.SendGroupNotice{
					GroupID: p.GroupID, Content: p.Content, Image: p.Image,
					Pinned: 0, Type: 1, ConfirmRequired: 1, IsShowEditCard: 0, TipWindowType: 0,
				}))
			},
		},
		{
			Name:        "set_essence",
			Description: "把一条群消息设为精华消息。",
			Parameters: tool.ObjectParams(map[string]any{
				"message_id": tool.IntProp("消息 ID"),
			}, []string{"message_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					MessageID int64 `json:"message_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.MessageID == 0 {
					return "", fmt.Errorf("message_id 不能为 0")
				}
				return emptyOK(ctx.call(&api.SetEssenceMsg{MessageID: p.MessageID}))
			},
		},
		{
			Name:        "remove_essence",
			Description: "把一条消息移出群精华消息。",
			Parameters: tool.ObjectParams(map[string]any{
				"message_id": tool.IntProp("消息 ID"),
			}, []string{"message_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					MessageID int64 `json:"message_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.MessageID == 0 {
					return "", fmt.Errorf("message_id 不能为 0")
				}
				return emptyOK(ctx.call(&api.DeleteEssenceMsg{MessageID: p.MessageID}))
			},
		},
		{
			Name:        "poke",
			Description: "戳一戳对方（群聊戳群成员，私聊戳好友）。",
			Parameters: tool.ObjectParams(map[string]any{
				"chat_type": tool.StringProp("会话类型：private / group"),
				"chat_id":   tool.IntProp("群号（group 时）；私聊可传对方 QQ"),
				"user_id":   tool.IntProp("要戳的人的 QQ 号"),
			}, []string{"chat_type", "user_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					ChatType string `json:"chat_type"`
					ChatID   int64  `json:"chat_id"`
					UserID   int64  `json:"user_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.UserID == 0 {
					return "", fmt.Errorf("缺少必填参数 user_id（且不能为 0）")
				}
				switch p.ChatType {
				case "group":
					if p.ChatID == 0 {
						return "", fmt.Errorf("group 戳一戳需要 chat_id（群号）")
					}
					return emptyOK(ctx.call(&api.SendGroupPoke{GroupID: &p.ChatID, UserID: p.UserID}))
				case "private":
					return emptyOK(ctx.call(&api.SendFriendPoke{UserID: p.UserID}))
				default:
					return "", fmt.Errorf("chat_type 必须是 private 或 group, got %q", p.ChatType)
				}
			},
		},
		{
			Name:        "send_like",
			Description: "给指定 QQ 点赞（每天最多 10 个赞）。",
			Parameters: tool.ObjectParams(map[string]any{
				"qq":    tool.IntProp("对方 QQ 号"),
				"times": tool.IntProp("点赞次数，默认 1，最大 10"),
			}, []string{"qq"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					QQ    int64 `json:"qq"`
					Times int   `json:"times"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.QQ == 0 {
					return "", fmt.Errorf("缺少必填参数 qq（且不能为 0）")
				}
				times := p.Times
				if times <= 0 {
					times = 1
				}
				if times > 10 {
					times = 10
				}
				return emptyOK(ctx.call(&api.SendLike{UserID: p.QQ, Times: times}))
			},
		},
		{
			Name:        "handle_friend_request",
			Description: "同意或拒绝加好友请求。flag 从好友申请事件/系统消息里拿到。",
			Parameters: tool.ObjectParams(map[string]any{
				"flag":    tool.StringProp("请求 flag"),
				"approve": map[string]any{"type": "boolean", "description": "是否同意"},
			}, []string{"flag"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Flag    string `json:"flag"`
					Approve bool   `json:"approve"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.Flag == "" {
					return "", fmt.Errorf("缺少必填参数 flag")
				}
				return emptyOK(ctx.call(&api.SetFriendAddRequest{Flag: p.Flag, Approve: p.Approve}))
			},
		},
		{
			Name:        "handle_group_request",
			Description: "同意或拒绝加群请求/邀请。flag 从群系统消息里拿到；sub_type 为 add（申请）或 invite（邀请）。",
			Parameters: tool.ObjectParams(map[string]any{
				"flag":     tool.StringProp("请求 flag"),
				"sub_type": tool.StringProp("add（加群申请）或 invite（邀请）"),
				"approve":  map[string]any{"type": "boolean", "description": "是否同意"},
			}, []string{"flag", "sub_type"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Flag    string `json:"flag"`
					SubType string `json:"sub_type"`
					Approve bool   `json:"approve"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.Flag == "" {
					return "", fmt.Errorf("缺少必填参数 flag")
				}
				if p.SubType != "add" && p.SubType != "invite" {
					return "", fmt.Errorf("sub_type 必须是 add 或 invite, got %q", p.SubType)
				}
				// api.SetGroupAddRequest 缺 sub_type 字段（文档示例要求传），
				// 用本地结构体补齐。
				return emptyOK(ctx.call(&groupAddRequestReq{
					Flag: p.Flag, SubType: p.SubType, Approve: p.Approve,
				}))
			},
		},
		{
			Name:        "get_msg_history",
			Description: "拉取某个会话在 NapCat 侧的历史消息（不受本地记忆库上限约束），返回 messages 数组。",
			Parameters: tool.ObjectParams(map[string]any{
				"chat_type":   tool.StringProp("会话类型：private / group"),
				"chat_id":     tool.IntProp("群号；私聊 = 对方 QQ"),
				"message_seq": tool.IntProp("可选，起始消息序号（翻页用）"),
				"count":       tool.IntProp("可选，条数，默认 20，最大 20"),
			}, []string{"chat_type", "chat_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					ChatType   string `json:"chat_type"`
					ChatID     int64  `json:"chat_id"`
					MessageSeq int64  `json:"message_seq"`
					Count      int    `json:"count"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.ChatID == 0 {
					return "", fmt.Errorf("缺少必填参数 chat_id（且不能为 0）")
				}
				count := p.Count
				if count <= 0 {
					count = maxMsgHistoryCount
				}
				if count > maxMsgHistoryCount {
					count = maxMsgHistoryCount
				}
				switch p.ChatType {
				case "group":
					return passthrough(ctx.call(&api.GetGroupMsgHistory{
						GroupID: p.ChatID, MessageSeq: p.MessageSeq, Count: count,
					}))
				case "private":
					return passthrough(ctx.call(&api.GetFriendMsgHistory{
						UserID: p.ChatID, MessageSeq: p.MessageSeq, Count: count,
					}))
				default:
					return "", fmt.Errorf("chat_type 必须是 private 或 group, got %q", p.ChatType)
				}
			},
		},
		{
			Name:        "get_forward_detail",
			Description: "查看一条合并转发消息的具体内容。forward_id 从消息段/发送结果里拿到。",
			Parameters: tool.ObjectParams(map[string]any{
				"forward_id": tool.StringProp("合并转发消息的 ID"),
			}, []string{"forward_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					ForwardID string `json:"forward_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.ForwardID == "" {
					return "", fmt.Errorf("缺少必填参数 forward_id")
				}
				// 文档：get_forward_msg 收字符串形态的 message_id
				return passthrough(ctx.call(&api.GetForwardMsg{MessageID: p.ForwardID}))
			},
		},
		{
			Name:        "upload_group_file",
			Description: "上传本地文件到群文件系统。file 仅支持本地路径，且文件必须与 NapCat 在同一台机器上（不支持 http/base64）。",
			Parameters: tool.ObjectParams(map[string]any{
				"group_id": tool.IntProp("群号"),
				"file":     tool.StringProp("本地文件绝对路径"),
				"name":     tool.StringProp("上传后显示的文件名"),
			}, []string{"group_id", "file", "name"}),
			Usage:     "上传耗时与文件大小相关，大文件建议 `\"_async\": true` 后台执行：立即返回任务回执，完成后自动送达结果。\n错误形态：file 为 http/base64 链接 → 报错；群文件系统未开启 → NapCat 报错回填。",
			Asyncable: true,
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID int64  `json:"group_id"`
					File    string `json:"file"`
					Name    string `json:"name"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				if p.Name == "" {
					return "", fmt.Errorf("缺少必填参数 name")
				}
				if p.File == "" {
					return "", fmt.Errorf("缺少必填参数 file")
				}
				low := strings.ToLower(p.File)
				if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "base64://") {
					return "", fmt.Errorf("upload_group_file 仅支持本地路径（NapCat 与文件须在同一台机器上），不支持 http/base64: %s", p.File)
				}
				// 工作区相对路径映射为 NapCat(WSL) 侧路径；绝对 Windows 路径
				// 自动映射 /mnt/<盘符>/...，模型不需要知道 WSL 的存在
				file, merr := workspacepath.NapcatPath(ctx.Workspace, p.File)
				if merr != nil {
					return "", merr
				}
				return passthrough(ctx.call(&api.UploadGroupFile{
					GroupID: p.GroupID, File: file, Name: p.Name, UploadFile: true,
				}))
			},
		},
		{
			Name:        "get_group_files",
			Description: "查看群文件列表：不传 folder_id 列根目录，传 folder_id 列该文件夹内容。",
			Parameters: tool.ObjectParams(map[string]any{
				"group_id":  tool.IntProp("群号"),
				"folder_id": tool.StringProp("可选，文件夹 ID（不传则列根目录）"),
			}, []string{"group_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					GroupID  int64  `json:"group_id"`
					FolderID string `json:"folder_id"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.GroupID == 0 {
					return "", fmt.Errorf("缺少必填参数 group_id（且不能为 0）")
				}
				if p.FolderID == "" {
					return passthrough(ctx.call(&api.GetGroupRootFiles{GroupID: p.GroupID}))
				}
				return passthrough(ctx.call(&api.GetGroupFilesByFolder{GroupID: p.GroupID, FolderID: p.FolderID}))
			},
		},
		{
			Name:        "napcat_api",
			Description: "按需调用已开放的 SnowLuma/NapCat 动作。优先已有封装工具；先用 napcat_docs 搜索并查具体参数。权限、参数与发言校验不可绕过。",
			Parameters: tool.ObjectParams(map[string]any{
				"action": tool.StringProp("NapCat 动作名，如 get_group_honor_info（可带 / 前缀）"),
				"params": map[string]any{
					"type":        "object",
					"description": "动作参数对象，键为文档里的参数名；无参动作可省略",
				},
			}, []string{"action"}),
			Usage: "先 `napcat_docs({\"query\":\"群荣誉\"})` 找动作，再 `napcat_docs({\"action\":\"get_group_honor_info\"})` 看结构和示例，最后调用。params 必须符合该动作的 input_schema，键名大小写严格一致；无参可省略。新增动作默认禁用，权限以程序返回为准。结果过长会标记 truncated，改用分页/更小查询或封装工具。",
			Run: func(args json.RawMessage) (string, error) {
				return runNapcatAPI(ctx, args)
			},
		},
		{
			Name:        "napcat_docs",
			Description: "按需查完整 SnowLuma API 目录：query 搜索，category 分类，action 查参数/返回结构/示例/本地权限。空参返回分类和首屏，列表分页。",
			Parameters: tool.ObjectParams(map[string]any{
				"query":    tool.StringProp("可选，中英文动作名或用途关键词，如 qzone、群荣誉、表情"),
				"category": tool.StringProp("可选，空参返回的分类名，如 空间、群管理、消息、群文件"),
				"action":   tool.StringProp("可选，精确动作名；返回详情而非列表，可带 / 前缀"),
				"offset":   tool.IntProp("可选，列表偏移量，默认 0；接上页 next_offset"),
				"limit":    tool.IntProp("可选，每页条数，默认 12，上限 25"),
			}, nil),
			Run: func(args json.RawMessage) (string, error) {
				return napcatDocs(args, admin)
			},
		},
	}
}

// groupAddRequestReq 带 sub_type 的处理加群请求（文档示例要求 sub_type，
// api.SetGroupAddRequest 未含该字段，这里在 agent 侧补齐）。
type groupAddRequestReq struct {
	Flag    string `json:"flag"`
	SubType string `json:"sub_type"`
	Approve bool   `json:"approve"`
	Reason  string `json:"reason,omitempty"`
}

func (r *groupAddRequestReq) Path() string { return "/set_group_add_request" }
