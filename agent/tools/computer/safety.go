package computer

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
)

const (
	maxFileReadBytes  = 64 << 10
	maxFileWriteBytes = 2 << 20
	maxDownloadBytes  = 200 << 20
)

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
