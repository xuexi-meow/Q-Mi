// agent/images.go 内容寻址图片存储：磁盘 agent/images/<sha256> 为唯一事实源，
// (message_id, idx) → (sha256, mime) 映射持久化在 storage.message_images 表。
// QQ 图片 URL 是一次性临时链（rkey 随时失效），仅在映射未命中时作为下载来源。
//
// 设计要点：
//   - 消息锚定：同一张图全生命周期只下载一次，URL 失效后映射/磁盘仍然可用；
//   - 内容寻址：不同消息里的同图共享同一文件，落盘幂等（已存在跳过写）；
//   - 跨重启字节稳定：历史多模态行的 base64 不随进程重启变化 → prompt cache
//     前缀稳定（内存缓存重启清零做不到这一点）；
//   - 内存零常驻：data URL 按需读盘编码（几 MB 级，毫秒级开销），不再像旧
//     imageCache 那样把每张图的 base64 永久留在进程内存；
//   - 容量管理：目录总量超上限按 mtime 最旧优先清理（图片是不可变内容，被
//     清理后映射仍在，需要时经映射检测文件缺失重新走 URL 下载）。
//
// 负缓存：按 URL 记失败时间，TTL 内不重试——rkey 过期死链在本 TTL 内注定
// 失败，逐次白等超时没有意义；TTL 过后允许重试（可能拿到新链或网络恢复）。
// 下载在 builder 所在 worker goroutine 内同步进行——阻塞思考循环等待图片
// 是有意行为（模型需要图片内容才能回复）。
package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"Q-Mi/storage"
)

// maxImageBytes 单张图片下载体积上限（20MB），防止异常响应撑爆磁盘/内存。
const maxImageBytes = 20 << 20

// negCacheTTL URL 负缓存有效期。
const negCacheTTL = time.Hour

// maxImageDirBytes 图片目录总量上限，超出按最旧优先清理。
const maxImageDirBytes = int64(2) << 30

// pruneAfterWritten 累计写入该字节数后触发一次容量检查（避免每次下载都遍历目录）。
const pruneAfterWritten = int64(256) << 20

// imageStore 内容寻址图片存储。builder 会被多会话 worker 共享，负缓存/写盘
// 计数以互斥锁保护；磁盘写本身幂等（同哈希已存在则跳过），并发重复下载仅
// 多耗一次请求，无数据竞争。
type imageStore struct {
	dir    string
	store  *storage.Store // nil = 不持久化映射（退化为纯 URL 直连下载）
	client *http.Client   // 独立下载 client（非 LLM client），带总超时

	mu sync.Mutex
	// neg url → 最近一次下载失败时间；urlHash url → 已落盘内容哈希（无消息
	// 锚时的第二道去重：同一 URL 下载成功过一次就不再发请求）
	neg     map[string]time.Time
	urlHash map[string]string
	// writtenSincePrune 距上次容量检查的累计落盘字节数
	writtenSincePrune int64
}

// newImageStore 构造存储：确保目录存在并做一次容量清理。
func newImageStore(dir string, store *storage.Store) *imageStore {
	return newImageStoreWithClient(dir, store, &http.Client{Timeout: 10 * time.Second})
}

// newImageStoreWithClient 以指定 client 构造（测试注入短超时/桩服务器用）。
// 目录懒创建：构造不落盘，首次 save 才建目录（测试构造零副作用）。
func newImageStoreWithClient(dir string, store *storage.Store, client *http.Client) *imageStore {
	s := &imageStore{
		dir:     dir,
		store:   store,
		client:  client,
		neg:     make(map[string]time.Time),
		urlHash: make(map[string]string),
	}
	s.prune()
	return s
}

// dataURLFor 解析 (messageID, idx) 锚定的图片为 data URL：
//  1. 映射命中且文件存在 → 读盘编码返回（不发生任何网络请求）；
//  2. url 非空且不在负缓存 TTL 内 → 下载，魔数判定 MIME，内容哈希落盘并记
//     映射，编码返回；
//  3. 失败 → 记 URL 负缓存，返回原 url（调用方以 "data:" 前缀区分成败）。
//
// messageID 为 0（无锚，如临时上下文）时跳过映射，行为退化为旧 URL 直连。
// nil 接收器与空 url 原样透传。
func (s *imageStore) dataURLFor(messageID int64, idx int, url string) string {
	if s == nil || url == "" {
		return url
	}
	if s.store != nil && messageID != 0 {
		if refs, err := s.store.ImageRefs(messageID); err == nil {
			for _, ref := range refs {
				if ref.Idx != idx {
					continue
				}
				if du := s.dataURLFromRef(ref); du != "" {
					return du
				}
				break // 映射在但文件已被清理 → 落回 URL 重新下载
			}
		} else {
			slog.Warn("查询图片映射失败，本次走 URL 下载", "message_id", messageID, "err", err)
		}
	}
	if s.negFresh(url) {
		return url
	}
	if du := s.fromURLHash(url); du != "" {
		return du
	}
	data, mime, err := s.download(url)
	if err != nil {
		slog.Warn("图片下载失败，已记入负缓存", "url", url, "err", err)
		s.markNeg(url)
		return url
	}
	s.markOK(url)
	name := s.save(messageID, idx, data, mime)
	s.mu.Lock()
	s.urlHash[url] = name
	s.mu.Unlock()
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// fromURLHash URL 已下载过（哈希已知）时直接读盘，避免无消息锚场景下的重复下载。
func (s *imageStore) fromURLHash(url string) string {
	s.mu.Lock()
	name := s.urlHash[url]
	s.mu.Unlock()
	if name == "" {
		return ""
	}
	return s.dataURLFromRef(storage.ImageRef{Sha256: name})
}

// dataURLFromRef 从映射记录直接读盘编码；文件缺失/无法识别返回空串。
func (s *imageStore) dataURLFromRef(ref storage.ImageRef) string {
	if s == nil || ref.Sha256 == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(s.dir, ref.Sha256))
	if err != nil {
		return ""
	}
	mime := ref.Mime
	if mime == "" {
		mime = sniffImageMIME(data)
	}
	if mime == "" {
		return ""
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// save 内容哈希落盘（幂等）+ 写映射 + 累计写入量触发容量清理；返回内容哈希名。
func (s *imageStore) save(messageID int64, idx int, data []byte, mime string) string {
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:])
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		slog.Warn("图片缓存目录创建失败，图片将无法落盘", "dir", s.dir, "err", err)
		return name
	}
	path := filepath.Join(s.dir, name)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		tmp := path + ".tmp"
		if werr := os.WriteFile(tmp, data, 0o644); werr == nil {
			if rerr := os.Rename(tmp, path); rerr != nil {
				os.Remove(tmp)
				slog.Warn("图片落盘失败", "file", name, "err", rerr)
			}
		} else {
			slog.Warn("图片落盘失败", "file", name, "err", werr)
		}
	}
	s.mu.Lock()
	s.writtenSincePrune += int64(len(data))
	needPrune := s.writtenSincePrune >= pruneAfterWritten
	if needPrune {
		s.writtenSincePrune = 0
	}
	s.mu.Unlock()
	if needPrune {
		s.prune()
	}
	if s.store != nil && messageID != 0 {
		if err := s.store.PutImageRef(messageID, idx, name, mime); err != nil {
			slog.Warn("记录图片映射失败", "message_id", messageID, "idx", idx, "err", err)
		}
	}
	return name
}

// prune 目录总量超上限时按 mtime 最旧优先清理到上限以内。被清理文件的映射
// 仍在，下次使用经 dataURLFor 检测文件缺失后自动落回 URL 下载。
func (s *imageStore) prune() {
	entries, err := os.ReadDir(s.dir)
	if err != nil { // 目录尚不存在 = 无缓存可清
		return
	}
	type item struct {
		name string
		size int64
		mod  time.Time
	}
	var (
		items []item
		total int64
	)
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{e.Name(), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= maxImageDirBytes {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.Before(items[j].mod) })
	for _, it := range items {
		if total <= maxImageDirBytes {
			break
		}
		if err := os.Remove(filepath.Join(s.dir, it.name)); err == nil {
			total -= it.size
			slog.Info("图片缓存清理（超容量上限，最旧优先）", "file", it.name, "bytes", it.size)
		}
	}
}

// negFresh URL 负缓存是否仍在有效期内。
func (s *imageStore) negFresh(url string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.neg[url]
	return ok && time.Since(t) < negCacheTTL
}

func (s *imageStore) markNeg(url string) {
	s.mu.Lock()
	s.neg[url] = time.Now()
	s.mu.Unlock()
}

func (s *imageStore) markOK(url string) {
	s.mu.Lock()
	delete(s.neg, url)
	s.mu.Unlock()
}

// download 下载 url 并返回 (字节, MIME 类型, 错误)。独立 client（默认超时
// 10s），响应体经 http.MaxBytesReader 限 maxImageBytes。
// MIME 只信魔数不信 CDN 的 Content-Type——腾讯 CDN 会把 AVIF 等实际不支持的
// 格式误标成 image/jpeg，导致提供商以 "unsupported image" 拒绝整个请求。
// 魔数不在提供商支持的四种格式（jpeg/png/gif/webp）内一律视为失败，该图由
// 上层从上下文剔除。
func (s *imageStore) download(url string) ([]byte, string, error) {
	resp, err := s.client.Get(url)
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
	mime := sniffImageMIME(body)
	if mime == "" {
		ct := strings.TrimSpace(strings.SplitN(resp.Header.Get("Content-Type"), ";", 2)[0])
		return nil, "", fmt.Errorf("agent: 图片格式不受支持 (魔数未匹配 jpeg/png/gif/webp, Content-Type=%q)", ct)
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
