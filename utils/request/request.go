package request

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"Q-Mi/api"
	apimsg "Q-Mi/api/message"
	"Q-Mi/utils/config"
)

// 本包负责向 NapCat 发送 API 请求:
// 接收 api 包中实现了 api.Request 的结构体,序列化为 JSON 后
// POST 到该请求 Path() 对应的 API 地址,并把响应解析为 *api.Response[T]。
// 连接地址与鉴权 token 来自配置文件中的 napcat.http 配置块。

// client 包级默认客户端,main 启动时调用 Init 初始化一次
var client *Client

type Client struct {
	baseURL string // 如 http://127.0.0.1:8080
	token   string // NapCat 的 access_token,为空则不带鉴权头
	http    *http.Client
}

// Init 用配置初始化默认客户端
func Init(cfg *config.Napcat) error {
	if cfg == nil {
		return fmt.Errorf("napcat 配置为空")
	}
	if !cfg.HTTP.Enable {
		return fmt.Errorf("napcat http 未启用,无法调用 API")
	}
	client = &Client{
		baseURL: fmt.Sprintf("http://%s:%d", cfg.HTTP.Host, cfg.HTTP.Port),
		token:   cfg.HTTP.Token,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
	slog.Info("NapCat API 客户端就绪", "addr", client.baseURL)
	return nil
}

// Do 发送一个 API 请求并解析响应。
// T 指定该 API 返回的业务数据类型,如 Do[api.SendMsgData](ctx, &api.SendGroupMsg{...})。
// retcode 非 0 时返回错误,但响应对象仍会返回,便于调用方拿到完整信息。
func Do[T any](ctx context.Context, req api.Request) (*api.Response[T], error) {
	if client == nil {
		return nil, fmt.Errorf("request 未初始化,请先调用 request.Init")
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	raw, err := client.post(ctx, req.Path(), body)
	if err != nil {
		return nil, err
	}

	var resp api.Response[T]
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}
	if resp.Retcode != 0 {
		return &resp, fmt.Errorf("API %s 调用失败 retcode=%d status=%s message=%s wording=%s",
			req.Path(), resp.Retcode, resp.Status, resp.Message, resp.Wording)
	}
	return &resp, nil
}

func (c *Client) post(ctx context.Context, path string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 NapCat 失败: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, data)
	}
	return data, nil
}

// ---------------- 常用便捷函数 ----------------

// Call 通用调用:发送任意 api.Request 并把 data 透传为 json.RawMessage。
// 适合没有专门结构化响应类型的动作,由调用方自行解析 resp.Data。
func Call(ctx context.Context, req api.Request) (*api.Response[json.RawMessage], error) {
	return Do[json.RawMessage](ctx, req)
}

// SendForwardMsg 发送合并转发消息,返回 message_id/forward_id 等结构化数据
func SendForwardMsg(ctx context.Context, req *api.SendForwardMsg) (*api.Response[api.SendMsgData], error) {
	return Do[api.SendMsgData](ctx, req)
}

// SendGroupForwardMsg 发送群合并转发消息
func SendGroupForwardMsg(ctx context.Context, req *api.SendGroupForwardMsg) (*api.Response[api.SendMsgData], error) {
	return Do[api.SendMsgData](ctx, req)
}

// SendPrivateForwardMsg 发送私聊合并转发消息
func SendPrivateForwardMsg(ctx context.Context, req *api.SendPrivateForwardMsg) (*api.Response[api.SendMsgData], error) {
	return Do[api.SendMsgData](ctx, req)
}

// GetGroupMsgHistory 获取群历史消息,解析 messages 数组
func GetGroupMsgHistory(ctx context.Context, req *api.GetGroupMsgHistory) (*api.Response[api.GetMsgHistoryData], error) {
	return Do[api.GetMsgHistoryData](ctx, req)
}

// GetFriendMsgHistory 获取好友历史消息,解析 messages 数组
func GetFriendMsgHistory(ctx context.Context, req *api.GetFriendMsgHistory) (*api.Response[api.GetMsgHistoryData], error) {
	return Do[api.GetMsgHistoryData](ctx, req)
}

// GetFriendList 获取好友列表,解析为 FriendData 数组
func GetFriendList(ctx context.Context, req *api.GetFriendList) (*api.Response[[]api.FriendData], error) {
	return Do[[]api.FriendData](ctx, req)
}

// SendGroupMsg 发送群消息,消息段用 apimsg.Text / apimsg.At 等构造
func SendGroupMsg(ctx context.Context, groupID int64, msg ...*apimsg.Message) (*api.Response[api.SendMsgData], error) {
	return Do[api.SendMsgData](ctx, &api.SendGroupMsg{GroupID: groupID, Message: msg})
}

// SendPrivateMsg 发送私聊消息
func SendPrivateMsg(ctx context.Context, userID int64, msg ...*apimsg.Message) (*api.Response[api.SendMsgData], error) {
	return Do[api.SendMsgData](ctx, &api.SendPrivateMsg{UserID: userID, Message: msg})
}

// GetMsg 获取消息详情
func GetMsg(ctx context.Context, messageID int64) (*api.Response[api.GetMsgData], error) {
	return Do[api.GetMsgData](ctx, &api.GetMsg{MessageID: messageID})
}

// DeleteMsg 撤回消息
func DeleteMsg(ctx context.Context, messageID int64) (*api.Response[api.EmptyData], error) {
	return Do[api.EmptyData](ctx, &api.DeleteMsg{MessageID: messageID})
}
