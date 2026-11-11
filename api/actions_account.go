package api

import "encoding/json"

// 系统接口、系统扩展与 Go-CQHTTP 其他(来源: https://napcat.apifox.cn/llms.txt 各动作详情页)

// ---------------- 系统接口 ----------------

// GetLoginInfo 获取登录号信息 POST /get_login_info
type GetLoginInfo struct{}

func (r *GetLoginInfo) Path() string { return "/get_login_info" }

// GetVersionInfo 获取版本信息 POST /get_version_info
type GetVersionInfo struct{}

func (r *GetVersionInfo) Path() string { return "/get_version_info" }

// CanSendRecord 是否可以发送语音 POST /can_send_record
type CanSendRecord struct{}

func (r *CanSendRecord) Path() string { return "/can_send_record" }

// CanSendImage 是否可以发送图片 POST /can_send_image
type CanSendImage struct{}

func (r *CanSendImage) Path() string { return "/can_send_image" }

// GetStatus 获取运行状态 POST /get_status
type GetStatus struct{}

func (r *GetStatus) Path() string { return "/get_status" }

// GetCsrfToken 获取 CSRF Token POST /get_csrf_token
type GetCsrfToken struct{}

func (r *GetCsrfToken) Path() string { return "/get_csrf_token" }

// GetCredentials 获取登录凭证 POST /get_credentials
type GetCredentials struct {
	Domain string `json:"domain"`
}

func (r *GetCredentials) Path() string { return "/get_credentials" }

// NcGetPacketStatus 获取 Packet 状态 POST /nc_get_packet_status
type NcGetPacketStatus struct{}

func (r *NcGetPacketStatus) Path() string { return "/nc_get_packet_status" }

// SetRestart 重启服务 POST /set_restart
type SetRestart struct{}

func (r *SetRestart) Path() string { return "/set_restart" }

// GetGroupSystemMsg 获取群系统消息 POST /get_group_system_msg
type GetGroupSystemMsg struct {
	Count int `json:"count"`
}

func (r *GetGroupSystemMsg) Path() string { return "/get_group_system_msg" }

// CleanCache 清理缓存 POST /clean_cache
type CleanCache struct{}

func (r *CleanCache) Path() string { return "/clean_cache" }

// SetDoubtFriendsAddRequest 处理可疑好友申请 POST /set_doubt_friends_add_request
type SetDoubtFriendsAddRequest struct {
	Flag    string `json:"flag"`
	Approve bool   `json:"approve"`
}

func (r *SetDoubtFriendsAddRequest) Path() string { return "/set_doubt_friends_add_request" }

// GetDoubtFriendsAddRequest 获取可疑好友申请 POST /get_doubt_friends_add_request
type GetDoubtFriendsAddRequest struct {
	Count int `json:"count"`
}

func (r *GetDoubtFriendsAddRequest) Path() string { return "/get_doubt_friends_add_request" }

// ---------------- 系统扩展 ----------------

// GetRkey 获取扩展 RKey POST /get_rkey
type GetRkey struct{}

func (r *GetRkey) Path() string { return "/get_rkey" }

// GetRkeyServer 获取 RKey 服务器 POST /get_rkey_server
type GetRkeyServer struct{}

func (r *GetRkeyServer) Path() string { return "/get_rkey_server" }

// SetOnlineStatus 设置在线状态 POST /set_online_status
type SetOnlineStatus struct {
	Status        int `json:"status"`
	ExtStatus     int `json:"ext_status"`
	BatteryStatus int `json:"battery_status"`
}

func (r *SetOnlineStatus) Path() string { return "/set_online_status" }

// GetRobotUinRange 获取机器人 UIN 范围 POST /get_robot_uin_range
type GetRobotUinRange struct{}

func (r *GetRobotUinRange) Path() string { return "/get_robot_uin_range" }

// FetchCustomFace 获取自定义表情 POST /fetch_custom_face
type FetchCustomFace struct {
	Count int `json:"count"`
}

func (r *FetchCustomFace) Path() string { return "/fetch_custom_face" }

// FetchCustomFaceDetail 获取自定义表情详情 POST /fetch_custom_face_detail
type FetchCustomFaceDetail struct {
	Count int `json:"count"`
}

func (r *FetchCustomFaceDetail) Path() string { return "/fetch_custom_face_detail" }

// CustomFaceDetail 一条账号收藏表情的详情（SnowLuma 扩展）。
type CustomFaceDetail struct {
	EmojiID string `json:"emoji_id"`
	ResID   string `json:"resId"`
	URL     string `json:"url"`
	MD5     string `json:"md5"`
	Desc    string `json:"desc"`
}

// SendCustomFace 发送账号收藏表情 POST /send_custom_face（SnowLuma 扩展）。
// 该接口只能单独发一条，不能塞进 message 数组。
type SendCustomFace struct {
	EmojiID string `json:"emoji_id"`
	GroupID int64  `json:"group_id,omitempty"`
	UserID  int64  `json:"user_id,omitempty"`
	ReplyTo int64  `json:"reply_to,omitempty"`
}

func (r *SendCustomFace) Path() string { return "/send_custom_face" }

// AddCustomFace 添加自定义表情 POST /add_custom_face
type AddCustomFace struct {
	File       string `json:"file"`
	EmojiID    string `json:"emoji_id,omitempty"`
	PackageID  int    `json:"package_id,omitempty"`
	FileName   string `json:"file_name,omitempty"`
	FileSize   int64  `json:"file_size,omitempty"`
	MD5        string `json:"md5,omitempty"`
	IsMarkFace bool   `json:"is_mark_face,omitempty"`
	IsOrigin   bool   `json:"is_origin,omitempty"`
}

func (r *AddCustomFace) Path() string { return "/add_custom_face" }

// DeleteCustomFace 删除自定义表情 POST /delete_custom_face
// res_id/id/md5 兼容字符串或数组,用 RawMessage 透传
type DeleteCustomFace struct {
	ResID json.RawMessage `json:"res_id,omitempty"`
	ID    json.RawMessage `json:"id,omitempty"`
	IDs   []string        `json:"ids,omitempty"`
	MD5   json.RawMessage `json:"md5,omitempty"`
}

func (r *DeleteCustomFace) Path() string { return "/delete_custom_face" }

// SetCustomFaceDesc 修改自定义表情描述 POST /set_custom_face_desc
type SetCustomFaceDesc struct {
	EmojiID string `json:"emoji_id"`
	ResID   string `json:"res_id"`
	MD5     string `json:"md5"`
	Desc    string `json:"desc"`
}

func (r *SetCustomFaceDesc) Path() string { return "/set_custom_face_desc" }

// SetInputStatus 设置输入状态 POST /set_input_status
type SetInputStatus struct {
	UserID    int64 `json:"user_id"`
	EventType int   `json:"event_type"`
}

func (r *SetInputStatus) Path() string { return "/set_input_status" }

// NcGetUserStatus 获取用户在线状态 POST /nc_get_user_status
type NcGetUserStatus struct {
	UserID int64 `json:"user_id"`
}

func (r *NcGetUserStatus) Path() string { return "/nc_get_user_status" }

// NcGetRkey 获取 RKey POST /nc_get_rkey
type NcGetRkey struct{}

func (r *NcGetRkey) Path() string { return "/nc_get_rkey" }

// GetMiniAppArk 获取小程序 Ark POST /get_mini_app_ark
// 文档为多模板 anyOf(bili/weibo 等),字段按文档列出,rawArkData 原样透传
type GetMiniAppArk struct {
	Type       string          `json:"type,omitempty"`
	Title      string          `json:"title,omitempty"`
	Desc       string          `json:"desc,omitempty"`
	PicURL     string          `json:"picUrl,omitempty"`
	JumpURL    string          `json:"jumpUrl,omitempty"`
	WebURL     string          `json:"webUrl,omitempty"`
	RawArkData json.RawMessage `json:"raw_ark_data,omitempty"`
}

func (r *GetMiniAppArk) Path() string { return "/get_mini_app_ark" }

// SendPacket 发送原始数据包 POST /send_packet
type SendPacket struct {
	Cmd  string `json:"cmd"`
	Data string `json:"data"`
	Rsp  bool   `json:"rsp"`
}

func (r *SendPacket) Path() string { return "/send_packet" }

// BotExit 退出登录 POST /bot_exit
type BotExit struct{}

func (r *BotExit) Path() string { return "/bot_exit" }

// GetCollectionList 获取收藏列表 POST /get_collection_list
type GetCollectionList struct {
	Category string `json:"category"`
	Count    string `json:"count"`
}

func (r *GetCollectionList) Path() string { return "/get_collection_list" }

// ---------------- Go-CQHTTP 其他 ----------------

// SetQQProfile 设置QQ资料 POST /set_qq_profile
type SetQQProfile struct {
	Nickname     string `json:"nickname"`
	PersonalNote string `json:"personal_note,omitempty"`
	Sex          int    `json:"sex,omitempty"`
}

func (r *SetQQProfile) Path() string { return "/set_qq_profile" }

// CheckURLSafely 检查URL安全性 POST /check_url_safely
type CheckURLSafely struct {
	URL string `json:"url"`
}

func (r *CheckURLSafely) Path() string { return "/check_url_safely" }

// GetOnlineClients 获取在线客户端 POST /get_online_clients
type GetOnlineClients struct{}

func (r *GetOnlineClients) Path() string { return "/get_online_clients" }

// GetStrangerInfo 获取陌生人信息 POST /get_stranger_info
type GetStrangerInfo struct {
	UserID  int64 `json:"user_id"`
	NoCache bool  `json:"no_cache"`
}

func (r *GetStrangerInfo) Path() string { return "/get_stranger_info" }

// GetModelShow 获取机型显示 POST /_get_model_show
type GetModelShow struct {
	Model string `json:"model,omitempty"`
}

func (r *GetModelShow) Path() string { return "/_get_model_show" }

// SetModelShow 设置机型 POST /_set_model_show
// 文档 properties 为空,字段取自官方请求示例
type SetModelShow struct {
	Model     string `json:"model"`
	ModelShow string `json:"model_show"`
}

func (r *SetModelShow) Path() string { return "/_set_model_show" }

// SendGroupNotice 发送群公告 POST /_send_group_notice
type SendGroupNotice struct {
	GroupID         int64  `json:"group_id"`
	Content         string `json:"content"`
	Image           string `json:"image,omitempty"`
	Pinned          int    `json:"pinned"`
	Type            int    `json:"type"`
	ConfirmRequired int    `json:"confirm_required"`
	IsShowEditCard  int    `json:"is_show_edit_card"`
	TipWindowType   int    `json:"tip_window_type"`
}

func (r *SendGroupNotice) Path() string { return "/_send_group_notice" }

// HandleQuickOperation 处理快速操作 POST /.handle_quick_operation
// context/operation 为任意事件上下文与操作对象,用 RawMessage 透传
type HandleQuickOperation struct {
	Context   json.RawMessage `json:"context"`
	Operation json.RawMessage `json:"operation"`
}

func (r *HandleQuickOperation) Path() string { return "/.handle_quick_operation" }
