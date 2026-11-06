package api

import (
	"encoding/json"
	"testing"

	apimsg "Q-Mi/api/message"
)

// 本文件测试 P3-1 补全的 NapCat API 请求结构体:
// 1. 全部类型实现 Request 接口(编译期保证);
// 2. 代表动作的 Path() 与 llms.txt 文档一致;
// 3. 必需字段序列化输出、可选字段 omitempty 行为。

// allActions 收集全部新增请求结构体的零值,编译期保证每个类型都实现了 Request。
// 新增动作时必须同步在此登记。
func allActions() []Request {
	return []Request{
		// ---------------- 核心接口 ----------------
		&SetGroupTodo{}, &CompleteGroupTodo{}, &CancelGroupTodo{},
		&SendGroupPoke{}, &SendFriendPoke{}, &SendPoke{},

		// ---------------- 群组接口 ----------------
		&GetGroupDetailInfo{}, &GetGroupList{}, &GetGroupInfo{},
		&GetGroupMemberList{}, &GetGroupMemberInfo{},
		&SetGroupAddRequest{}, &SetGroupLeave{}, &SetGroupWholeBan{},
		&SetGroupBan{}, &SetGroupKick{}, &SetGroupAdmin{},
		&SetGroupName{}, &SetGroupCard{}, &GetEssenceMsgList{},
		&GetGroupIgnoredNotifies{}, &DeleteEssenceMsg{}, &GetGroupNotice{},
		&SetEssenceMsg{}, &DeleteGroupNotice{}, &GetGroupShutList{},
		&GetGroupIgnoreAddRequest{}, &SetGroupMemberInvitePolicy{},
		&SetGroupMemberPermissions{}, &SetGroupNewMemberHistoryVisibility{},
		&GetGroupHonorInfo{}, &GetGroupAtAllRemain{}, &SetGroupPortrait{},

		// ---------------- 群组扩展 ----------------
		&SetGroupAddOption{}, &SetGroupRobotAddOption{}, &SetGroupSearch{},
		&SetGroupRemark{}, &GetGroupInfoEx{}, &SetGroupSign{}, &SendGroupSign{},
		&GetGroupSignedList{}, &GetGroupShareLink{},

		// ---------------- 好友/用户 ----------------
		&SetFriendRemark{}, &SendLike{}, &GetFriendList{},
		&SetFriendAddRequest{}, &GetCookies{}, &GetRecentContact{},
		&GetFriendsWithCategory{}, &GetProfileLike{}, &SetDiyOnlineStatus{},
		&GetUnidirectionalFriendList{}, &DeleteFriend{},

		// ---------------- 消息接口 ----------------
		&SendMsg{}, &ForwardFriendSingleMsg{}, &ForwardGroupSingleMsg{},
		&MarkGroupMsgAsRead{}, &MarkPrivateMsgAsRead{}, &MarkMsgAsRead{},
		&MarkAllAsRead{},
		&SendForwardMsg{}, &SendGroupForwardMsg{}, &SendPrivateForwardMsg{},
		&GetForwardMsg{}, &GetGroupMsgHistory{}, &GetFriendMsgHistory{},

		// ---------------- 消息扩展 ----------------
		&FetchEmojiLike{}, &GetEmojiLikes{}, &FetchPttText{},
		&ArkShareGroup{}, &ArkSharePeer{}, &SendGroupArkShare{}, &SendArkShare{},
		&SetMsgEmojiLike{}, &ClickInlineKeyboardButton{},

		// ---------------- 文件接口 ----------------
		&GetFile{}, &GetImage{}, &GetRecord{},
		&GetGroupFileURL{}, &GetPrivateFileURL{},

		// ---------------- 文件扩展 ----------------
		&MoveGroupFile{}, &RenameGroupFile{}, &TransGroupFile{},
		&CreateFlashTask{}, &GetFlashFileList{}, &GetFlashFileURL{},
		&SendFlashMsg{}, &GetShareLink{}, &GetFilesetInfo{},
		&GetOnlineFileMsg{}, &SendOnlineFile{}, &SendOnlineFolder{},
		&ReceiveOnlineFile{}, &RefuseOnlineFile{}, &CancelOnlineFile{},
		&DownloadFileset{}, &GetFilesetID{},

		// ---------------- Go-CQHTTP 文件 ----------------
		&GetGroupRootFiles{}, &UploadGroupFile{}, &DeleteGroupFile{},
		&CreateGroupFileFolder{}, &DeleteGroupFolder{}, &GetGroupFileSystemInfo{},
		&GetGroupFilesByFolder{}, &DownloadFile{}, &UploadPrivateFile{},

		// ---------------- 系统接口 ----------------
		&GetLoginInfo{}, &GetVersionInfo{}, &CanSendRecord{}, &CanSendImage{},
		&GetStatus{}, &GetCsrfToken{}, &GetCredentials{}, &NcGetPacketStatus{},
		&SetRestart{}, &GetGroupSystemMsg{}, &CleanCache{},
		&SetDoubtFriendsAddRequest{}, &GetDoubtFriendsAddRequest{},

		// ---------------- 系统扩展 ----------------
		&GetRkey{}, &GetRkeyServer{}, &SetOnlineStatus{}, &GetRobotUinRange{},
		&FetchCustomFace{}, &FetchCustomFaceDetail{}, &AddCustomFace{},
		&DeleteCustomFace{}, &SetCustomFaceDesc{}, &SetInputStatus{},
		&NcGetUserStatus{}, &NcGetRkey{}, &GetMiniAppArk{}, &SendPacket{},
		&BotExit{}, &GetCollectionList{},

		// ---------------- Go-CQHTTP 其他 ----------------
		&SetQQProfile{}, &CheckURLSafely{}, &GetOnlineClients{},
		&GetStrangerInfo{}, &GetModelShow{}, &SetModelShow{},
		&SendGroupNotice{}, &HandleQuickOperation{},

		// ---------------- 扩展接口 ----------------
		&SetGroupKickMembers{}, &CreateCollection{}, &SetSelfLongNick{},
		&SetQQAvatar{}, &TranslateEn2Zh{}, &GetClientKey{},
		&SendQzoneMsg{}, &OCRImage{}, &DeleteQzoneMsg{}, &OCRImageInternal{},
		&SetGroupSpecialTitle{}, &GetAICharacters{},
		&GetQzoneFeeds{}, &GetQzoneMsgList{}, &GetQzoneMsgDetail{},
		&GetQzoneCommentList{}, &GetQzoneCommentReplies{},
		&LikeQzone{}, &UnlikeQzone{}, &CommentQzone{},
		&GetQzoneProfile{}, &SetQzoneProfile{}, &SetQzoneMsgRight{}, &SetQzoneBan{},

		// ---------------- AI 扩展 ----------------
		&GetAIRecord{}, &SendGroupAIRecord{},

		// ---------------- QQ空间扩展(空友爱看) ----------------
		&GetQzonePublicCapabilities{}, &GetQzonePublicFeeds{},
		&GetQzonePublicFeedDetail{}, &GetQzonePublicComments{},
		&GetQzonePublicReplies{}, &SetQzonePublicUpvote{}, &SetQzonePublicLike{},
		&SetQzonePublicFollow{}, &SetQzonePublicCommentLike{},
		&CommentQzonePublicFeed{}, &ReplyQzonePublicFeed{},
		&DeleteQzonePublicComment{}, &DeleteQzonePublicReply{},
		&ForwardQzonePublicFeed{},

		// ---------------- 群相册 ----------------
		&DelGroupAlbumMedia{}, &SetGroupAlbumMediaLike{},
		&CancelGroupAlbumMediaLike{}, &DoGroupAlbumComment{},
		&GetGroupAlbumMediaList{}, &GetQunAlbumList{},
		&UploadImageToQunAlbum{}, &UploadImagesToQunAlbum{}, &UploadVideoToQunAlbum{},
	}
}

func TestAllActionsImplementRequest(t *testing.T) {
	actions := allActions()
	// 与 llms.txt 对齐:196 个动作中 4 个已在 api.go 定义,新增 192 个
	if len(actions) != 192 {
		t.Errorf("登记的动作数量 = %d, want 192", len(actions))
	}
	seen := make(map[string]bool)
	for _, a := range actions {
		p := a.Path()
		if p == "" || p[0] != '/' {
			t.Errorf("Path() 返回非法: %q", p)
		}
		if seen[p] {
			t.Errorf("Path 重复: %s", p)
		}
		seen[p] = true
	}
}

// TestActionPaths 抽取各分类代表动作,断言 Path 与文档一致
func TestActionPaths(t *testing.T) {
	cases := []struct {
		name string
		req  Request
		want string
	}{
		{"核心-设置群待办", &SetGroupTodo{}, "/set_group_todo"},
		{"核心-发送戳一戳", &SendPoke{}, "/send_poke"},
		{"群组-获取群列表", &GetGroupList{}, "/get_group_list"},
		{"群组-群组禁言", &SetGroupBan{}, "/set_group_ban"},
		{"群组-获取群公告", &GetGroupNotice{}, "/_get_group_notice"},
		{"群组扩展-群打卡", &SetGroupSign{}, "/set_group_sign"},
		{"群组扩展-群打卡旧路径", &SendGroupSign{}, "/send_group_sign"},
		{"好友-获取好友列表", &GetFriendList{}, "/get_friend_list"},
		{"好友-删除好友", &DeleteFriend{}, "/delete_friend"},
		{"消息-发送消息", &SendMsg{}, "/send_msg"},
		{"消息-群历史消息", &GetGroupMsgHistory{}, "/get_group_msg_history"},
		{"消息-合并转发", &SendGroupForwardMsg{}, "/send_group_forward_msg"},
		{"消息扩展-表情点赞", &SetMsgEmojiLike{}, "/set_msg_emoji_like"},
		{"消息扩展-Ark分享群", &ArkShareGroup{}, "/ArkShareGroup"},
		{"文件-获取语音", &GetRecord{}, "/get_record"},
		{"文件扩展-创建闪传任务", &CreateFlashTask{}, "/create_flash_task"},
		{"系统-重启服务", &SetRestart{}, "/set_restart"},
		{"系统扩展-发送原始数据包", &SendPacket{}, "/send_packet"},
		{"扩展-设置专属头衔", &SetGroupSpecialTitle{}, "/set_group_special_title"},
		{"AI-发送群AI语音", &SendGroupAIRecord{}, "/send_group_ai_record"},
		{"QQ空间-空友爱看动态", &GetQzonePublicFeeds{}, "/get_qzone_public_feeds"},
		{"群相册-上传图片", &UploadImageToQunAlbum{}, "/upload_image_to_qun_album"},
		{"Go-CQHTTP-快速操作", &HandleQuickOperation{}, "/.handle_quick_operation"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.req.Path(); got != c.want {
				t.Errorf("Path() = %q, want %q", got, c.want)
			}
		})
	}
}

// TestRequiredFieldSerialization 断言必需字段一定输出且 tag 与文档一致
func TestRequiredFieldSerialization(t *testing.T) {
	cases := []struct {
		name string
		req  Request
		want map[string]any
	}{
		{
			"群组禁言:三个必需字段",
			&SetGroupBan{GroupID: 123, UserID: 456, Duration: 600},
			map[string]any{"group_id": float64(123), "user_id": float64(456), "duration": float64(600)},
		},
		{
			"批量踢人:user_id 为数组",
			&SetGroupKickMembers{GroupID: 1, UserIDs: []int64{10, 20}},
			map[string]any{"group_id": float64(1), "user_id": []any{float64(10), float64(20)}},
		},
		{
			"发送合并转发消息:message 为消息段数组",
			&SendGroupForwardMsg{GroupID: 7, Message: []*apimsg.Message{apimsg.Text("hi")}},
			map[string]any{"group_id": float64(7)},
		},
		{
			"发送原始数据包:rsp 布尔必需",
			&SendPacket{Cmd: "wtlogin.login", Data: "0A0B", Rsp: true},
			map[string]any{"cmd": "wtlogin.login", "data": "0A0B", "rsp": true},
		},
		{
			"创建收藏:rawData 驼峰 tag",
			&CreateCollection{RawData: "{\"x\":1}", Brief: "收藏"},
			map[string]any{"rawData": "{\"x\":1}", "brief": "收藏"},
		},
		{
			"设置个性签名:longNick 驼峰 tag",
			&SetSelfLongNick{LongNick: "签名"},
			map[string]any{"longNick": "签名"},
		},
		{
			"群相册评论:content 必需,lloc/batch_id 可选",
			&DoGroupAlbumComment{GroupID: 1, AlbumID: "a1", Content: "好看"},
			map[string]any{"group_id": float64(1), "album_id": "a1", "content": "好看"},
		},
		{
			"上传多图到群相册:files 数组",
			&UploadImagesToQunAlbum{GroupID: 1, AlbumID: "a", AlbumName: "n", Files: []string{"f1", "f2"}},
			map[string]any{"group_id": float64(1), "album_id": "a", "album_name": "n", "files": []any{"f1", "f2"}},
		},
		{
			"空友爱看动态详情:feed 对象透传",
			&GetQzonePublicFeedDetail{Feed: json.RawMessage(`{"tid":"t1"}`)},
			map[string]any{"feed": map[string]any{"tid": "t1"}},
		},
		{
			"闪传任务:files 兼容单文件字符串",
			&CreateFlashTask{Files: json.RawMessage(`"C:\\a.jpg"`)},
			map[string]any{"files": "C:\\a.jpg"},
		},
		{
			"获取好友历史消息:必需的布尔序列化",
			&GetFriendMsgHistory{UserID: 5, Count: 20},
			map[string]any{"user_id": float64(5), "count": float64(20),
				"reverse_order": false, "disable_get_url": false, "parse_mult_msg": false, "quick_reply": false},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data, err := json.Marshal(c.req)
			if err != nil {
				t.Fatalf("序列化失败: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("反序列化失败: %v", err)
			}
			for k, want := range c.want {
				v, ok := got[k]
				if !ok {
					t.Errorf("缺少字段 %s, 完整输出: %s", k, data)
					continue
				}
				if fmtJSON(v) != fmtJSON(want) {
					t.Errorf("字段 %s = %s, want %s", k, fmtJSON(v), fmtJSON(want))
				}
			}
		})
	}
}

// TestOptionalFieldsOmitted 断言可选字段在零值时不输出
func TestOptionalFieldsOmitted(t *testing.T) {
	cases := []struct {
		name string
		req  Request
		abs  []string // 必须不出现的可选字段
	}{
		{"获取群列表:no_cache 省略", &GetGroupList{}, []string{"no_cache"}},
		{"戳一戳:group_id/target_id 省略", &SendFriendPoke{UserID: 1}, []string{"group_id", "target_id"}},
		{"设置消息表情点赞:set 省略", &SetMsgEmojiLike{MessageID: 1, EmojiID: "4"}, []string{"set"}},
		{"添加自定义表情:可选字段省略", &AddCustomFace{File: "f.png"},
			[]string{"emoji_id", "package_id", "file_name", "file_size", "md5", "is_mark_face", "is_origin"}},
		{"获取群分享链接:可选字段省略", &GetGroupShareLink{GroupID: 1},
			[]string{"need_short_url", "src_id", "additional_param"}},
		{"下载文件:可选字段省略", &DownloadFile{URL: "http://x"},
			[]string{"base64", "name", "headers"}},
		{"设置群待办:message_id/message_seq 省略", &SetGroupTodo{GroupID: 1},
			[]string{"message_id", "message_seq"}},
		{"上传群文件:folder 兼容字段省略", &UploadGroupFile{GroupID: 1, File: "f", Name: "n", UploadFile: true},
			[]string{"folder", "folder_id"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data, err := json.Marshal(c.req)
			if err != nil {
				t.Fatalf("序列化失败: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("反序列化失败: %v", err)
			}
			for _, k := range c.abs {
				if _, ok := got[k]; ok {
					t.Errorf("可选字段 %s 不应输出, 完整输出: %s", k, data)
				}
			}
		})
	}
}

// TestResponseDataStructs 少数结构化响应:历史消息与好友列表条目
func TestResponseDataStructs(t *testing.T) {
	var hist GetMsgHistoryData
	if err := json.Unmarshal([]byte(
		`{"messages":[{"message_id":11,"raw_message":"hi"}]}`), &hist); err != nil {
		t.Fatalf("解析历史消息失败: %v", err)
	}
	if len(hist.Messages) != 1 || hist.Messages[0].MessageID != 11 || hist.Messages[0].RawMessage != "hi" {
		t.Errorf("GetMsgHistoryData 解析错误: %+v", hist)
	}

	var friends []FriendData
	if err := json.Unmarshal([]byte(
		`[{"user_id":10001,"nickname":"甲","remark":"备注甲","level":3}]`), &friends); err != nil {
		t.Fatalf("解析好友列表失败: %v", err)
	}
	if len(friends) != 1 || friends[0].UserID != 10001 || friends[0].Remark != "备注甲" || friends[0].Level != 3 {
		t.Errorf("FriendData 解析错误: %+v", friends)
	}
}

// fmtJSON 统一比较格式
func fmtJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
