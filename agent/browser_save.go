// browser_save 截图存盘工具：与 browser_screenshot 共用 captureBrowser，
// 区别是不把图片附进上下文（不烧视觉 token），而是写成工作区里的 PNG 文件，
// 返回本地路径与 NapCat 侧路径，便于后续 send_message / upload_group_file。
package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// defaultScreenshotName 自动文件名：browser-日期-时间.毫秒.png。
func defaultScreenshotName(now time.Time) string {
	return "browser-" + now.Format("20060102-150405.000") + ".png"
}

// resolveScreenshotPath 解析保存路径：
//   - 相对路径相对工作区；空串视为工作区根目录
//   - 以 / 或 \ 结尾、或已存在的目录 → 目录内自动命名
//   - 无扩展名 → 补 .png；其他扩展名 → 归一为 .png（实际编码固定 PNG）
//
// 工作区外返回 errOutside（abs 为可用的工作区外绝对路径），由调用方决定审批。
func resolveScreenshotPath(ctx *ToolContext, raw string, now time.Time) (string, error) {
	workspace := ws(ctx)
	p := strings.TrimSpace(raw)
	if p == "" {
		p = "."
	}
	abs, absErr := workspaceAbs(workspace, p)
	if absErr != nil {
		var outside errOutside
		if !errors.As(absErr, &outside) {
			return "", absErr
		}
	}

	dirHint := strings.HasSuffix(p, "/") || strings.HasSuffix(p, "\\")
	if info, statErr := os.Stat(abs); statErr == nil && info.IsDir() {
		dirHint = true
	}
	switch {
	case dirHint:
		abs = filepath.Join(abs, defaultScreenshotName(now))
	case filepath.Ext(abs) == "":
		abs += ".png"
	case !strings.EqualFold(filepath.Ext(abs), ".png"):
		abs = strings.TrimSuffix(abs, filepath.Ext(abs)) + ".png"
	}
	if absErr != nil {
		return abs, absErr
	}
	return abs, nil
}

func browserSaveScreenshotTool(ctx *ToolContext) Tool {
	return Tool{
		Name:        "browser_save_screenshot",
		Description: "把当前页面截图保存为 PNG 文件（默认写工作区，可指定路径；工作区外需管理员审批）。不附图、不消耗视觉上下文，返回本地路径和 napcat_path（可直接给 send_message 的 image 段或 upload_group_file）。",
		Parameters: objectParams(map[string]any{
			"path":      strProp("保存路径：相对工作区；以 / 结尾或指向已存在目录时自动命名 browser-时间戳.png；无扩展名自动补 .png；工作区外需审批"),
			"full_page": boolProp("整页截图；默认只截当前视口"),
		}, []string{"path"}),
		Run: func(args json.RawMessage) (string, error) {
			var p struct {
				Path     string `json:"path"`
				FullPage bool   `json:"full_page"`
			}
			if err := decodeArgs(args, &p); err != nil {
				return "", err
			}
			if ctx.Browser == nil {
				return "", errors.New("浏览器不可用")
			}

			now := time.Now()
			abs, pathErr := resolveScreenshotPath(ctx, p.Path, now)
			if pathErr != nil {
				if !ctx.Approved {
					return "", approvalNeeded{pathErr.Error()}
				}
				if _, outside := pathErr.(errOutside); !outside {
					return "", pathErr
				}
			}

			meta, buf, err := captureBrowser(ctx, p.FullPage, false)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
				return "", fmt.Errorf("创建目录失败: %w", err)
			}
			overwritten := false
			if _, statErr := os.Stat(abs); statErr == nil {
				overwritten = true
			}
			if err := os.WriteFile(abs, buf, 0o644); err != nil {
				return "", fmt.Errorf("写入截图失败: %w", err)
			}

			rel := abs
			if root, rootErr := filepath.Abs(ws(ctx)); rootErr == nil {
				if r, relErr := filepath.Rel(root, abs); relErr == nil && !strings.HasPrefix(r, "..") {
					rel = filepath.ToSlash(r)
				}
			}
			out := map[string]any{
				"saved": true, "path": rel, "absolute": abs,
				"bytes": meta["bytes"], "mime": "image/png",
				"image_width": meta["image_width"], "image_height": meta["image_height"],
				"viewport_width": meta["viewport_width"], "viewport_height": meta["viewport_height"],
				"url": meta["url"], "full_page": p.FullPage,
				"tab_id": meta["tab_id"], "snapshot_id": meta["snapshot_id"],
				"overwritten": overwritten, "captured_at": meta["captured_at"],
			}
			if np, npErr := napcatFilePath(ws(ctx), abs); npErr == nil && np != "" {
				out["napcat_path"] = np
			}
			return marshalCompact(out)
		},
	}
}
