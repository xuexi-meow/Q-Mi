package agent

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// pngBytes 一段带 PNG 魔数的假图字节（仅用于下载/编码断言，无需完整合法图片）。
var pngBytes = append(
	[]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 'I', 'H', 'D', 'R'},
	make([]byte, 16)...,
)

// pngDataURL pngBytes 对应的期望 data URL。
var pngDataURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)

// servePNG 起一个固定返回 pngBytes 的假图床；contentType 为空时不发
// Content-Type 头（测魔数嗅探）。返回 server 与请求计数。
func servePNG(t *testing.T, contentType string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		_, _ = w.Write(pngBytes)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// TestImageCachePutGet 缓存基础读写：未 put 不命中，put 后命中且值一致。
func TestImageCachePutGet(t *testing.T) {
	c := newImageCache()
	if _, ok := c.get("https://img.example/1"); ok {
		t.Error("未 put 时 get 不应命中")
	}
	c.put("https://img.example/1", pngDataURL)
	v, ok := c.get("https://img.example/1")
	if !ok || v != pngDataURL {
		t.Errorf("get = (%q, %v), want (%q, true)", v, ok, pngDataURL)
	}
}

// TestImageCacheDataURLSuccess 成功下载 → 返回 data:image/png;base64 前缀的
// data URL，且 base64 解码后与响应体完全一致。
func TestImageCacheDataURLSuccess(t *testing.T) {
	srv, _ := servePNG(t, "image/png")
	c := newImageCache()

	got := c.dataURL(srv.URL + "/a.png")

	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Fatalf("dataURL 前缀不符: %q", got)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got, "data:image/png;base64,"))
	if err != nil {
		t.Fatalf("base64 解码失败: %v", err)
	}
	if !bytes.Equal(raw, pngBytes) {
		t.Errorf("data URL 载荷与响应体不一致: got %d bytes, want %d bytes", len(raw), len(pngBytes))
	}
	if got != pngDataURL {
		t.Errorf("dataURL = %q, want %q", got, pngDataURL)
	}
}

// TestImageCacheDataURLCachesSingleRequest 同一 url 两次调用只发一次 HTTP
// 请求（计数器），两次结果一致。
func TestImageCacheDataURLCachesSingleRequest(t *testing.T) {
	srv, hits := servePNG(t, "image/png")
	c := newImageCache()
	u := srv.URL + "/a.png"

	first := c.dataURL(u)
	second := c.dataURL(u)

	if first != second {
		t.Errorf("两次结果不一致: %q vs %q", first, second)
	}
	if hits.Load() != 1 {
		t.Errorf("HTTP 请求数 = %d, want 1 (第二次应命中缓存)", hits.Load())
	}
}

// TestImageCacheDataURLServerErrorFallsBackAndNotCached 非 2xx（500）→ 返回
// 原 url；失败结果不缓存——服务恢复后再次调用能成功下载。
func TestImageCacheDataURLServerErrorFallsBackAndNotCached(t *testing.T) {
	var failing atomic.Bool
	failing.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing.Load() {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(pngBytes)
	}))
	t.Cleanup(srv.Close)
	c := newImageCache()
	u := srv.URL + "/a.png"

	if got := c.dataURL(u); got != u {
		t.Errorf("500 时应回退原 url, got %q", got)
	}

	failing.Store(false)
	if got := c.dataURL(u); got != pngDataURL {
		t.Errorf("失败结果不应被缓存, 服务恢复后应成功: got %q", got)
	}
}

// TestImageCacheDataURLTimeoutFallsBack 下载超时（服务端 sleep 100ms，
// client 超时 30ms）→ 回退原 url，不缓存失败结果。
func TestImageCacheDataURLTimeoutFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write(pngBytes)
	}))
	t.Cleanup(srv.Close)
	c := newImageCacheWithClient(&http.Client{Timeout: 30 * time.Millisecond})
	u := srv.URL + "/slow.png"

	if got := c.dataURL(u); got != u {
		t.Errorf("超时应回退原 url, got %q", got)
	}
}

// TestImageCacheDataURLSniffsMagicNumber 响应无 Content-Type 时按魔数识别
// （PNG 头 → image/png）。
func TestImageCacheDataURLSniffsMagicNumber(t *testing.T) {
	srv, _ := servePNG(t, "")
	c := newImageCache()

	got := c.dataURL(srv.URL + "/raw")

	if got != pngDataURL {
		t.Errorf("无 Content-Type 应按魔数识别为 image/png, got %q", got)
	}
}

// TestImageCacheDataURLUnknownBodyFallsBack 无 Content-Type 且魔数无法识别
// → 回退原 url。
func TestImageCacheDataURLUnknownBodyFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain text, not an image"))
	}))
	t.Cleanup(srv.Close)
	c := newImageCache()
	u := srv.URL + "/x"

	if got := c.dataURL(u); got != u {
		t.Errorf("无法识别的响应体应回退原 url, got %q", got)
	}
}

// TestImageCacheDataURLRejectsOversize 超过 20MB 上限的响应 → 回退原 url。
func TestImageCacheDataURLRejectsOversize(t *testing.T) {
	big := make([]byte, maxImageBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(big)
	}))
	t.Cleanup(srv.Close)
	c := newImageCache()
	u := srv.URL + "/big.png"

	if got := c.dataURL(u); got != u {
		t.Errorf("超限响应应回退原 url, got %q", got)
	}
}

// TestImageCacheDataURLConcurrentSameResult 多 goroutine 并发对同一 url 调用
// dataURL（-race 下验证并发安全），所有结果一致且为成功 data URL。
func TestImageCacheDataURLConcurrentSameResult(t *testing.T) {
	srv, _ := servePNG(t, "image/png")
	c := newImageCache()
	u := srv.URL + "/c.png"

	const n = 16
	results := make([]string, n)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = c.dataURL(u)
		}()
	}
	wg.Wait()

	for i, r := range results {
		if r != pngDataURL {
			t.Errorf("results[%d] = %q, want 成功 data URL", i, r)
		}
	}
}

// TestDataURLRejectsHeaderLies CDN 把 AVIF（魔数 ftyp+avif）误标成 image/jpeg
// 时必须拒绝——只信魔数不信 header；否则该图会以 "unsupported image" 拖垮
// 整个请求（线上实测）。
func TestDataURLRejectsHeaderLies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("\x00\x00\x00\x20ftypavif\x00\x00\x00\x00avifmif1fake-payload"))
	}))
	defer srv.Close()
	c := newImageCacheWithClient(srv.Client())
	if got := c.dataURL(srv.URL + "/a.jpg"); !strings.HasPrefix(got, "http") {
		t.Fatalf("AVIF 伪装 jpeg 应下载失败回退原 URL, got %q", got[:40])
	}
	// 真 jpeg 带 octet-stream 头应凭魔数成功
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("\xFF\xD8\xFF\xE0jpeg-bytes"))
	}))
	defer ok.Close()
	c2 := newImageCacheWithClient(ok.Client())
	if got := c2.dataURL(ok.URL + "/b.jpg"); !strings.HasPrefix(got, "data:image/jpeg;base64,") {
		t.Fatalf("真 jpeg 应凭魔数识别成功, got %q", got[:40])
	}
}
