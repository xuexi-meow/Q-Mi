// workspace.go 文件操作的工作区围栏与跨端路径映射：
//   - 围栏：文件工具默认只在 workspace_dir（缺省 agent/workspace）内活动，
//     工作区外的操作返回 approvalNeeded 信号走管理员审批（spec 权限模型：
//     工作区内/非危险自动放行，工作区外/危险发管理员确认）。
//   - 映射：Q-Mi 跑在 Windows、NapCat/SnowLuma 跑在 WSL，同一文件两边路径
//     不通；napcatFilePath 把 Windows 盘符路径映射为 /mnt/<盘符>/...（WSL
//     默认挂载）。图片 base64 绝不经模型上下文，一律走路径。
package agent

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// defaultWorkspaceDir 工作区缺省目录（相对工作目录）。
const defaultWorkspaceDir = "agent/workspace"

// maxFileReadBytes read_file 单次读取上限（文本截断）。
const maxFileReadBytes = 64 << 10

// maxFileWriteBytes write_file 单次写入上限。
const maxFileWriteBytes = 2 << 20

// maxDownloadBytes download_file 单文件上限。
const maxDownloadBytes = 200 << 20

// winDriveRe Windows 盘符绝对路径（F:\... / f:/...）。
var winDriveRe = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// approvalNeeded 工具操作需要管理员审批的信号：execToolCall 捕获后走
// ApprovalManager（管理员 QQ 回复「同意/拒绝 <id>」后执行）。
type approvalNeeded struct{ reason string }

func (e approvalNeeded) Error() string { return e.reason }

// workspaceAbs 把 p 解析为工作区内绝对路径。相对路径相对工作区；绝对路径
// 必须落在工作区内。越界时返回 errOutside 且 abs 为其工作区外绝对路径——
// 调用方在 ctx.Approved（管理员已同意）时可继续使用 abs。
func workspaceAbs(workspace, p string) (string, error) {
	if strings.TrimSpace(p) == "" {
		return "", errors.New("路径不能为空")
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	var abs string
	if filepath.IsAbs(p) {
		abs, err = filepath.Abs(p)
	} else {
		abs = filepath.Join(root, p)
	}
	if err != nil {
		return "", err
	}
	if !withinRoot(root, abs) {
		return abs, errOutside{path: p}
	}
	return abs, nil
}

// errOutside 路径越界（转 approvalNeeded 由上层决定审批流）。
type errOutside struct{ path string }

func (e errOutside) Error() string { return "路径在工作区外: " + e.path }

// withinRoot abs 是否位于 root 内（含 root 本身）。两端先转绝对路径再比较；
// 符号链接用 EvalSymlinks 归一（路径不存在时跳过归一）。
func withinRoot(root, abs string) bool {
	if !filepath.IsAbs(root) || !filepath.IsAbs(abs) {
		return false
	}
	root = normalizeExisting(root)
	abs = normalizeExisting(abs)
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// normalizeExisting 存在的路径经 EvalSymlinks 归一；不存在的部分逐级拼回。
func normalizeExisting(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	parent := filepath.Dir(p)
	if parent != p {
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(resolved, filepath.Base(p))
		}
	}
	return p
}

// napcatFilePath 把本地路径映射为 NapCat(WSL) 侧可读路径：
//   - Windows 盘符路径 F:\dir\a.png → /mnt/f/dir/a.png
//   - http/https/base64:///已以 / 开头的路径原样返回
//   - 相对路径相对工作区解析后再映射
//
// 反向（WSL → Windows）走 wslUncPath（\\wsl$\<distro>\...），save_message_media
// 拷贝用。
func napcatFilePath(workspace, p string) (string, error) {
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") ||
		strings.HasPrefix(p, "base64://") || strings.HasPrefix(p, "/") {
		return p, nil
	}
	if winDriveRe.MatchString(p) {
		return toWSLMount(p), nil
	}
	abs, err := workspaceAbs(workspace, p)
	if err != nil {
		return "", err
	}
	return toWSLMount(abs), nil
}

// toWSLMount F:\a\b → /mnt/f/a/b。
func toWSLMount(winPath string) string {
	p := strings.ReplaceAll(winPath, "/", "\\")
	if m := winDriveRe.FindString(p); m != "" {
		drive := strings.ToLower(m[:1]) // 盘符字母（m 形如 "F:\"）
		rest := strings.TrimPrefix(strings.TrimPrefix(p, m), "\\")
		return "/mnt/" + drive + "/" + strings.ReplaceAll(rest, `\`, "/")
	}
	return p
}

// wslUncPath /home/x → \\wsl$\<distro>\home\x（Windows 侧读 WSL 文件）。
func wslUncPath(distro, p string) string {
	return `\\wsl$\` + distro + filepath.FromSlash(p)
}

// isProbablyBinary 前 512 字节含 NUL 视为二进制（read_file 拒绝）。
func isProbablyBinary(data []byte) bool {
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	for _, b := range head {
		if b == 0 {
			return true
		}
	}
	return false
}

// ensureWorkspace 确保工作区目录存在，返回绝对路径。
func ensureWorkspace(workspace string) (string, error) {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return root, nil
}

// hostShell 构造当前系统的 shell 执行参数：Windows 用 PowerShell，
// 其余用 sh -c。
func hostShell(command string) (name string, args []string) {
	if runtime.GOOS == "windows" {
		return "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-Command", command}
	}
	return "sh", []string{"-c", command}
}

// dangerousCommandRe 明确的危险命令黑名单（命中走审批）。正则是减速带
// 不是墙：shell 本质是信任边界内的自由，全量命令都会进 eventlog 审计。
var dangerousCommandRe = regexp.MustCompile(`(?i)` +
	`shutdown|restart-computer|` +
	`format\s+[a-z]:|` +
	`mkfs|fdisk|dd\s+if=` +
	`|Remove-Item\s+(-Recurse\s+)?-Force\s+\S*:[\\/]` +
	`|rd\s+/s\s+/q|del\s+/[sf]\s|` +
	`reg(\.exe)?\s+(delete|add)\s+HK` +
	`|taskkill\s+/f|Stop-Process\s+-Force` +
	`|rm\s+-rf\s+/(usr|etc|var|home)\b` +
	`|:[(){ ]*\|.*&.*};?` + // fork bomb 形态
	`|curl[^|]*\|\s*(iex|sh|bash)|wget[^|]*\|\s*(iex|sh|bash)`)
