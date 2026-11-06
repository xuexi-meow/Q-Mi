package api

import "encoding/json"

// 文件接口、文件扩展与 Go-CQHTTP 文件操作(来源: https://napcat.apifox.cn/llms.txt 各动作详情页)

// ---------------- 文件接口 ----------------

// GetFile 获取文件 POST /get_file
type GetFile struct {
	File   string `json:"file,omitempty"`
	FileID string `json:"file_id,omitempty"`
}

func (r *GetFile) Path() string { return "/get_file" }

// GetImage 获取图片 POST /get_image
type GetImage struct {
	File   string `json:"file,omitempty"`
	FileID string `json:"file_id,omitempty"`
}

func (r *GetImage) Path() string { return "/get_image" }

// GetRecord 获取语音 POST /get_record
type GetRecord struct {
	File      string `json:"file,omitempty"`
	FileID    string `json:"file_id,omitempty"`
	OutFormat string `json:"out_format"`
}

func (r *GetRecord) Path() string { return "/get_record" }

// GetGroupFileURL 获取群文件URL POST /get_group_file_url
type GetGroupFileURL struct {
	GroupID int64  `json:"group_id"`
	FileID  string `json:"file_id"`
}

func (r *GetGroupFileURL) Path() string { return "/get_group_file_url" }

// GetPrivateFileURL 获取私聊文件URL POST /get_private_file_url
type GetPrivateFileURL struct {
	FileID string `json:"file_id"`
}

func (r *GetPrivateFileURL) Path() string { return "/get_private_file_url" }

// ---------------- 文件扩展 ----------------

// MoveGroupFile 移动群文件 POST /move_group_file
type MoveGroupFile struct {
	GroupID                int64  `json:"group_id"`
	FileID                 string `json:"file_id"`
	CurrentParentDirectory string `json:"current_parent_directory"`
	TargetParentDirectory  string `json:"target_parent_directory"`
}

func (r *MoveGroupFile) Path() string { return "/move_group_file" }

// RenameGroupFile 重命名群文件 POST /rename_group_file
type RenameGroupFile struct {
	GroupID                int64  `json:"group_id"`
	FileID                 string `json:"file_id"`
	CurrentParentDirectory string `json:"current_parent_directory"`
	NewName                string `json:"new_name"`
}

func (r *RenameGroupFile) Path() string { return "/rename_group_file" }

// TransGroupFile 传输群文件 POST /trans_group_file
type TransGroupFile struct {
	GroupID int64  `json:"group_id"`
	FileID  string `json:"file_id"`
}

func (r *TransGroupFile) Path() string { return "/trans_group_file" }

// CreateFlashTask 创建闪传任务 POST /create_flash_task
// files 兼容单个文件路径字符串或路径数组,用 RawMessage 透传
type CreateFlashTask struct {
	Files           json.RawMessage `json:"files"`
	Name            string          `json:"name,omitempty"`
	ThumbPath       string          `json:"thumb_path,omitempty"`
	ValiditySeconds int             `json:"validitySeconds,omitempty"`
}

func (r *CreateFlashTask) Path() string { return "/create_flash_task" }

// GetFlashFileList 获取闪传文件列表 POST /get_flash_file_list
type GetFlashFileList struct {
	FilesetID string `json:"fileset_id"`
}

func (r *GetFlashFileList) Path() string { return "/get_flash_file_list" }

// GetFlashFileURL 获取闪传文件链接 POST /get_flash_file_url
type GetFlashFileURL struct {
	FilesetID string `json:"fileset_id"`
	FileName  string `json:"file_name,omitempty"`
	FileIndex int    `json:"file_index,omitempty"`
}

func (r *GetFlashFileURL) Path() string { return "/get_flash_file_url" }

// SendFlashMsg 发送闪传消息 POST /send_flash_msg
type SendFlashMsg struct {
	FilesetID string `json:"fileset_id"`
	UserID    int64  `json:"user_id,omitempty"`
	GroupID   int64  `json:"group_id,omitempty"`
}

func (r *SendFlashMsg) Path() string { return "/send_flash_msg" }

// GetShareLink 获取文件分享链接 POST /get_share_link
type GetShareLink struct {
	FilesetID string `json:"fileset_id"`
}

func (r *GetShareLink) Path() string { return "/get_share_link" }

// GetFilesetInfo 获取文件集信息 POST /get_fileset_info
type GetFilesetInfo struct {
	FilesetID string `json:"fileset_id"`
}

func (r *GetFilesetInfo) Path() string { return "/get_fileset_info" }

// GetOnlineFileMsg 获取在线文件消息 POST /get_online_file_msg
type GetOnlineFileMsg struct {
	UserID int64 `json:"user_id"`
}

func (r *GetOnlineFileMsg) Path() string { return "/get_online_file_msg" }

// SendOnlineFile 发送在线文件 POST /send_online_file
type SendOnlineFile struct {
	UserID   int64  `json:"user_id"`
	FilePath string `json:"file_path"`
	FileName string `json:"file_name,omitempty"`
}

func (r *SendOnlineFile) Path() string { return "/send_online_file" }

// SendOnlineFolder 发送在线文件夹 POST /send_online_folder
type SendOnlineFolder struct {
	UserID     int64  `json:"user_id"`
	FolderPath string `json:"folder_path"`
	FolderName string `json:"folder_name,omitempty"`
}

func (r *SendOnlineFolder) Path() string { return "/send_online_folder" }

// ReceiveOnlineFile 接收在线文件 POST /receive_online_file
type ReceiveOnlineFile struct {
	UserID    int64  `json:"user_id"`
	MsgID     string `json:"msg_id"`
	ElementID string `json:"element_id"`
}

func (r *ReceiveOnlineFile) Path() string { return "/receive_online_file" }

// RefuseOnlineFile 拒绝在线文件 POST /refuse_online_file
type RefuseOnlineFile struct {
	UserID    int64  `json:"user_id"`
	MsgID     string `json:"msg_id"`
	ElementID string `json:"element_id"`
}

func (r *RefuseOnlineFile) Path() string { return "/refuse_online_file" }

// CancelOnlineFile 取消在线文件 POST /cancel_online_file
type CancelOnlineFile struct {
	UserID    int64  `json:"user_id"`
	MsgID     string `json:"msg_id"`
	ElementID string `json:"element_id"`
}

func (r *CancelOnlineFile) Path() string { return "/cancel_online_file" }

// DownloadFileset 下载文件集 POST /download_fileset
type DownloadFileset struct {
	FilesetID string `json:"fileset_id"`
}

func (r *DownloadFileset) Path() string { return "/download_fileset" }

// GetFilesetID 获取文件集 ID POST /get_fileset_id
type GetFilesetID struct {
	ShareCode string `json:"share_code"`
}

func (r *GetFilesetID) Path() string { return "/get_fileset_id" }

// ---------------- Go-CQHTTP 文件操作 ----------------

// GetGroupRootFiles 获取群根目录文件列表 POST /get_group_root_files
type GetGroupRootFiles struct {
	GroupID   int64 `json:"group_id"`
	FileCount int   `json:"file_count"`
}

func (r *GetGroupRootFiles) Path() string { return "/get_group_root_files" }

// UploadGroupFile 上传群文件 POST /upload_group_file
type UploadGroupFile struct {
	GroupID    int64  `json:"group_id"`
	File       string `json:"file"`
	Name       string `json:"name"`
	Folder     string `json:"folder,omitempty"`
	FolderID   string `json:"folder_id,omitempty"`
	UploadFile bool   `json:"upload_file"`
}

func (r *UploadGroupFile) Path() string { return "/upload_group_file" }

// DeleteGroupFile 删除群文件 POST /delete_group_file
type DeleteGroupFile struct {
	GroupID int64  `json:"group_id"`
	FileID  string `json:"file_id"`
	BusID   int    `json:"bus_id,omitempty"`
	Busid   int    `json:"busid,omitempty"`
}

func (r *DeleteGroupFile) Path() string { return "/delete_group_file" }

// CreateGroupFileFolder 创建群文件目录 POST /create_group_file_folder
type CreateGroupFileFolder struct {
	GroupID    int64  `json:"group_id"`
	FolderName string `json:"folder_name,omitempty"`
	Name       string `json:"name,omitempty"`
}

func (r *CreateGroupFileFolder) Path() string { return "/create_group_file_folder" }

// DeleteGroupFolder 删除群文件目录 POST /delete_group_folder
type DeleteGroupFolder struct {
	GroupID  int64  `json:"group_id"`
	FolderID string `json:"folder_id,omitempty"`
	Folder   string `json:"folder,omitempty"`
}

func (r *DeleteGroupFolder) Path() string { return "/delete_group_folder" }

// GetGroupFileSystemInfo 获取群文件系统信息 POST /get_group_file_system_info
type GetGroupFileSystemInfo struct {
	GroupID int64 `json:"group_id"`
}

func (r *GetGroupFileSystemInfo) Path() string { return "/get_group_file_system_info" }

// GetGroupFilesByFolder 获取群文件夹文件列表 POST /get_group_files_by_folder
type GetGroupFilesByFolder struct {
	GroupID   int64  `json:"group_id"`
	FolderID  string `json:"folder_id,omitempty"`
	Folder    string `json:"folder,omitempty"`
	FileCount int    `json:"file_count"`
}

func (r *GetGroupFilesByFolder) Path() string { return "/get_group_files_by_folder" }

// DownloadFile 下载文件 POST /download_file
// headers 兼容字符串或字符串数组,用 RawMessage 透传
type DownloadFile struct {
	URL     string          `json:"url,omitempty"`
	Base64  string          `json:"base64,omitempty"`
	Name    string          `json:"name,omitempty"`
	Headers json.RawMessage `json:"headers,omitempty"`
}

func (r *DownloadFile) Path() string { return "/download_file" }

// UploadPrivateFile 上传私聊文件 POST /upload_private_file
type UploadPrivateFile struct {
	UserID     int64  `json:"user_id"`
	File       string `json:"file"`
	Name       string `json:"name"`
	UploadFile bool   `json:"upload_file"`
}

func (r *UploadPrivateFile) Path() string { return "/upload_private_file" }
