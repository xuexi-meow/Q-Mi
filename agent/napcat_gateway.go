package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Policies are explicit, not inferred from a name prefix or upstream readOnly.
// Updating the embedded catalog never grants new capabilities automatically.
var napcatPolicies = buildNapcatPolicies()

func buildNapcatPolicies() map[string]string {
	m := map[string]string{}
	for mode, names := range map[string]string{
		"allowed": `
			_get_friend_dress _get_group_notice _mark_all_as_read
			add_custom_face can_send_image can_send_record
			cancel_group_album_media_like cancel_group_todo check_url_safely
			comment_qzone comment_qzone_public_feed complete_group_todo
			create_group_file_folder delete_custom_face delete_essence_msg delete_msg
			do_group_album_comment download_file download_fileset
			fetch_custom_face fetch_custom_face_detail fetch_emoji_like fetch_face_entity
			fetch_ptt_text fetch_super_face_id fetch_sys_faces
			forward_friend_single_msg forward_group_single_msg forward_qzone_public_feed friend_poke
			get_ai_characters get_ai_record get_collection_list get_doubt_friends_add_request
			get_emoji_likes get_essence_msg_list get_file get_fileset_id get_fileset_info
			get_flash_file_list get_flash_file_url get_forward_msg get_friend_list
			get_friend_msg_history get_friends_with_category get_group_admin_permissions
			get_group_admin_settings get_group_album_list get_group_album_media_list
			get_group_at_all_remain get_group_detail_info get_group_file_system_info
			get_group_file_url get_group_files_by_folder get_group_honor_info
			get_group_ignore_add_request get_group_ignored_notifies get_group_info get_group_info_ex
			get_group_list get_group_member_info get_group_member_list get_group_msg_history
			get_group_root_files get_group_share_link get_group_shut_list get_group_signed_list
			get_group_system_msg get_group_tags get_group_todo_list get_image get_login_info
			get_mini_app_ark get_msg get_msg_emoji_likes get_online_clients get_private_file_url
			get_profile_like get_qun_album_list get_qzone_feed_detail get_qzone_feeds
			get_qzone_home_feeds get_qzone_msg_detail get_qzone_msg_list get_qzone_profile
			get_qzone_public_capabilities get_qzone_public_comments get_qzone_public_feed_detail
			get_qzone_public_feeds get_qzone_public_replies get_qzone_share_ark get_record
			get_share_link get_status get_stranger_info get_unidirectional_friend_list
			get_version_info group_poke like_qzone list_filesets mark_group_msg_as_read
			mark_msg_as_read mark_private_msg_as_read modify_custom_face move_custom_face_to_front
			nc_get_user_status ocr_image reply_qzone_public_feed search_sys_faces
			send_ark_share send_custom_face send_flash_msg send_forward_msg send_group_ai_record
			send_group_ark_share send_group_forward_msg send_group_msg send_like send_msg
			send_poke send_private_forward_msg send_private_msg send_qzone_msg send_tuwen_ark
			set_diy_online_status set_doubt_friends_add_request set_essence_msg set_friend_add_request
			set_friend_remark set_friends_category set_group_add_request set_group_album_media_like
			set_group_ban set_group_card set_group_reaction set_group_remark set_group_sign
			set_group_special_title set_group_todo set_group_whole_ban set_input_status
			set_msg_emoji_like set_online_status set_qzone_public_comment_like
			set_qzone_public_follow set_qzone_public_like set_qzone_public_upvote
			set_self_longnick share_group_ex share_peer trans_group_file translate_en2zh
			upload_forward_msg upload_foward_msg upload_group_file upload_private_file
			upload_image_to_qun_album upload_video_to_qun_album
		`,
		"admin": `
			_del_group_notice _send_group_notice add_group_tag delete_friend delete_group_file
			delete_group_file_folder delete_group_folder delete_group_tag del_group_album_media
			delete_qzone_msg delete_qzone_public_comment delete_qzone_public_reply
			move_group_file rename_group_file rename_group_file_folder
			set_group_add_option set_group_admin set_group_admin_permissions
			set_group_admin_receive_verify set_group_kick set_group_kick_members set_group_leave
			set_group_member_invite_policy set_group_member_permissions set_group_name
			set_group_new_member_history_visibility set_group_portrait set_group_robot_add_option
			set_group_search set_qq_avatar set_qq_profile set_qzone_ban set_qzone_msg_right set_qzone_profile
		`,
	} {
		for _, name := range strings.Fields(names) {
			m[name] = mode
		}
	}
	return m
}

func napcatActionPolicy(name string) (string, string) {
	switch napcatPolicies[name] {
	case "allowed":
		return "allowed", ""
	case "admin":
		return "admin", "影响权限、账号资料或删除资源，需要管理员权限"
	default:
		return "disabled", "未开放：凭据/原始协议/进程控制/流式接口、上游占位，或尚未完成本地审查"
	}
}

func authorizeNapcatAction(name string, admin bool) error {
	mode, reason := napcatActionPolicy(name)
	if mode == "disabled" {
		return fmt.Errorf("动作 %s 已禁用：%s", name, reason)
	}
	if mode == "admin" && !admin {
		return errAdminRequired
	}
	return nil
}

func napcatLocalConstraints(name string) string {
	switch name {
	case "set_group_ban":
		return "duration 为 0~2592000 秒；显式 0 表示解除禁言"
	case "get_group_msg_history", "get_friend_msg_history":
		return "count 至多 20；未指定时默认 20"
	case "upload_group_file", "upload_private_file":
		return "file 只允许本地路径，沿用工作区到 NapCat 的路径映射"
	default:
		return "只接受文档列出的顶层参数；保留 false/0；发言/评论/转发附言必须通过正文前缀校验；结果过长会明确标记截断"
	}
}

// rawNapcatRequest preserves supplied JSON values, including false/0 and
// upstream fields not represented by the older typed requests.
type rawNapcatRequest struct {
	action string
	params json.RawMessage
}

func (r *rawNapcatRequest) Path() string                 { return "/" + r.action }
func (r *rawNapcatRequest) MarshalJSON() ([]byte, error) { return r.params, nil }

func runNapcatAPI(ctx *ToolContext, args json.RawMessage) (string, error) {
	var p struct {
		Action string          `json:"action"`
		Params json.RawMessage `json:"params"`
	}
	if err := decodeArgs(args, &p); err != nil {
		return "", err
	}
	a, err := findNapcatAction(p.Action)
	if err != nil {
		return "", err
	}
	if err := authorizeNapcatAction(a.Name, ctx.Admin); err != nil {
		return "", err
	}
	if len(p.Params) == 0 {
		p.Params = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(p.Params))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", fmt.Errorf("params 应为参数对象: %w", err)
	}
	if err := a.schema.Validate(value); err != nil {
		return "", fmt.Errorf("params 不符合 %s 参数结构（napcat_docs(action) 查看）: %w", a.Name, err)
	}
	params := value.(map[string]any)
	switch a.Name {
	case "set_group_ban":
		if n, ok := params["duration"].(json.Number); ok {
			v, err := n.Int64()
			if err != nil || v < 0 || v > maxBanDuration {
				return "", fmt.Errorf("duration 需在 0 ~ %d 秒之间", maxBanDuration)
			}
		}
	case "get_group_msg_history", "get_friend_msg_history":
		if n, ok := params["count"].(json.Number); ok {
			v, err := n.Int64()
			if err != nil || v < 1 || v > maxMsgHistoryCount {
				return "", fmt.Errorf("count 需在 1 ~ %d 之间", maxMsgHistoryCount)
			}
		} else {
			params["count"] = maxMsgHistoryCount
		}
	case "upload_group_file", "upload_private_file":
		file, _ := params["file"].(string)
		low := strings.ToLower(file)
		if strings.HasPrefix(low, "http:") || strings.HasPrefix(low, "https:") || strings.HasPrefix(low, "base64:") {
			return "", fmt.Errorf("%s 仅支持本地路径", a.Name)
		}
		params["file"], err = napcatFilePath(ctx.Workspace, file)
		if err != nil {
			return "", err
		}
	}
	p.Params, err = json.Marshal(params)
	if err != nil {
		return "", err
	}
	data, err := callNapCat(&rawNapcatRequest{action: a.Name, params: p.Params})
	if err != nil {
		return "", err
	}
	const maxResultRunes = 12000
	if len([]rune(string(data))) > maxResultRunes {
		return marshalCompact(map[string]any{
			"truncated": true, "total_bytes": len(data), "preview": truncateRunes(string(data), maxResultRunes),
			"note": "结果超出上下文预算，preview 不是完整 JSON；请缩小查询/分页，常见查询优先使用封装工具。",
		})
	}
	return string(data), nil
}
