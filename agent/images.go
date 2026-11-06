// agent/images.go 实现 P3-3 图片本地下载转 base64 兜底：用户 LLM 提供商服务端
// 直接下载腾讯多媒体 URL（multimedia.nt.qq.com.cn，带一次性 rkey）不可靠，
// 失败即 400 拒绝整个请求。因此在 Q-Mi 侧把图片下载为 base64 data URL 再发给
// 提供商，下载失败时回退原始 URL。
//
// 内存说明：缓存条目为完整图片字节的 base64（≈1.33×原体积，单张上限 20MB），
// 20MB×缓存条数 可接受。不做淘汰：QQ 图片 URL 天然不重复（fileid/一次性 rkey），
// 同 URL 的二次访问很少，淘汰收益有限；进程常驻场景下单实例、图片低频，进程
// 重启即清零。下载在 builder 所在 worker goroutine 内同步进行——阻塞思考循环
// 等待图片是有意行为（模型需要图片内容才能回复）。
package agent

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// maxImageBytes 单张图片下载体积上限（20MB），防止异常响应撑爆内存。
const maxImageBytes = 20 << 20

// imageCache 图片完整 URL → base64 data URL 的进程内缓存。builder 会被多会话
// worker 共享，get/put 以互斥锁保护；首次下载可能被并发 worker 重复执行
// （仅多耗一次请求，无数据竞争）。
type imageCache struct {
	mu     sync.Mutex
	m      map[string]string
	client *http.Client // 独立下载 client（非 LLM client），带总超时
}

// newImageCache 构造缓存，默认下载超时 10s。
func newImageCache() *imageCache {
	return &imageCache{
		m:      make(map[string]string),
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// newImageCacheWithClient 以指定 client 构造缓存（测试注入短超时用）。
func newImageCacheWithClient(client *http.Client) *imageCache {
	return &imageCache{
		m:      make(map[string]string),
		client: client,
	}
}

// get 查缓存，键为完整 URL。
func (c *imageCache) get(url string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[url]
	return v, ok
}

// put 写缓存，键为完整 URL。
func (c *imageCache) put(url, dataURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[url] = dataURL
}

// dataURL 返回 url 对应的 base64 data URL：命中缓存直接返回；未命中则下载，
// 成功转 data:<mime>;base64,<...> 并缓存；任何失败（超时/超限/非 2xx/内容
// 无法识别）返回原 url 且不缓存失败结果。nil 接收器与空 url 原样透传。
func (c *imageCache) dataURL(url string) string {
	if c == nil || url == "" {
		return url
	}
	if v, ok := c.get(url); ok {
		return v
	}
	data, mime, err := c.download(url)
	if err != nil {
		slog.Warn("图片下载失败，回退原始 URL", "url", url, "err", err)
		return url
	}
	du := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
	c.put(url, du)
	return du
}

// download 下载 url 并返回 (字节, MIME 类型, 错误)。独立 client（默认超时
// 10s），响应体经 http.MaxBytesReader 限 maxImageBytes；Content-Type 缺失或
// 非 image/*（如 CDN 误标 text/plain、octet-stream）时按魔数猜
// jpeg/png/gif/webp，仍无法识别视为失败。
func (c *imageCache) download(url string) ([]byte, string, error) {
	resp, err := c.client.Get(url)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", fmt.Errorf("agent: 图片下载 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(http.MaxBytesReader(nil, resp.Body, maxImageBytes))
	if err != nil {
		return nil, "", err
	}
	mime := strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0])
	if !strings.HasPrefix(mime, "image/") {
		if mime = sniffImageMIME(body); mime == "" {
			return nil, "", fmt.Errorf("agent: 无法识别图片类型 (Content-Type=%q 且魔数不匹配)", mime)
		}
	}
	return body, mime, nil
}

// sniffImageMIME 按文件头魔数简单判定图片类型；未匹配返回空串。
func sniffImageMIME(b []byte) string {
	switch {
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "image/jpeg"
	case len(b) >= 8 && bytes.Equal(b[:8], []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}):
		return "image/png"
	case len(b) >= 6 && (bytes.Equal(b[:6], []byte("GIF87a")) || bytes.Equal(b[:6], []byte("GIF89a"))):
		return "image/gif"
	case len(b) >= 12 && bytes.Equal(b[:4], []byte("RIFF")) && bytes.Equal(b[8:12], []byte("WEBP")):
		return "image/webp"
	default:
		return ""
	}
}
