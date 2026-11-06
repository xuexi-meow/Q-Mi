// tools.go 定义 P2 工具集（spec §3）：查询、发消息、思考笔记、情绪状态。
// help 的输出直接从工具定义生成（单一源头）；工具 Run 返回紧凑 JSON 字符串，
// 出错返回 error 由调用方（manager）包装成 {"error": ...} 回填思维链。
// finish 无 Run，仅作为 manager 思考循环的终止标记。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
// 查询类工具返回 error。
func newToolSet(store *storage.Store, notesPath, statePath string) []Tool {
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
