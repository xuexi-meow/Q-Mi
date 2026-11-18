package qq

import (
	"Q-Mi/api"
	"reflect"
	"strings"
	"testing"
)

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

// actionRegistry 保留旧 api 包的类型登记，用于覆盖性检查。
// 逃生舱使用官方目录与原始 JSON，不再经反射结构体丢弃参数。
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
func TestActionRegistryCoversAllActions(t *testing.T) {
	// 全量动作注册表：与 api 包 196 个动作一一对应（192 个 P3-1 + api.go 4 个）
	if len(actionRegistry) != 196 {
		t.Errorf("动作注册表数量 = %d, want 196", len(actionRegistry))
	}
	for name, typ := range actionRegistry {
		if name == "" || strings.HasPrefix(name, "/") {
			t.Errorf("注册表键非法: %q", name)
		}
		req := reflect.New(typ).Interface().(interface{ Path() string })
		if strings.TrimPrefix(req.Path(), "/") != name {
			t.Errorf("注册表键 %q 与 Path() 不一致", name)
		}
	}
	for _, want := range []string{"send_group_msg", "delete_msg", "get_friend_list", "upload_group_file", "set_group_kick"} {
		if _, ok := actionRegistry[want]; !ok {
			t.Errorf("注册表缺动作 %q", want)
		}
	}
}
