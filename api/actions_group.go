package api

// 群组接口与群组扩展(来源: https://napcat.apifox.cn/llms.txt 各动作详情页)
// 发送群消息已在 api.go 定义,此处不重复。

// ---------------- 群组接口 ----------------

// GetGroupDetailInfo 获取群详细信息 POST /get_group_detail_info
type GetGroupDetailInfo struct {
	GroupID int64 `json:"group_id"`
}

func (r *GetGroupDetailInfo) Path() string { return "/get_group_detail_info" }

// GetGroupList 获取群列表 POST /get_group_list
type GetGroupList struct {
	NoCache bool `json:"no_cache,omitempty"`
}

func (r *GetGroupList) Path() string { return "/get_group_list" }

// GetGroupInfo 获取群信息 POST /get_group_info
type GetGroupInfo struct {
	GroupID int64 `json:"group_id"`
}

func (r *GetGroupInfo) Path() string { return "/get_group_info" }

// GetGroupMemberList 获取群成员列表 POST /get_group_member_list
type GetGroupMemberList struct {
	GroupID int64 `json:"group_id"`
	NoCache bool  `json:"no_cache,omitempty"`
}

func (r *GetGroupMemberList) Path() string { return "/get_group_member_list" }

// GetGroupMemberInfo 获取群成员信息 POST /get_group_member_info
type GetGroupMemberInfo struct {
	GroupID int64 `json:"group_id"`
	UserID  int64 `json:"user_id"`
	NoCache bool  `json:"no_cache,omitempty"`
}

func (r *GetGroupMemberInfo) Path() string { return "/get_group_member_info" }

// SetGroupAddRequest 处理加群请求 POST /set_group_add_request
type SetGroupAddRequest struct {
	Flag    string `json:"flag"`
	Approve bool   `json:"approve,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Count   int    `json:"count,omitempty"`
}

func (r *SetGroupAddRequest) Path() string { return "/set_group_add_request" }

// SetGroupLeave 退出群组 POST /set_group_leave
type SetGroupLeave struct {
	GroupID   int64 `json:"group_id"`
	IsDismiss bool  `json:"is_dismiss,omitempty"`
}

func (r *SetGroupLeave) Path() string { return "/set_group_leave" }

// SetGroupWholeBan 全员禁言 POST /set_group_whole_ban
type SetGroupWholeBan struct {
	GroupID int64 `json:"group_id"`
	Enable  bool  `json:"enable"`
}

func (r *SetGroupWholeBan) Path() string { return "/set_group_whole_ban" }

// SetGroupBan 群组禁言 POST /set_group_ban
type SetGroupBan struct {
	GroupID  int64 `json:"group_id"`
	UserID   int64 `json:"user_id"`
	Duration int64 `json:"duration"`
}

func (r *SetGroupBan) Path() string { return "/set_group_ban" }

// SetGroupKick 群组踢人 POST /set_group_kick
type SetGroupKick struct {
	GroupID          int64 `json:"group_id"`
	UserID           int64 `json:"user_id"`
	RejectAddRequest bool  `json:"reject_add_request,omitempty"`
}

func (r *SetGroupKick) Path() string { return "/set_group_kick" }

// SetGroupAdmin 设置群管理员 POST /set_group_admin
type SetGroupAdmin struct {
	GroupID int64 `json:"group_id"`
	UserID  int64 `json:"user_id"`
	Enable  bool  `json:"enable"`
}

func (r *SetGroupAdmin) Path() string { return "/set_group_admin" }

// SetGroupName 设置群名称 POST /set_group_name
type SetGroupName struct {
	GroupID   int64  `json:"group_id"`
	GroupName string `json:"group_name"`
}

func (r *SetGroupName) Path() string { return "/set_group_name" }

// SetGroupCard 设置群名片 POST /set_group_card
type SetGroupCard struct {
	GroupID int64  `json:"group_id"`
	UserID  int64  `json:"user_id"`
	Card    string `json:"card,omitempty"`
}

func (r *SetGroupCard) Path() string { return "/set_group_card" }

// GetEssenceMsgList 获取群精华消息 POST /get_essence_msg_list
type GetEssenceMsgList struct {
	GroupID int64 `json:"group_id"`
}

func (r *GetEssenceMsgList) Path() string { return "/get_essence_msg_list" }

// GetGroupIgnoredNotifies 获取群忽略通知 POST /get_group_ignored_notifies
type GetGroupIgnoredNotifies struct{}

func (r *GetGroupIgnoredNotifies) Path() string { return "/get_group_ignored_notifies" }

// DeleteEssenceMsg 移出精华消息 POST /delete_essence_msg
type DeleteEssenceMsg struct {
	MessageID int64  `json:"message_id,omitempty"`
	MsgSeq    string `json:"msg_seq,omitempty"`
	MsgRandom string `json:"msg_random,omitempty"`
	GroupID   int64  `json:"group_id,omitempty"`
}

func (r *DeleteEssenceMsg) Path() string { return "/delete_essence_msg" }

// GetGroupNotice 获取群公告 POST /_get_group_notice
type GetGroupNotice struct {
	GroupID int64 `json:"group_id"`
}

func (r *GetGroupNotice) Path() string { return "/_get_group_notice" }

// SetEssenceMsg 设置精华消息 POST /set_essence_msg
type SetEssenceMsg struct {
	MessageID int64 `json:"message_id"`
}

func (r *SetEssenceMsg) Path() string { return "/set_essence_msg" }

// DeleteGroupNotice 删除群公告 POST /_del_group_notice
type DeleteGroupNotice struct {
	GroupID  int64  `json:"group_id"`
	NoticeID string `json:"notice_id"`
}

func (r *DeleteGroupNotice) Path() string { return "/_del_group_notice" }

// GetGroupShutList 获取群禁言列表 POST /get_group_shut_list
type GetGroupShutList struct {
	GroupID int64 `json:"group_id"`
}

func (r *GetGroupShutList) Path() string { return "/get_group_shut_list" }

// GetGroupIgnoreAddRequest 获取群被忽略的加群请求 POST /get_group_ignore_add_request
type GetGroupIgnoreAddRequest struct{}

func (r *GetGroupIgnoreAddRequest) Path() string { return "/get_group_ignore_add_request" }

// SetGroupMemberInvitePolicy 设置群成员邀请策略 POST /set_group_member_invite_policy
type SetGroupMemberInvitePolicy struct {
	GroupID int64  `json:"group_id"`
	Policy  string `json:"policy"`
}

func (r *SetGroupMemberInvitePolicy) Path() string { return "/set_group_member_invite_policy" }

// SetGroupMemberPermissions 设置群成员功能权限 POST /set_group_member_permissions
type SetGroupMemberPermissions struct {
	GroupID                     int64 `json:"group_id"`
	AllowMemberUploadAlbum      bool  `json:"allow_member_upload_album,omitempty"`
	AllowMemberTemporarySession bool  `json:"allow_member_temporary_session,omitempty"`
	AllowMemberCreateGroup      bool  `json:"allow_member_create_group,omitempty"`
}

func (r *SetGroupMemberPermissions) Path() string { return "/set_group_member_permissions" }

// SetGroupNewMemberHistoryVisibility 设置新成员历史消息可见性 POST /set_group_new_member_history_visibility
type SetGroupNewMemberHistoryVisibility struct {
	GroupID int64 `json:"group_id"`
	Visible bool  `json:"visible"`
}

func (r *SetGroupNewMemberHistoryVisibility) Path() string {
	return "/set_group_new_member_history_visibility"
}

// GetGroupHonorInfo 获取群荣誉信息 POST /get_group_honor_info
type GetGroupHonorInfo struct {
	GroupID int64  `json:"group_id"`
	Type    string `json:"type,omitempty"`
}

func (r *GetGroupHonorInfo) Path() string { return "/get_group_honor_info" }

// GetGroupAtAllRemain 获取群艾特全体剩余次数 POST /get_group_at_all_remain
type GetGroupAtAllRemain struct {
	GroupID int64 `json:"group_id"`
}

func (r *GetGroupAtAllRemain) Path() string { return "/get_group_at_all_remain" }

// SetGroupPortrait 设置群头像 POST /set_group_portrait
type SetGroupPortrait struct {
	File    string `json:"file"`
	GroupID int64  `json:"group_id"`
}

func (r *SetGroupPortrait) Path() string { return "/set_group_portrait" }

// ---------------- 群组扩展 ----------------

// SetGroupAddOption 设置群加群选项 POST /set_group_add_option
type SetGroupAddOption struct {
	GroupID       int64  `json:"group_id"`
	AddType       int    `json:"add_type"`
	GroupQuestion string `json:"group_question,omitempty"`
	GroupAnswer   string `json:"group_answer,omitempty"`
}

func (r *SetGroupAddOption) Path() string { return "/set_group_add_option" }

// SetGroupRobotAddOption 设置群机器人加群选项 POST /set_group_robot_add_option
type SetGroupRobotAddOption struct {
	GroupID            int `json:"group_id"`
	RobotMemberSwitch  int `json:"robot_member_switch,omitempty"`
	RobotMemberExamine int `json:"robot_member_examine,omitempty"`
}

func (r *SetGroupRobotAddOption) Path() string { return "/set_group_robot_add_option" }

// SetGroupSearch 设置群搜索选项 POST /set_group_search
type SetGroupSearch struct {
	GroupID          int `json:"group_id"`
	NoCodeFingerOpen int `json:"no_code_finger_open,omitempty"`
	NoFingerOpen     int `json:"no_finger_open,omitempty"`
}

func (r *SetGroupSearch) Path() string { return "/set_group_search" }

// SetGroupRemark 设置群备注 POST /set_group_remark
type SetGroupRemark struct {
	GroupID int64  `json:"group_id"`
	Remark  string `json:"remark"`
}

func (r *SetGroupRemark) Path() string { return "/set_group_remark" }

// GetGroupInfoEx 获取群详细信息(扩展) POST /get_group_info_ex
type GetGroupInfoEx struct {
	GroupID int64 `json:"group_id"`
}

func (r *GetGroupInfoEx) Path() string { return "/get_group_info_ex" }

// SetGroupSign 群打卡 POST /set_group_sign
type SetGroupSign struct {
	GroupID int64 `json:"group_id"`
}

func (r *SetGroupSign) Path() string { return "/set_group_sign" }

// SendGroupSign 群打卡(旧路径) POST /send_group_sign
type SendGroupSign struct {
	GroupID int64 `json:"group_id"`
}

func (r *SendGroupSign) Path() string { return "/send_group_sign" }

// GetGroupSignedList 获取群组今日打卡列表 POST /get_group_signed_list
type GetGroupSignedList struct {
	GroupID int64 `json:"group_id"`
}

func (r *GetGroupSignedList) Path() string { return "/get_group_signed_list" }

// GetGroupShareLink 获取群分享链接 POST /get_group_share_link
type GetGroupShareLink struct {
	GroupID         int64  `json:"group_id"`
	NeedShortURL    *bool  `json:"need_short_url,omitempty"`
	SrcID           int    `json:"src_id,omitempty"`
	AdditionalParam string `json:"additional_param,omitempty"`
}

func (r *GetGroupShareLink) Path() string { return "/get_group_share_link" }
