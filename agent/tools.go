// tools.go 定义 P2 工具集（spec §3）与 P3-2 QQ 能力工具、万能逃生舱。
// help 的输出直接从工具定义生成（单一源头）；工具 Run 返回紧凑 JSON 字符串，
// 出错返回 error 由调用方（manager）包装成 {"error": ...} 回填思维链。
// finish 无 Run，仅作为 manager 思考循环的终止标记。
package agent

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"Q-Mi/api"
	apimsg "Q-Mi/api/message"
	"Q-Mi/storage"
	"Q-Mi/utils/request"
)

// Tool 一个可被 LLM 调用的工具。Run 收到原始 JSON 参数，返回紧凑 JSON
// 字符串；Run 为 nil 表示纯标记工具（finish），由循环特殊处理、不执行。
type Tool struct {
	Name        string
	Description string
	Parameters  any
	Run         func(args json.RawMessage) (string, error)
}

// toolDefs 把工具集转成 OpenAI tools 请求参数。
func toolDefs(tools []Tool) []ToolDef {
	defs := make([]ToolDef, 0, len(tools))
	for _, t := range tools {
		defs = append(defs, ToolDef{
			Type: "function",
			Function: ToolSchema{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.Parameters,
			},
		})
	}
	return defs
}

// objectParams 生成 {"type":"object","properties":...} 形态的参数 schema。
// required 为空时省略（部分 OpenAI 兼容提供商拒绝 "required": null），
// properties 为空时给空对象（同样不能是 null）。
func objectParams(properties map[string]any, required []string) map[string]any {
	if properties == nil {
		properties = map[string]any{}
	}
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// strProp / intProp 属性快捷构造。
func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

// newToolSet 构造全部工具。notesPath/statePath 由 manager 注入（默认
// agent/notes.json、agent/state.json，测试注入临时目录）；store 为 nil 时
// 查询类工具返回 error。isAdmin 由 manager 按 turn 发送者是否管理员判定，
// 决定危险工具（踢人/退群/设管理员/逃生舱危险动作）是否放行。
func newToolSet(store *storage.Store, notesPath, statePath string, isAdmin bool) []Tool {
	tools := []Tool{
		{
			Name:        "get_recent_messages",
			Description: "查询最近的聊天记录（跨会话混合，按时间正序返回 JSON 数组）。",
			Parameters: objectParams(map[string]any{
				"chat_type": strProp("可选，按会话类型过滤：private / group"),
				"chat_id":   intProp("可选，按会话 ID 过滤：群号；私聊 = 对方 QQ"),
				"limit":     intProp("可选，返回条数，默认 50，最大 100"),
			}, nil),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					ChatType string `json:"chat_type"`
					ChatID   int64  `json:"chat_id"`
					Limit    int    `json:"limit"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				limit := p.Limit
				if limit <= 0 {
					limit = 50
				}
				if limit > 100 {
					limit = 100
				}
				return queryMessageRows(store, storage.MessageFilter{
					ChatType: p.ChatType,
					ChatID:   p.ChatID,
					Limit:    limit,
				})
			},
		},
		{
			Name:        "search_messages",
			Description: "按关键词全文搜索历史聊天记录（按时间正序返回 JSON 数组）。",
			Parameters: objectParams(map[string]any{
				"query":     strProp("搜索关键词"),
				"chat_type": strProp("可选，按会话类型过滤：private / group"),
				"chat_id":   intProp("可选，按会话 ID 过滤：群号；私聊 = 对方 QQ"),
				"limit":     intProp("可选，返回条数，默认 20，最大 50"),
			}, []string{"query"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Query    string `json:"query"`
					ChatType string `json:"chat_type"`
					ChatID   int64  `json:"chat_id"`
					Limit    int    `json:"limit"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.Query == "" {
					return "", fmt.Errorf("query 不能为空")
				}
				limit := p.Limit
				if limit <= 0 {
					limit = 20
				}
				if limit > 50 {
					limit = 50
				}
				return queryMessageRows(store, storage.MessageFilter{
					ChatType: p.ChatType,
					ChatID:   p.ChatID,
					Limit:    limit,
				}, p.Query)
			},
		},
		{
			Name:        "send_message",
			Description: "给指定会话发送一条消息：text 纯文本，或 segments 富消息段数组（at/reply/face/dice/rps/json/image），两者二选一。一个思考轮里可多次调用实现分条发送；不调用则不回复。",
			Parameters: objectParams(map[string]any{
				"chat_type": strProp("会话类型：private / group"),
				"chat_id":   intProp("会话 ID：群号；私聊 = 对方 QQ"),
				"text":      strProp("要发送的纯文本内容（与 segments 二选一）"),
				"segments": map[string]any{
					"type":        "array",
					"description": "富消息段数组（与 text 二选一），按顺序发送。段类型白名单：text/at/reply/face/dice/rps/json/image",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"type": strProp("段类型：text / at / reply / face / dice / rps / json / image"),
							"data": map[string]any{
								"type":        "object",
								"description": "段参数：text{text} / at{qq} / reply{id} / face{id} / dice{result} / rps{result} / json{data} / image{file, summary?}；image 的 file 只支持 http/https/base64:// 图片链接",
							},
						},
						"required": []string{"type", "data"},
					},
				},
			}, []string{"chat_type", "chat_id"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					ChatType string         `json:"chat_type"`
					ChatID   int64          `json:"chat_id"`
					Text     string         `json:"text"`
					Segments []segmentParam `json:"segments"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.ChatType != "private" && p.ChatType != "group" {
					return "", fmt.Errorf("chat_type 必须是 private 或 group, got %q", p.ChatType)
				}
				if p.ChatID == 0 {
					return "", fmt.Errorf("chat_id 不能为 0")
				}
				var segs []*apimsg.Message
				switch {
				case p.Text != "" && len(p.Segments) > 0:
					return "", fmt.Errorf("text 与 segments 只能二选一，避免歧义")
				case p.Text != "":
					if n := len([]rune(p.Text)); n > maxSendMessageTextRunes {
						return "", fmt.Errorf("text 内容 %d 字符，超过单次上限 %d", n, maxSendMessageTextRunes)
					}
					segs = []*apimsg.Message{apimsg.Text(p.Text)}
				case len(p.Segments) > 0:
					var err error
					if segs, err = buildSegments(p.Segments); err != nil {
						return "", err
					}
				default:
					return "", fmt.Errorf("text 与 segments 必须提供一个")
				}
				return sendSegments(p.ChatType, p.ChatID, segs)
			},
		},
		{
			Name:        "think",
			Description: "记一条思考笔记，持久化后下一轮对话会注入上下文，用于跨轮次保持思路连贯。",
			Parameters: objectParams(map[string]any{
				"note": strProp("笔记内容"),
			}, []string{"note"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Note string `json:"note"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.Note == "" {
					return "", fmt.Errorf("note 不能为空")
				}
				if err := AppendNote(notesPath, p.Note); err != nil {
					return "", fmt.Errorf("写思考笔记失败: %w", err)
				}
				return "{}", nil
			},
		},
		{
			Name:        "update_mood",
			Description: "更新自己的当前情绪状态，会注入到之后的 system 提示中。",
			Parameters: objectParams(map[string]any{
				"mood": strProp("新的情绪状态，如 开心 / 无聊 / 有点生气"),
			}, []string{"mood"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Mood string `json:"mood"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if p.Mood == "" {
					return "", fmt.Errorf("mood 不能为空")
				}
				return "{}", writeState(statePath, p.Mood)
			},
		},
		{
			Name:        "finish",
			Description: "结束本轮思考。可以发了消息再结束，也可以什么都不发直接结束；调用后本轮终止。",
			Parameters:  objectParams(nil, nil),
		},
	}
	// P3-2 QQ 能力工具与逃生舱，插在 help 之前（help 捕获完整工具集）。
	tools = append(tools, qqTools(isAdmin)...)

	// help 的 Run 捕获完整工具集（含自身，单一源头）。
	help := Tool{
		Name:        "help",
		Description: "返回全部工具的名称、描述与参数用法说明（JSON 数组）。不确定其他工具怎么用时先调用它。",
		Parameters:  objectParams(nil, nil),
		Run: func(json.RawMessage) (string, error) {
			return marshalCompact(helpPayload(tools))
		},
	}
	tools = append(tools, help)
	return tools
}

// helpPayload 从工具定义生成 help 输出（单一源头，无独立文档）。
func helpPayload(tools []Tool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"parameters":  t.Parameters,
		})
	}
	return out
}

// decodeArgs 解析工具参数；空参数视为无参。
func decodeArgs(args json.RawMessage, v any) error {
	if len(args) == 0 {
		return nil
	}
	if err := json.Unmarshal(args, v); err != nil {
		return fmt.Errorf("工具参数不是合法 JSON: %w", err)
	}
	return nil
}

// marshalCompact 序列化为紧凑 JSON（map 键自动排序，无缩进）。
func marshalCompact(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("序列化工具结果失败: %w", err)
	}
	return string(data), nil
}

// queryMessageRows 查询（可选 FTS 关键词）并把结果序列化为与上下文行
// 同构的紧凑 JSON 数组（旧→新）：message_id/chat_type/chat_id/sender_name/text/time。
func queryMessageRows(store *storage.Store, f storage.MessageFilter, keyword ...string) (string, error) {
	if store == nil {
		return "", fmt.Errorf("记忆库未启用，无法查询消息")
	}
	var (
		records []*storage.MessageRecord
		err     error
	)
	if len(keyword) > 0 {
		records, err = store.SearchMessages(keyword[0], f)
	} else {
		records, err = store.QueryMessages(f)
	}
	if err != nil {
		return "", fmt.Errorf("查询消息失败: %w", err)
	}
	rows := make([]map[string]any, 0, len(records))
	for i := len(records) - 1; i >= 0; i-- { // 倒序 → 正序（旧→新）
		r := records[i]
		rows = append(rows, map[string]any{
			"message_id":  r.MessageID,
			"chat_type":   r.ChatType,
			"chat_id":     r.ChatID,
			"sender_name": r.SenderName,
			"text":        r.RawMessage,
			"time":        time.Unix(int64(r.Time), 0).Format(timeLayout),
		})
	}
	return marshalCompact(rows)
}

// maxSendMessageTextRunes send_message 单次调用 text 内容的累计上限
// （rune 计），纯 text 与 segments 里的 text 段共用该额度。
const maxSendMessageTextRunes = 8000

// segmentParam send_message segments 数组里的单个消息段参数。
type segmentParam struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
}

// buildSegments 把 segments 参数转成消息段数组（P2.6）。只接受白名单类型，
// 白名单外报错；text 段内容累计超过 maxSendMessageTextRunes 报错；
// image 的 file 原样透传（http/https/base64:// 由 NapCat 处理，
// 本地路径不该出现，同样透传让 NapCat 报出明确错误）。
func buildSegments(segs []segmentParam) ([]*apimsg.Message, error) {
	out := make([]*apimsg.Message, 0, len(segs))
	textLen := 0
	for i, s := range segs {
		at := i + 1
		switch s.Type {
		case "text":
			v, err := segString(s.Data, "text", at)
			if err != nil {
				return nil, err
			}
			if v == "" {
				return nil, fmt.Errorf("第 %d 段 text 不能为空", at)
			}
			textLen += len([]rune(v))
			if textLen > maxSendMessageTextRunes {
				return nil, fmt.Errorf("text 内容累计 %d 字符，超过单次上限 %d", textLen, maxSendMessageTextRunes)
			}
			out = append(out, apimsg.Text(v))
		case "at":
			v, err := segString(s.Data, "qq", at)
			if err != nil {
				return nil, err
			}
			out = append(out, apimsg.At(v))
		case "reply":
			v, err := segString(s.Data, "id", at)
			if err != nil {
				return nil, err
			}
			out = append(out, apimsg.Reply(v))
		case "face":
			v, err := segString(s.Data, "id", at)
			if err != nil {
				return nil, err
			}
			out = append(out, apimsg.Face(v))
		case "dice":
			v, err := segString(s.Data, "result", at)
			if err != nil {
				return nil, err
			}
			out = append(out, apimsg.Dice(v))
		case "rps":
			v, err := segString(s.Data, "result", at)
			if err != nil {
				return nil, err
			}
			out = append(out, apimsg.RPS(v))
		case "json":
			data, ok := s.Data["data"]
			if !ok {
				return nil, fmt.Errorf("第 %d 段 json 缺少 %q 字段", at, "data")
			}
			out = append(out, apimsg.Json(data))
		case "image":
			file, err := segString(s.Data, "file", at)
			if err != nil {
				return nil, err
			}
			var summary *string
			if raw, ok := s.Data["summary"]; ok {
				str, ok := raw.(string)
				if !ok {
					return nil, fmt.Errorf("第 %d 段 image 的 %q 应为字符串", at, "summary")
				}
				summary = &str
			}
			out = append(out, apimsg.Image(file, summary, nil))
		default:
			return nil, fmt.Errorf("第 %d 段不支持的消息段类型 %q，可用：text/at/reply/face/dice/rps/json/image", at, s.Type)
		}
	}
	return out, nil
}

// segString 取消息段 data 里的必填字符串字段，缺失或类型不符报错。
func segString(data map[string]any, key string, at int) (string, error) {
	raw, ok := data[key]
	if !ok {
		return "", fmt.Errorf("第 %d 段缺少 %q 字段", at, key)
	}
	s, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("第 %d 段的 %q 应为字符串", at, key)
	}
	return s, nil
}

// sendSegments 经 NapCat 发送消息段链并返回 {"message_id":N}
// （NapCat 调用是短请求，用独立 Background ctx，打断逻辑在 P2 后续处理）。
func sendSegments(chatType string, chatID int64, segs []*apimsg.Message) (string, error) {
	ctx := context.Background()
	var (
		resp *api.Response[api.SendMsgData]
		err  error
	)
	if chatType == "group" {
		resp, err = request.SendGroupMsg(ctx, chatID, segs...)
	} else {
		resp, err = request.SendPrivateMsg(ctx, chatID, segs...)
	}
	if err != nil {
		return "", err
	}
	return marshalCompact(map[string]any{"message_id": resp.Data.MessageID})
}

// agentState state.json 的内容结构。
type agentState struct {
	Mood    string    `json:"mood"`
	Updated time.Time `json:"updated"`
}

// readStateFrom 读取状态文件；文件不存在返回零值状态。
func readStateFrom(path string) (agentState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return agentState{}, nil
		}
		return agentState{}, err
	}
	var s agentState
	if err := json.Unmarshal(data, &s); err != nil {
		return agentState{}, err
	}
	return s, nil
}

// writeState 写入情绪状态；mood 与现值相同则不写盘（避免无谓的 mtime 抖动）。
func writeState(path, mood string) error {
	cur, err := readStateFrom(path)
	if err != nil {
		return fmt.Errorf("读取状态文件失败: %w", err)
	}
	if cur.Mood == mood {
		return nil
	}
	data, err := json.Marshal(agentState{Mood: mood, Updated: time.Now()})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// ================= P3-2：QQ 能力工具与万能逃生舱 =================

// errAdminRequired 非管理员调用危险操作的错误文案（回填思维链，模型能看懂）。
var errAdminRequired = errors.New("此操作需要管理员权限")

// dangerousNapcatActions napcat_api 逃生舱中需要管理员权限的动作集合。
var dangerousNapcatActions = map[string]bool{
	"set_group_kick":  true,
	"set_group_leave": true,
	"delete_friend":   true,
	"set_qq_avatar":   true,
	"set_qq_profile":  true,
	"set_group_name":  true,
}

// maxBanDuration 单次禁言时长上限：30 天（秒）。
const maxBanDuration = 2592000

// maxMembersReturned lookup_members 单次返回的成员条数上限（防撑爆上下文）。
const maxMembersReturned = 100

// maxMsgHistoryCount get_msg_history 单次返回条数上限。
const maxMsgHistoryCount = 20

// callNapCat 用独立 ctx 调一次 NapCat API 并返回 data（RawMessage 原样透传）。
// 出错时返回 request 层的错误（含 retcode/wording，模型可据此调整）。
func callNapCat(req api.Request) (json.RawMessage, error) {
	resp, err := request.Call(context.Background(), req)
	if err != nil {
		return nil, err
	}
	return resp.Data, nil
}

// qqTools 构造 P3-2 的 QQ 能力工具（26 个人性化封装 + 逃生舱 2 个）。
// isAdmin 决定危险工具是否放行。
func qqTools(isAdmin bool) []Tool {
	// requireAdmin 危险工具的统一门槛。
	requireAdmin := func() error {
		if !isAdmin {
			return errAdminRequired
		}
		return nil
	}
	// emptyOK 调用成功即返回 "{}"。
	emptyOK := func(_ json.RawMessage, err error) (string, error) {
		if err != nil {
			return "", err
		}
		return "{}", nil
	}
	// passthrough data 原样返回（NapCat 返回的本身就是紧凑 JSON）。
	passthrough := func(data json.RawMessage, err error) (string, error) {
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	return []Tool{
		{
			Name:        "recall_message",
			Description: "撤回一条消息（群消息需要自己是管理员或发送者）。传消息的 message_id。",
			Parameters: objectParams(map[string]any{
				"message_id": intProp("要撤回的消息 ID"),
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
				return emptyOK(callNapCat(&api.DeleteMsg{MessageID: p.MessageID}))
			},
		},
		{
			Name:        "get_friend_list",
			Description: "获取好友列表（user_id/nickname/remark）。",
			Parameters:  objectParams(nil, nil),
			Run: func(json.RawMessage) (string, error) {
				data, err := callNapCat(&api.GetFriendList{})
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
			Parameters: objectParams(map[string]any{
				"qq": intProp("对方 QQ 号"),
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
				return passthrough(callNapCat(&api.GetStrangerInfo{UserID: p.QQ}))
			},
		},
		{
			Name:        "lookup_groups",
			Description: "获取自己加入的群列表（group_id/group_name/member_count）。",
			Parameters:  objectParams(nil, nil),
			Run: func(json.RawMessage) (string, error) {
				data, err := callNapCat(&api.GetGroupList{})
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
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
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
				info, err := callNapCat(&api.GetGroupInfo{GroupID: p.GroupID})
				if err != nil {
					return "", err
				}
				ex, err := callNapCat(&api.GetGroupInfoEx{GroupID: p.GroupID})
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
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
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
				data, err := callNapCat(&api.GetGroupMemberList{GroupID: p.GroupID})
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
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"user_id":  intProp("成员 QQ 号"),
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
				return passthrough(callNapCat(&api.GetGroupMemberInfo{GroupID: p.GroupID, UserID: p.UserID}))
			},
		},
		{
			Name:        "set_group_card",
			Description: "设置群成员的群名片（管理员能力）。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"user_id":  intProp("成员 QQ 号"),
				"card":     strProp("新群名片（空串表示清除）"),
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
				return emptyOK(callNapCat(&api.SetGroupCard{GroupID: p.GroupID, UserID: p.UserID, Card: p.Card}))
			},
		},
		{
			Name:        "set_group_special_title",
			Description: "设置群成员的专属头衔（管理员能力）。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"user_id":  intProp("成员 QQ 号"),
				"title":    strProp("专属头衔（空串表示清除）"),
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
				return emptyOK(callNapCat(&api.SetGroupSpecialTitle{
					GroupID: p.GroupID, UserID: p.UserID, SpecialTitle: p.Title,
				}))
			},
		},
		{
			Name:        "group_ban",
			Description: "禁言群成员一段时间（管理员能力）。duration 单位秒，最长 30 天。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"user_id":  intProp("成员 QQ 号"),
				"duration": intProp("禁言时长（秒），0 表示解除禁言，上限 2592000"),
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
				return emptyOK(callNapCat(&api.SetGroupBan{GroupID: p.GroupID, UserID: p.UserID, Duration: p.Duration}))
			},
		},
		{
			Name:        "group_whole_ban",
			Description: "开启/关闭群全员禁言（管理员能力）。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
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
				return emptyOK(callNapCat(&api.SetGroupWholeBan{GroupID: raw.GroupID, Enable: *raw.Enable}))
			},
		},
		{
			Name:        "kick_member",
			Description: "把成员踢出群聊（管理员能力，危险操作，需要管理员权限）。",
			Parameters: objectParams(map[string]any{
				"group_id":           intProp("群号"),
				"user_id":            intProp("成员 QQ 号"),
				"reject_add_request": map[string]any{"type": "boolean", "description": "是否拒绝此人再次加群，默认 false"},
			}, []string{"group_id", "user_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if err := requireAdmin(); err != nil {
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
				return emptyOK(callNapCat(&api.SetGroupKick{
					GroupID: p.GroupID, UserID: p.UserID, RejectAddRequest: p.RejectAddRequest,
				}))
			},
		},
		{
			Name:        "set_group_admin",
			Description: "设置/取消群管理员（危险操作，需要管理员权限）。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"user_id":  intProp("成员 QQ 号"),
				"enable":   map[string]any{"type": "boolean", "description": "true 设为管理员，false 取消"},
			}, []string{"group_id", "user_id", "enable"}),
			Run: func(args json.RawMessage) (string, error) {
				if err := requireAdmin(); err != nil {
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
				return emptyOK(callNapCat(&api.SetGroupAdmin{GroupID: raw.GroupID, UserID: raw.UserID, Enable: *raw.Enable}))
			},
		},
		{
			Name:        "set_group_name",
			Description: "修改群名称。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"name":     strProp("新群名称"),
			}, []string{"group_id", "name"}),
			Run: func(args json.RawMessage) (string, error) {
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
				return emptyOK(callNapCat(&api.SetGroupName{GroupID: p.GroupID, GroupName: p.Name}))
			},
		},
		{
			Name:        "leave_group",
			Description: "退出指定群聊（危险操作，需要管理员权限；群主解散群请通过逃生舱 is_dismiss）。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("要退出的群号"),
			}, []string{"group_id"}),
			Run: func(args json.RawMessage) (string, error) {
				if err := requireAdmin(); err != nil {
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
				return emptyOK(callNapCat(&api.SetGroupLeave{GroupID: p.GroupID}))
			},
		},
		{
			Name:        "send_group_notice",
			Description: "在群里发布一条公告。content（文字）与 image（图片 URL/路径）至少提供一个。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"content":  strProp("公告文字内容"),
				"image":    strProp("可选，公告图片（路径或 URL）"),
			}, []string{"group_id"}),
			Run: func(args json.RawMessage) (string, error) {
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
				return emptyOK(callNapCat(&api.SendGroupNotice{
					GroupID: p.GroupID, Content: p.Content, Image: p.Image,
					Pinned: 0, Type: 1, ConfirmRequired: 1, IsShowEditCard: 0, TipWindowType: 0,
				}))
			},
		},
		{
			Name:        "set_essence",
			Description: "把一条群消息设为精华消息。",
			Parameters: objectParams(map[string]any{
				"message_id": intProp("消息 ID"),
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
				return emptyOK(callNapCat(&api.SetEssenceMsg{MessageID: p.MessageID}))
			},
		},
		{
			Name:        "remove_essence",
			Description: "把一条消息移出群精华消息。",
			Parameters: objectParams(map[string]any{
				"message_id": intProp("消息 ID"),
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
				return emptyOK(callNapCat(&api.DeleteEssenceMsg{MessageID: p.MessageID}))
			},
		},
		{
			Name:        "poke",
			Description: "戳一戳对方（群聊戳群成员，私聊戳好友）。",
			Parameters: objectParams(map[string]any{
				"chat_type": strProp("会话类型：private / group"),
				"chat_id":   intProp("群号（group 时）；私聊可传对方 QQ"),
				"user_id":   intProp("要戳的人的 QQ 号"),
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
					return emptyOK(callNapCat(&api.SendGroupPoke{GroupID: &p.ChatID, UserID: p.UserID}))
				case "private":
					return emptyOK(callNapCat(&api.SendFriendPoke{UserID: p.UserID}))
				default:
					return "", fmt.Errorf("chat_type 必须是 private 或 group, got %q", p.ChatType)
				}
			},
		},
		{
			Name:        "send_like",
			Description: "给指定 QQ 点赞（每天最多 10 个赞）。",
			Parameters: objectParams(map[string]any{
				"qq":    intProp("对方 QQ 号"),
				"times": intProp("点赞次数，默认 1，最大 10"),
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
				return emptyOK(callNapCat(&api.SendLike{UserID: p.QQ, Times: times}))
			},
		},
		{
			Name:        "handle_friend_request",
			Description: "同意或拒绝加好友请求。flag 从好友申请事件/系统消息里拿到。",
			Parameters: objectParams(map[string]any{
				"flag":    strProp("请求 flag"),
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
				return emptyOK(callNapCat(&api.SetFriendAddRequest{Flag: p.Flag, Approve: p.Approve}))
			},
		},
		{
			Name:        "handle_group_request",
			Description: "同意或拒绝加群请求/邀请。flag 从群系统消息里拿到；sub_type 为 add（申请）或 invite（邀请）。",
			Parameters: objectParams(map[string]any{
				"flag":     strProp("请求 flag"),
				"sub_type": strProp("add（加群申请）或 invite（邀请）"),
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
				return emptyOK(callNapCat(&groupAddRequestReq{
					Flag: p.Flag, SubType: p.SubType, Approve: p.Approve,
				}))
			},
		},
		{
			Name:        "get_msg_history",
			Description: "拉取某个会话在 NapCat 侧的历史消息（不受本地记忆库上限约束），返回 messages 数组。",
			Parameters: objectParams(map[string]any{
				"chat_type":    strProp("会话类型：private / group"),
				"chat_id":      intProp("群号；私聊 = 对方 QQ"),
				"message_seq":  intProp("可选，起始消息序号（翻页用）"),
				"count":        intProp("可选，条数，默认 20，最大 20"),
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
					return passthrough(callNapCat(&api.GetGroupMsgHistory{
						GroupID: p.ChatID, MessageSeq: p.MessageSeq, Count: count,
					}))
				case "private":
					return passthrough(callNapCat(&api.GetFriendMsgHistory{
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
			Parameters: objectParams(map[string]any{
				"forward_id": strProp("合并转发消息的 ID"),
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
				return passthrough(callNapCat(&api.GetForwardMsg{MessageID: p.ForwardID}))
			},
		},
		{
			Name:        "upload_group_file",
			Description: "上传本地文件到群文件系统。file 仅支持本地路径，且文件必须与 NapCat 在同一台机器上（不支持 http/base64）。",
			Parameters: objectParams(map[string]any{
				"group_id": intProp("群号"),
				"file":     strProp("本地文件绝对路径"),
				"name":     strProp("上传后显示的文件名"),
			}, []string{"group_id", "file", "name"}),
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
				low := strings.ToLower(p.File)
				if strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "base64://") {
					return "", fmt.Errorf("upload_group_file 仅支持本地路径（NapCat 与文件须在同一台机器上），不支持 http/base64: %s", p.File)
				}
				if p.File == "" {
					return "", fmt.Errorf("缺少必填参数 file")
				}
				return passthrough(callNapCat(&api.UploadGroupFile{
					GroupID: p.GroupID, File: p.File, Name: p.Name, UploadFile: true,
				}))
			},
		},
		{
			Name:        "get_group_files",
			Description: "查看群文件列表：不传 folder_id 列根目录，传 folder_id 列该文件夹内容。",
			Parameters: objectParams(map[string]any{
				"group_id":  intProp("群号"),
				"folder_id": strProp("可选，文件夹 ID（不传则列根目录）"),
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
					return passthrough(callNapCat(&api.GetGroupRootFiles{GroupID: p.GroupID}))
				}
				return passthrough(callNapCat(&api.GetGroupFilesByFolder{GroupID: p.GroupID, FolderID: p.FolderID}))
			},
		},
		{
			Name:        "napcat_api",
			Description: "万能逃生舱：调用任意 NapCat API 动作。action 为动作名（可带 / 前缀），params 为参数对象。先用 napcat_docs 查常用动作；未封装的动作参数细节见 apifox 文档。部分危险动作需要管理员权限。",
			Parameters: objectParams(map[string]any{
				"action": strProp("NapCat 动作名，如 get_group_honor_info（可带 / 前缀）"),
				"params": map[string]any{
					"type":        "object",
					"description": "动作参数对象，键为文档里的参数名；无参动作可省略",
				},
			}, []string{"action"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Action string          `json:"action"`
					Params json.RawMessage `json:"params"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				name := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(p.Action), "/"))
				if name == "" {
					return "", fmt.Errorf("缺少必填参数 action")
				}
				typ, ok := actionRegistry[name]
				if !ok {
					names := make([]string, 0, len(actionRegistry))
					for n := range actionRegistry {
						names = append(names, n)
					}
					sort.Strings(names)
					return "", fmt.Errorf("未知动作 %q，可用动作：%s", p.Action, strings.Join(names, ", "))
				}
				if !isAdmin && dangerousNapcatActions[name] {
					return "", errAdminRequired
				}
				reqVal := reflect.New(typ) // *T，T 为该动作的请求结构体
				if len(p.Params) > 0 {
					if err := json.Unmarshal(p.Params, reqVal.Interface()); err != nil {
						return "", fmt.Errorf("params 解析失败（应为参数对象）: %w", err)
					}
				}
				return passthrough(callNapCat(reqVal.Interface().(api.Request)))
			},
		},
		{
			Name:        "napcat_docs",
			Description: "返回常用 NapCat 动作速查（动作名 + 用途 + 关键参数）。配合 napcat_api 使用；全部动作见 apifox 文档。",
			Parameters:  objectParams(nil, nil),
			Run: func(json.RawMessage) (string, error) {
				return strings.TrimSpace(napcatActionsJSON), nil
			},
		},
	}
}

// groupAddRequestReq 带 sub_type 的处理加群请求（文档示例要求 sub_type，
// api.SetGroupAddRequest 未含该字段，这里在 agent 侧补齐）。
type groupAddRequestReq struct {
	Flag    string `json:"flag"`
	SubType string `json:"sub_type"`
	Approve bool   `json:"approve,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

func (r *groupAddRequestReq) Path() string { return "/set_group_add_request" }

// actionTypes 全量 NapCat 动作请求类型（与 api 包 196 个动作一一对应：
// actions_test.go 登记的 192 个 + api.go 定义的 4 个）。
func actionTypes() []api.Request {
	return []api.Request{
		// api.go 已有
		&api.SendGroupMsg{}, &api.SendPrivateMsg{}, &api.GetMsg{}, &api.DeleteMsg{},

		// ---------------- 核心接口 ----------------
		&api.SetGroupTodo{}, &api.CompleteGroupTodo{}, &api.CancelGroupTodo{},
		&api.SendGroupPoke{}, &api.SendFriendPoke{}, &api.SendPoke{},

		// ---------------- 群组接口 ----------------
		&api.GetGroupDetailInfo{}, &api.GetGroupList{}, &api.GetGroupInfo{},
		&api.GetGroupMemberList{}, &api.GetGroupMemberInfo{},
		&api.SetGroupAddRequest{}, &api.SetGroupLeave{}, &api.SetGroupWholeBan{},
		&api.SetGroupBan{}, &api.SetGroupKick{}, &api.SetGroupAdmin{},
		&api.SetGroupName{}, &api.SetGroupCard{}, &api.GetEssenceMsgList{},
		&api.GetGroupIgnoredNotifies{}, &api.DeleteEssenceMsg{}, &api.GetGroupNotice{},
		&api.SetEssenceMsg{}, &api.DeleteGroupNotice{}, &api.GetGroupShutList{},
		&api.GetGroupIgnoreAddRequest{}, &api.SetGroupMemberInvitePolicy{},
		&api.SetGroupMemberPermissions{}, &api.SetGroupNewMemberHistoryVisibility{},
		&api.GetGroupHonorInfo{}, &api.GetGroupAtAllRemain{}, &api.SetGroupPortrait{},

		// ---------------- 群组扩展 ----------------
		&api.SetGroupAddOption{}, &api.SetGroupRobotAddOption{}, &api.SetGroupSearch{},
		&api.SetGroupRemark{}, &api.GetGroupInfoEx{}, &api.SetGroupSign{}, &api.SendGroupSign{},
		&api.GetGroupSignedList{}, &api.GetGroupShareLink{},

		// ---------------- 好友/用户 ----------------
		&api.SetFriendRemark{}, &api.SendLike{}, &api.GetFriendList{},
		&api.SetFriendAddRequest{}, &api.GetCookies{}, &api.GetRecentContact{},
		&api.GetFriendsWithCategory{}, &api.GetProfileLike{}, &api.SetDiyOnlineStatus{},
		&api.GetUnidirectionalFriendList{}, &api.DeleteFriend{},

		// ---------------- 消息接口 ----------------
		&api.SendMsg{}, &api.ForwardFriendSingleMsg{}, &api.ForwardGroupSingleMsg{},
		&api.MarkGroupMsgAsRead{}, &api.MarkPrivateMsgAsRead{}, &api.MarkMsgAsRead{},
		&api.MarkAllAsRead{},
		&api.SendForwardMsg{}, &api.SendGroupForwardMsg{}, &api.SendPrivateForwardMsg{},
		&api.GetForwardMsg{}, &api.GetGroupMsgHistory{}, &api.GetFriendMsgHistory{},

		// ---------------- 消息扩展 ----------------
		&api.FetchEmojiLike{}, &api.GetEmojiLikes{}, &api.FetchPttText{},
		&api.ArkShareGroup{}, &api.ArkSharePeer{}, &api.SendGroupArkShare{}, &api.SendArkShare{},
		&api.SetMsgEmojiLike{}, &api.ClickInlineKeyboardButton{},

		// ---------------- 文件接口 ----------------
		&api.GetFile{}, &api.GetImage{}, &api.GetRecord{},
		&api.GetGroupFileURL{}, &api.GetPrivateFileURL{},

		// ---------------- 文件扩展 ----------------
		&api.MoveGroupFile{}, &api.RenameGroupFile{}, &api.TransGroupFile{},
		&api.CreateFlashTask{}, &api.GetFlashFileList{}, &api.GetFlashFileURL{},
		&api.SendFlashMsg{}, &api.GetShareLink{}, &api.GetFilesetInfo{},
		&api.GetOnlineFileMsg{}, &api.SendOnlineFile{}, &api.SendOnlineFolder{},
		&api.ReceiveOnlineFile{}, &api.RefuseOnlineFile{}, &api.CancelOnlineFile{},
		&api.DownloadFileset{}, &api.GetFilesetID{},

		// ---------------- Go-CQHTTP 文件 ----------------
		&api.GetGroupRootFiles{}, &api.UploadGroupFile{}, &api.DeleteGroupFile{},
		&api.CreateGroupFileFolder{}, &api.DeleteGroupFolder{}, &api.GetGroupFileSystemInfo{},
		&api.GetGroupFilesByFolder{}, &api.DownloadFile{}, &api.UploadPrivateFile{},

		// ---------------- 系统接口 ----------------
		&api.GetLoginInfo{}, &api.GetVersionInfo{}, &api.CanSendRecord{}, &api.CanSendImage{},
		&api.GetStatus{}, &api.GetCsrfToken{}, &api.GetCredentials{}, &api.NcGetPacketStatus{},
		&api.SetRestart{}, &api.GetGroupSystemMsg{}, &api.CleanCache{},
		&api.SetDoubtFriendsAddRequest{}, &api.GetDoubtFriendsAddRequest{},

		// ---------------- 系统扩展 ----------------
		&api.GetRkey{}, &api.GetRkeyServer{}, &api.SetOnlineStatus{}, &api.GetRobotUinRange{},
		&api.FetchCustomFace{}, &api.FetchCustomFaceDetail{}, &api.AddCustomFace{},
		&api.DeleteCustomFace{}, &api.SetCustomFaceDesc{}, &api.SetInputStatus{},
		&api.NcGetUserStatus{}, &api.NcGetRkey{}, &api.GetMiniAppArk{}, &api.SendPacket{},
		&api.BotExit{}, &api.GetCollectionList{},

		// ---------------- Go-CQHTTP 其他 ----------------
		&api.SetQQProfile{}, &api.CheckURLSafely{}, &api.GetOnlineClients{},
		&api.GetStrangerInfo{}, &api.GetModelShow{}, &api.SetModelShow{},
		&api.SendGroupNotice{}, &api.HandleQuickOperation{},

		// ---------------- 扩展接口 ----------------
		&api.SetGroupKickMembers{}, &api.CreateCollection{}, &api.SetSelfLongNick{},
		&api.SetQQAvatar{}, &api.TranslateEn2Zh{}, &api.GetClientKey{},
		&api.SendQzoneMsg{}, &api.OCRImage{}, &api.DeleteQzoneMsg{}, &api.OCRImageInternal{},
		&api.SetGroupSpecialTitle{}, &api.GetAICharacters{},
		&api.GetQzoneFeeds{}, &api.GetQzoneMsgList{}, &api.GetQzoneMsgDetail{},
		&api.GetQzoneCommentList{}, &api.GetQzoneCommentReplies{},
		&api.LikeQzone{}, &api.UnlikeQzone{}, &api.CommentQzone{},
		&api.GetQzoneProfile{}, &api.SetQzoneProfile{}, &api.SetQzoneMsgRight{}, &api.SetQzoneBan{},

		// ---------------- AI 扩展 ----------------
		&api.GetAIRecord{}, &api.SendGroupAIRecord{},

		// ---------------- QQ空间扩展(空友爱看) ----------------
		&api.GetQzonePublicCapabilities{}, &api.GetQzonePublicFeeds{},
		&api.GetQzonePublicFeedDetail{}, &api.GetQzonePublicComments{},
		&api.GetQzonePublicReplies{}, &api.SetQzonePublicUpvote{}, &api.SetQzonePublicLike{},
		&api.SetQzonePublicFollow{}, &api.SetQzonePublicCommentLike{},
		&api.CommentQzonePublicFeed{}, &api.ReplyQzonePublicFeed{},
		&api.DeleteQzonePublicComment{}, &api.DeleteQzonePublicReply{},
		&api.ForwardQzonePublicFeed{},

		// ---------------- 群相册 ----------------
		&api.DelGroupAlbumMedia{}, &api.SetGroupAlbumMediaLike{},
		&api.CancelGroupAlbumMediaLike{}, &api.DoGroupAlbumComment{},
		&api.GetGroupAlbumMediaList{}, &api.GetQunAlbumList{},
		&api.UploadImageToQunAlbum{}, &api.UploadImagesToQunAlbum{}, &api.UploadVideoToQunAlbum{},
	}
}

// actionRegistry 动作名（无 / 前缀）→ 请求结构体类型。napcat_api 逃生舱
// 用 reflect.New 现场构造零值请求后反序列化 params。init 期构建一次。
var actionRegistry = buildActionRegistry(actionTypes())

// buildActionRegistry 由动作实例列表构建名称 → 类型映射。
func buildActionRegistry(list []api.Request) map[string]reflect.Type {
	m := make(map[string]reflect.Type, len(list))
	for _, a := range list {
		name := strings.TrimPrefix(a.Path(), "/")
		if _, dup := m[name]; dup {
			panic("agent: 动作注册表路径重复 " + name)
		}
		m[name] = reflect.TypeOf(a).Elem()
	}
	return m
}

// napcatActionsJSON 常用动作速查资源（napcat_docs 工具输出）。
//
//go:embed napcat_actions.json
var napcatActionsJSON string
