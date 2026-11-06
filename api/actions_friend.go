package api

// 好友/用户接口与用户扩展(来源: https://napcat.apifox.cn/llms.txt 各动作详情页)

// SetFriendRemark 设置好友备注 POST /set_friend_remark
type SetFriendRemark struct {
	UserID int64  `json:"user_id"`
	Remark string `json:"remark"`
}

func (r *SetFriendRemark) Path() string { return "/set_friend_remark" }

// SendLike 点赞 POST /send_like
type SendLike struct {
	UserID int64 `json:"user_id"`
	Times  int   `json:"times"`
}

func (r *SendLike) Path() string { return "/send_like" }

// GetFriendList 获取好友列表 POST /get_friend_list
type GetFriendList struct {
	NoCache bool `json:"no_cache,omitempty"`
}

func (r *GetFriendList) Path() string { return "/get_friend_list" }

// FriendData 好友列表条目
type FriendData struct {
	UserID   int64  `json:"user_id"`
	Nickname string `json:"nickname"`
	Remark   string `json:"remark"`
	Level    int    `json:"level"`
}

// SetFriendAddRequest 处理加好友请求 POST /set_friend_add_request
type SetFriendAddRequest struct {
	Flag    string `json:"flag"`
	Approve bool   `json:"approve,omitempty"`
	Remark  string `json:"remark,omitempty"`
}

func (r *SetFriendAddRequest) Path() string { return "/set_friend_add_request" }

// GetCookies 获取 Cookies POST /get_cookies
type GetCookies struct {
	Domain string `json:"domain"`
}

func (r *GetCookies) Path() string { return "/get_cookies" }

// GetRecentContact 获取最近会话 POST /get_recent_contact
type GetRecentContact struct {
	Count int `json:"count"`
}

func (r *GetRecentContact) Path() string { return "/get_recent_contact" }

// GetFriendsWithCategory 获取带分组的好友列表 POST /get_friends_with_category
type GetFriendsWithCategory struct{}

func (r *GetFriendsWithCategory) Path() string { return "/get_friends_with_category" }

// GetProfileLike 获取资料点赞 POST /get_profile_like
type GetProfileLike struct {
	UserID int64 `json:"user_id,omitempty"`
	Start  int   `json:"start"`
	Count  int   `json:"count"`
}

func (r *GetProfileLike) Path() string { return "/get_profile_like" }

// SetDiyOnlineStatus 设置自定义在线状态 POST /set_diy_online_status
type SetDiyOnlineStatus struct {
	FaceID   int    `json:"face_id"`
	FaceType int    `json:"face_type"`
	Wording  string `json:"wording"`
}

func (r *SetDiyOnlineStatus) Path() string { return "/set_diy_online_status" }

// GetUnidirectionalFriendList 获取单向好友列表 POST /get_unidirectional_friend_list
type GetUnidirectionalFriendList struct{}

func (r *GetUnidirectionalFriendList) Path() string { return "/get_unidirectional_friend_list" }

// DeleteFriend 删除好友 POST /delete_friend
type DeleteFriend struct {
	FriendID    int64 `json:"friend_id,omitempty"`
	UserID      int64 `json:"user_id,omitempty"`
	TempBlock   bool  `json:"temp_block,omitempty"`
	TempBothDel bool  `json:"temp_both_del,omitempty"`
}

func (r *DeleteFriend) Path() string { return "/delete_friend" }
