// Package workspacepath owns workspace boundaries and NapCat path mapping.
package workspacepath

import (
	"errors"
	"path/filepath"
	"regexp"
	"strings"
)

const DefaultDir = "agent/workspace"

var windowsDrive = regexp.MustCompile(`^[A-Za-z]:[\\/]`)

// Resolve 把 p 解析为工作区内绝对路径。相对路径相对工作区；绝对路径
// 必须落在工作区内。越界时返回 Outside 且 abs 为其工作区外绝对路径——
// 调用方在 ctx.Approved（管理员已同意）时可继续使用 abs。
func Resolve(workspace, p string) (string, error) {
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
		return abs, Outside{Path: p}
	}
	return abs, nil
}

// Outside 路径越界（转 approvalNeeded 由上层决定审批流）。
type Outside struct{ Path string }

func (e Outside) Error() string { return "路径在工作区外: " + e.Path }

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

// NapcatPath 把本地路径映射为 NapCat(WSL) 侧可读路径：
//   - Windows 盘符路径 F:\dir\a.png → /mnt/f/dir/a.png
//   - http/https/base64:///已以 / 开头的路径原样返回
//   - 相对路径相对工作区解析后再映射
//
// 反向（WSL → Windows）走 WSLPath（\\wsl$\<distro>\...），save_message_media
// 拷贝用。
func NapcatPath(workspace, p string) (string, error) {
	if strings.HasPrefix(p, "http://") || strings.HasPrefix(p, "https://") ||
		strings.HasPrefix(p, "base64://") || strings.HasPrefix(p, "/") {
		return p, nil
	}
	if windowsDrive.MatchString(p) {
		return toWSLMount(p), nil
	}
	abs, err := Resolve(workspace, p)
	if err != nil {
		return "", err
	}
	return toWSLMount(abs), nil
}

// toWSLMount F:\a\b → /mnt/f/a/b。
func toWSLMount(winPath string) string {
	p := strings.ReplaceAll(winPath, "/", "\\")
	if m := windowsDrive.FindString(p); m != "" {
		drive := strings.ToLower(m[:1]) // 盘符字母（m 形如 "F:\"）
		rest := strings.TrimPrefix(strings.TrimPrefix(p, m), "\\")
		return "/mnt/" + drive + "/" + strings.ReplaceAll(rest, `\`, "/")
	}
	return p
}

// WSLPath /home/x → \\wsl$\<distro>\home\x（Windows 侧读 WSL 文件）。
func WSLPath(distro, p string) string {
	return `\\wsl$\` + distro + filepath.FromSlash(p)
}
