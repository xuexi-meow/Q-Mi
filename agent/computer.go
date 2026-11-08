// computer.go 电脑操作板块：文件读写/目录/下载/命令，全部以工作区为围栏。
// 权限模型（spec 权限模型拍板）：
//   - 工作区内操作与非危险命令：自动执行，无需申请
//   - 工作区外操作 / 危险命令：返回 approvalNeeded 信号 → 管理员 QQ 收到
//     「[操作审批 #id]」，回复「同意 <id>」后后台执行并投递结果
//   - 全部命令进 eventlog 审计
//
// 媒体进出：download_file / save_message_media 落到工作区；send_message 的
// image/file/video 段与 upload_group_file 自动把工作区路径映射为 NapCat(WSL)
// 侧 /mnt/<盘符>/...，模型不需要知道 WSL 的存在。
package agent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"Q-Mi/api"
)

// maxCommandOutputBytes run_command 输出回填上限（进思维链，必须限量）。
const maxCommandOutputBytes = 4 << 10

// defaultCommandTimeoutSeconds run_command 缺省超时。
const defaultCommandTimeoutSeconds = 30

// maxCommandTimeoutSeconds run_command 超时上限。
const maxCommandTimeoutSeconds = 600

// ws 工具侧的工作区便捷取值（ctx.Workspace 空时用缺省）。
func ws(ctx *ToolContext) string {
	if ctx.Workspace != "" {
		return ctx.Workspace
	}
	return defaultWorkspaceDir
}

// computerTools 电脑操作板块。ctx.Approved=true 时（管理员已同意）工具直接
// 放行工作区外路径。
func computerTools(ctx *ToolContext) []Tool {
	return []Tool{
		{
			Name:        "read_file",
			Description: "读工作区里的一个文本文件（默认上限 64KB，超出截断）。二进制文件（图片/视频等）会拒绝——媒体走 save_message_media / view_message_images。",
			Parameters: objectParams(map[string]any{
				"path":      strProp("文件路径（相对工作区，或工作区内绝对路径）"),
				"max_bytes": intProp("可选，读取上限字节，默认 65536"),
			}, []string{"path"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Path     string `json:"path"`
					MaxBytes int    `json:"max_bytes"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				abs, err := workspaceAbs(ws(ctx), p.Path)
				if err != nil {
					if !ctx.Approved {
						return "", approvalNeeded{err.Error()}
					}
					if _, outside := err.(errOutside); !outside {
						return "", err
					}
				}
				data, err := os.ReadFile(abs)
				if err != nil {
					return "", err
				}
				if isProbablyBinary(data) {
					return "", fmt.Errorf("%s 疑似二进制文件，文本工具读不了；图片/视频走对应媒体工具", p.Path)
				}
				max := p.MaxBytes
				if max <= 0 {
					max = maxFileReadBytes
				}
				truncated := false
				if len(data) > max {
					data = data[:max]
					truncated = true
				}
				out := map[string]any{"content": string(data), "bytes": len(data)}
				if truncated {
					out["truncated"] = true
				}
				return marshalCompact(out)
			},
		},
		{
			Name:        "write_file",
			Description: "把文本内容写到工作区里的一个文件（覆盖写，目录自动创建；上限 2MB）。需要生成脚本、配置、笔记文件时用。",
			Parameters: objectParams(map[string]any{
				"path":    strProp("文件路径（相对工作区，或工作区内绝对路径）"),
				"content": strProp("要写入的文本内容"),
			}, []string{"path", "content"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Path    string `json:"path"`
					Content string `json:"content"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				if len(p.Content) > maxFileWriteBytes {
					return "", fmt.Errorf("内容 %d 字节超过单次写入上限 %d", len(p.Content), maxFileWriteBytes)
				}
				abs, err := workspaceAbs(ws(ctx), p.Path)
				if err != nil {
					if !ctx.Approved {
						return "", approvalNeeded{err.Error()}
					}
					if _, outside := err.(errOutside); !outside {
						return "", err
					}
				}
				if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
					return "", err
				}
				if err := os.WriteFile(abs, []byte(p.Content), 0o644); err != nil {
					return "", err
				}
				return marshalCompact(map[string]any{"written": true, "bytes": len(p.Content)})
			},
		},
		{
			Name:        "list_dir",
			Description: "列工作区里某个目录的内容（名字/类型/大小）。",
			Parameters: objectParams(map[string]any{
				"path": strProp("目录路径（相对工作区，空 = 工作区根）"),
			}, nil),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Path string `json:"path"`
				}
				_ = decodeArgs(args, &p)
				abs, err := workspaceAbs(ws(ctx), p.Path)
				if err != nil {
					if !ctx.Approved {
						return "", approvalNeeded{err.Error()}
					}
					if _, outside := err.(errOutside); !outside {
						return "", err
					}
				}
				entries, err := os.ReadDir(abs)
				if err != nil {
					return "", err
				}
				rows := make([]map[string]any, 0, len(entries))
				for _, e := range entries {
					row := map[string]any{"name": e.Name(), "dir": e.IsDir()}
					if !e.IsDir() {
						if info, ierr := e.Info(); ierr == nil {
							row["bytes"] = info.Size()
						}
					}
					rows = append(rows, row)
				}
				return marshalCompact(rows)
			},
		},
		{
			Name:        "delete_file",
			Description: "删除工作区里的一个文件（不递归目录）。清理下载/生成的临时文件用。",
			Parameters: objectParams(map[string]any{
				"path": strProp("文件路径（相对工作区）"),
			}, []string{"path"}),
			Run: func(args json.RawMessage) (string, error) {
				var p struct {
					Path string `json:"path"`
				}
				if err := decodeArgs(args, &p); err != nil {
					return "", err
				}
				abs, err := workspaceAbs(ws(ctx), p.Path)
				if err != nil {
					if !ctx.Approved {
						return "", approvalNeeded{err.Error()}
					}
					if _, outside := err.(errOutside); !outside {
						return "", err
					}
				}
				return "{}", os.Remove(abs)
			},
		},
		{
			Name:        "download_file",
			Description: "把一个 http/https 链接下载到工作区（单文件上限 200MB），返回本地路径与 NapCat 侧路径——后者可直接给 send_message 的 image/video/file 段或 upload_group_file 使用。",
			Parameters: objectParams(map[string]any{
				"url":      strProp("下载链接（http/https）"),
				"filename": strProp("可选，保存的文件名（缺省从 URL 推断）"),
			}, []string{"url"}),
			Usage:     "下载耗时与体积相关，大文件建议 `\"_async\": true` 后台执行，完成自动送达结果。",
			Asyncable: true,
			Run:       func(args json.RawMessage) (string, error) { return runDownload(ctx, args) },
		},
		{
			Name:        "run_command",
			Description: "在电脑上执行一条命令（Windows 走 PowerShell，超时后强制结束），返回 stdout+stderr（上限 4KB）。全部命令进审计日志；命中危险黑名单或工作目录在工作区外会转管理员审批。",
			Parameters: objectParams(map[string]any{
				"command":         strProp("要执行的命令"),
				"timeout_seconds": intProp("可选，超时秒数，默认 30，最大 600"),
				"cwd":             strProp("可选，工作目录（默认工作区；工作区外需管理员审批）"),
			}, []string{"command"}),
			Usage:     "长命令（构建/批量转码）建议 `\"_async\": true` 后台执行。\n错误形态：超时会杀掉进程并返回已有输出；命中危险黑名单 → 转管理员审批，管理员回复后自动执行并投递结果。",
			Asyncable: true,
			Run: func(args json.RawMessage) (string, error) {
				return runCommand(ctx, args)
			},
		},
		{
			Name:        "save_message_media",
			Description: "把一条消息里的媒体（图片/视频/文件/语音）保存到工作区。图片走本地缓存直接落盘；视频/文件经 NapCat 下载后拷贝（跨 WSL 需要几秒）。返回路径与 NapCat 侧路径，之后可转发/上传/用工具处理。",
			Parameters: objectParams(map[string]any{
				"message_id": intProp("消息 ID"),
				"seg_index":  intProp("可选，该消息内媒体段序号（从 0 开始，默认第一个媒体段）"),
				"filename":   strProp("可选，保存的文件名"),
			}, []string{"message_id"}),
			Usage:     "大视频下载耗时较长，建议 `\"_async\": true` 后台执行。",
			Asyncable: true,
			Run: func(args json.RawMessage) (string, error) {
				return runSaveMedia(ctx, args)
			},
		},
	}
}

// runCommand run_command 实现：危险黑名单/越界 cwd → 审批；PowerShell/sh
// 执行，输出限量回填。
func runCommand(ctx *ToolContext, args json.RawMessage) (string, error) {
	var p struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		Cwd            string `json:"cwd"`
	}
	if err := decodeArgs(args, &p); err != nil {
		return "", err
	}
	if strings.TrimSpace(p.Command) == "" {
		return "", fmt.Errorf("缺少必填参数 command")
	}
	if dangerousCommandRe.MatchString(p.Command) {
		return "", approvalNeeded{reason: "命令命中危险黑名单"}
	}
	cwd := ws(ctx)
	if strings.TrimSpace(p.Cwd) != "" {
		abs, err := workspaceAbs(ws(ctx), p.Cwd)
		if err != nil {
			if !ctx.Approved {
				return "", approvalNeeded{err.Error()}
			}
			if _, outside := err.(errOutside); !outside {
				return "", err
			}
		}
		cwd = abs
	}
	timeout := time.Duration(p.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultCommandTimeoutSeconds * time.Second
	}
	if timeout > maxCommandTimeoutSeconds*time.Second {
		timeout = maxCommandTimeoutSeconds * time.Second
	}

	// ctx.Approved=true 时本调用已经过管理员同意，直接放行；否则此处即为
	// 自动放行路径（非危险命令）。
	name, shellArgs := hostShell(p.Command)
	cmdCtx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, name, shellArgs...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	truncated := false
	if len(out) > maxCommandOutputBytes {
		out = out[:maxCommandOutputBytes]
		truncated = true
	}
	resp := map[string]any{
		"output":    string(out),
		"exit_code": exitCode(err),
	}
	if truncated {
		resp["truncated"] = true
	}
	if cmdCtx.Err() == context.DeadlineExceeded {
		resp["timeout"] = true
	}
	return marshalCompact(resp)
}

// exitCode 从 exec 错误提取退出码；被信号杀死等返回 -1。
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errorsAs(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// errorsAs 薄包装（避免每处 import errors）。
func errorsAs(err error, target any) bool {
	return errors.As(err, target)
}

// runDownload download_file 实现：只落工作区，超限拒绝。
func runDownload(ctx *ToolContext, args json.RawMessage) (string, error) {
	var p struct {
		URL      string `json:"url"`
		Filename string `json:"filename"`
	}
	if err := decodeArgs(args, &p); err != nil {
		return "", err
	}
	if !strings.HasPrefix(p.URL, "http://") && !strings.HasPrefix(p.URL, "https://") {
		return "", fmt.Errorf("url 必须是 http/https 链接")
	}
	root, err := ensureWorkspace(ws(ctx))
	if err != nil {
		return "", err
	}
	name := p.Filename
	if strings.TrimSpace(name) == "" {
		name = filepath.Base(strings.SplitN(p.URL, "?", 2)[0])
		if name == "" || name == "/" {
			name = fmt.Sprintf("download_%d", time.Now().Unix())
		}
	}
	name = filepath.Base(name) // 防目录穿越
	abs := filepath.Join(root, name)

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(p.URL)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("下载 HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxDownloadBytes {
		return "", fmt.Errorf("文件 %d 字节超过单文件上限 %d", resp.ContentLength, maxDownloadBytes)
	}
	f, err := os.Create(abs)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(resp.Body, maxDownloadBytes+1))
	closeErr := f.Close()
	if err != nil {
		os.Remove(abs)
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if n > maxDownloadBytes {
		os.Remove(abs)
		return "", fmt.Errorf("文件超过单文件上限 %d", maxDownloadBytes)
	}
	napcatPath, _ := napcatFilePath(ws(ctx), abs)
	return marshalCompact(map[string]any{
		"path": abs, "napcat_path": napcatPath, "bytes": n,
	})
}

// runSaveMedia save_message_media 实现：图片走 imageStore 缓存字节直落；
// 视频/文件/语音走 NapCat get_file 再经 \\wsl$ 拷贝。
func runSaveMedia(ctx *ToolContext, args json.RawMessage) (string, error) {
	if ctx.Store == nil {
		return "", fmt.Errorf("记忆库未启用，无法定位消息")
	}
	var p struct {
		MessageID int64  `json:"message_id"`
		SegIndex  int    `json:"seg_index"`
		Filename  string `json:"filename"`
	}
	if err := decodeArgs(args, &p); err != nil {
		return "", err
	}
	rec, err := ctx.Store.GetMessage(p.MessageID)
	if err != nil {
		return "", fmt.Errorf("查询消息 %d 失败: %w", p.MessageID, err)
	}
	if rec == nil {
		return "", fmt.Errorf("没有找到消息 %d", p.MessageID)
	}
	var segs []struct {
		Type string `json:"type"`
		Data struct {
			URL     string `json:"url"`
			File    string `json:"file"`
			FileID  string `json:"file_id"`
			FileID2 string `json:"file_id2"`
			Name    string `json:"name"`
		} `json:"data"`
	}
	if len(rec.Segments) > 0 {
		if err := json.Unmarshal(rec.Segments, &segs); err != nil {
			return "", fmt.Errorf("解析消息段失败: %w", err)
		}
	}
	type mediaSeg struct {
		idx    int
		kind   string // image / video / file / record
		url    string
		fileID string
		name   string
	}
	var medias []mediaSeg
	idx := 0
	for _, seg := range segs {
		switch seg.Type {
		case "image", "video", "file", "record":
			medias = append(medias, mediaSeg{
				idx: idx, kind: seg.Type,
				url: seg.Data.URL, fileID: firstNonEmpty([]string{seg.Data.FileID, seg.Data.FileID2, seg.Data.File}),
				name: seg.Data.Name,
			})
		}
		idx++
	}
	if len(medias) == 0 {
		return "", fmt.Errorf("消息 %d 里没有可保存的媒体段", p.MessageID)
	}
	if p.SegIndex < 0 || p.SegIndex >= len(medias) {
		p.SegIndex = 0
	}
	seg := medias[p.SegIndex]

	root, err := ensureWorkspace(ws(ctx))
	if err != nil {
		return "", err
	}
	name := p.Filename
	if strings.TrimSpace(name) == "" {
		name = seg.name
	}

	switch seg.kind {
	case "image":
		if ctx.Images == nil {
			return "", fmt.Errorf("图片存储不可用")
		}
		du := ctx.Images.dataURLFor(p.MessageID, seg.idx, seg.url)
		if !strings.HasPrefix(du, "data:") {
			return "", fmt.Errorf("图片下载失败（链接可能已过期）")
		}
		payload := du[strings.Index(du, ",")+1:]
		data, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return "", err
		}
		if name == "" {
			name = fmt.Sprintf("msg_%d_img%d.png", p.MessageID, seg.idx)
		}
		abs := filepath.Join(root, filepath.Base(name))
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			return "", err
		}
		napcatPath, _ := napcatFilePath(ws(ctx), abs)
		return marshalCompact(map[string]any{"path": abs, "napcat_path": napcatPath, "bytes": len(data), "kind": "image"})
	default:
		// video/file/record：NapCat get_file 换取本地路径，再经 \\wsl$ 拷贝
		data, err := callNapCat(&api.GetFile{FileID: seg.fileID})
		if err != nil {
			return "", fmt.Errorf("NapCat get_file 失败: %w", err)
		}
		src := downloadFilePath(data)
		if src == "" {
			return "", fmt.Errorf("get_file 未返回文件路径: %s", data)
		}
		if name == "" {
			name = filepath.Base(src)
		}
		abs := filepath.Join(root, filepath.Base(name))
		if err := copyFile(wslToLocal(src), abs); err != nil {
			return "", fmt.Errorf("拷贝媒体失败（%s → 工作区）: %w", src, err)
		}
		var bytes int64
		if info, ierr := os.Stat(abs); ierr == nil {
			bytes = info.Size()
		}
		napcatPath := src // NapCat 侧本来就能读自己的文件
		if winDriveRe.MatchString(src) {
			napcatPath, _ = napcatFilePath(ws(ctx), src)
		}
		return marshalCompact(map[string]any{
			"path": abs, "napcat_path": napcatPath,
			"bytes": bytes, "kind": seg.kind, "source": src,
		})
	}
}

// wslToLocal NapCat 侧路径 → Windows 侧可读路径：WSL 路径走 \\wsl$\<distro>，
// /mnt/<盘>/x → <盘>:\x，Windows 路径原样。distro 取环境变量 SNOWLUMA_WSL_DISTRO
// （缺省 Ubuntu）。
func wslToLocal(p string) string {
	if winDriveRe.MatchString(p) {
		return filepath.FromSlash(strings.ReplaceAll(p, "/", "\\"))
	}
	if m := regexp.MustCompile(`^/mnt/([a-z])/`).FindStringSubmatch(p); m != nil {
		rest := strings.TrimPrefix(p, "/mnt/"+m[1])
		return strings.ToUpper(m[1]) + ":" + filepath.FromSlash(rest)
	}
	if strings.HasPrefix(p, "/") {
		distro := os.Getenv("SNOWLUMA_WSL_DISTRO")
		if distro == "" {
			distro = "Ubuntu"
		}
		return wslUncPath(distro, p)
	}
	return p
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(list []string) string {
	for _, s := range list {
		if s != "" {
			return s
		}
	}
	return ""
}

// copyFile 拷贝文件（save_message_media 跨 \wsl$ 用）。
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}
