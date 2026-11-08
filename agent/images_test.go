package agent

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"Q-Mi/storage"
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

// testImageStore 内容寻址存储（内存库 + 临时目录）。
func testImageStore(t *testing.T) (*imageStore, *storage.Store) {
	t.Helper()
	s := openTestStore(t)
	return newImageStore(t.TempDir(), s), s
}

// TestImageStoreDataURLForDownloadsAndSaves 首次解析：下载 → 落盘 → 写映射 →
// 返回 data URL；base64 解码后与响应体一致。
func TestImageStoreDataURLForDownloadsAndSaves(t *testing.T) {
	srv, _ := servePNG(t, "image/png")
	ims, st := testImageStore(t)

	got := ims.dataURLFor(42, 0, srv.URL+"/a.png")

	if got != pngDataURL {
		t.Fatalf("dataURL = %q, want %q", got, pngDataURL)
	}
	refs, err := st.ImageRefs(42)
	if err != nil || len(refs) != 1 {
		t.Fatalf("ImageRefs(42) = %v, %v, want 1 条映射", refs, err)
	}
	if refs[0].Idx != 0 || refs[0].Mime != "image/png" {
		t.Errorf("映射 = %+v, want idx=0 mime=image/png", refs[0])
	}
	data, err := os.ReadFile(filepath.Join(ims.dir, refs[0].Sha256))
	if err != nil {
		t.Fatalf("落盘文件读取失败: %v", err)
	}
	if string(data) != string(pngBytes) {
		t.Errorf("落盘内容与响应体不一致: got %d bytes, want %d bytes", len(data), len(pngBytes))
	}
}

// TestImageStoreDataURLForAnchoredNoSecondRequest 同一 (message_id, idx) 再次
// 解析（即使 URL 已换新链）命中映射/磁盘，零 HTTP 请求。
func TestImageStoreDataURLForAnchoredNoSecondRequest(t *testing.T) {
	srv, hits := servePNG(t, "image/png")
	ims, _ := testImageStore(t)

	first := ims.dataURLFor(42, 0, srv.URL+"/a.png")
	second := ims.dataURLFor(42, 0, srv.URL+"/new-rkey.png") // 换了 rkey 的新链

	if first != second || first != pngDataURL {
		t.Errorf("锚定解析结果不一致: %q vs %q", first, second)
	}
	if hits.Load() != 1 {
		t.Errorf("HTTP 请求数 = %d, want 1（第二次应命中映射，哪怕 URL 变了）", hits.Load())
	}
}

// TestImageStoreDataURLForSameURLNoSecondRequest 无消息锚（messageID=0）时
// 同一 URL 经 url→哈希 去重，也只下载一次。
func TestImageStoreDataURLForSameURLNoSecondRequest(t *testing.T) {
	srv, hits := servePNG(t, "image/png")
	ims, _ := testImageStore(t)

	ims.dataURLFor(0, 0, srv.URL+"/a.png")
	again := ims.dataURLFor(0, 0, srv.URL+"/a.png")

	if again != pngDataURL {
		t.Errorf("无锚解析 = %q, want data URL", again)
	}
	if hits.Load() != 1 {
		t.Errorf("HTTP 请求数 = %d, want 1（同 URL 应去重）", hits.Load())
	}
}

// TestImageStoreSurvivesRestart 重启（新 imageStore 实例 + 同目录同库）后
// 映射/磁盘仍命中，零下载——历史多模态字节跨重启稳定。
func TestImageStoreSurvivesRestart(t *testing.T) {
	srv, hits := servePNG(t, "image/png")
	dir := t.TempDir()
	st := openTestStore(t)
	ims := newImageStore(dir, st)
	want := ims.dataURLFor(7, 1, srv.URL+"/a.png")

	reborn := newImageStore(dir, st)
	got := reborn.dataURLFor(7, 1, srv.URL+"/a.png")

	if got != want || got != pngDataURL {
		t.Errorf("重启后解析 = %q, want %q", got, want)
	}
	if hits.Load() != 1 {
		t.Errorf("HTTP 请求数 = %d, want 1（重启后应命中磁盘，不再下载）", hits.Load())
	}
}

// TestImageStoreDedupsSameContent 不同消息/不同 URL 传同一张图（字节相同）
// 只落一个文件（内容寻址）。
func TestImageStoreDedupsSameContent(t *testing.T) {
	srv, hits := servePNG(t, "image/png")
	ims, _ := testImageStore(t)

	ims.dataURLFor(1, 0, srv.URL+"/one.png")
	ims.dataURLFor(2, 0, srv.URL+"/two.png")

	entries, err := os.ReadDir(ims.dir)
	if err != nil {
		t.Fatalf("读目录失败: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("目录文件数 = %d, want 1（同内容共享同一文件）", len(entries))
	}
	if hits.Load() != 2 {
		t.Errorf("HTTP 请求数 = %d, want 2（不同 URL 无法预知同内容，各下载一次）", hits.Load())
	}
}

// TestImageStoreDataURLForNegativeCache 失败结果进 URL 负缓存：TTL 内同一 URL
// 不再发 HTTP；TTL 过后允许重试。
func TestImageStoreDataURLForNegativeCache(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	ims, _ := testImageStore(t)
	u := srv.URL + "/a.png"

	if got := ims.dataURLFor(1, 0, u); got != u {
		t.Errorf("失败应回退原 url, got %q", got)
	}
	if got := ims.dataURLFor(1, 0, u); got != u {
		t.Errorf("负缓存命中仍应回退原 url, got %q", got)
	}
	if hits.Load() != 1 {
		t.Errorf("HTTP 请求数 = %d, want 1（失败已进负缓存）", hits.Load())
	}
	// TTL 过后允许重试（仍失败，但确实重新请求了）
	ims.mu.Lock()
	ims.neg[u] = time.Now().Add(-2 * negCacheTTL)
	ims.mu.Unlock()
	_ = ims.dataURLFor(1, 0, u)
	if hits.Load() != 2 {
		t.Errorf("TTL 过后应重试，HTTP 请求数 = %d, want 2", hits.Load())
	}
}

// TestImageStoreDataURLForTimeoutFallsBack 下载超时（服务端 sleep 100ms，
// client 超时 30ms）→ 回退原 url。
func TestImageStoreDataURLForTimeoutFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write(pngBytes)
	}))
	t.Cleanup(srv.Close)
	ims := newImageStoreWithClient(t.TempDir(), nil, &http.Client{Timeout: 30 * time.Millisecond})

	if got := ims.dataURLFor(0, 0, srv.URL+"/slow.png"); got != srv.URL+"/slow.png" {
		t.Errorf("超时应回退原 url, got %q", got)
	}
}

// TestImageStoreDataURLForSniffsMagicNumber 响应无 Content-Type 时按魔数识别
// （PNG 头 → image/png）。
func TestImageStoreDataURLForSniffsMagicNumber(t *testing.T) {
	srv, _ := servePNG(t, "")
	ims, _ := testImageStore(t)

	if got := ims.dataURLFor(0, 0, srv.URL+"/raw"); got != pngDataURL {
		t.Errorf("无 Content-Type 应按魔数识别为 image/png, got %q", got)
	}
}

// TestImageStoreDataURLForUnknownBodyFallsBack 无 Content-Type 且魔数无法识别
// → 回退原 url。
func TestImageStoreDataURLForUnknownBodyFallsBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("plain text, not an image"))
	}))
	t.Cleanup(srv.Close)
	ims, _ := testImageStore(t)

	if got := ims.dataURLFor(0, 0, srv.URL+"/x"); got != srv.URL+"/x" {
		t.Errorf("无法识别的响应体应回退原 url, got %q", got)
	}
}

// TestImageStoreDataURLForRejectsOversize 超过 20MB 上限的响应 → 回退原 url。
func TestImageStoreDataURLForRejectsOversize(t *testing.T) {
	big := make([]byte, maxImageBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(big)
	}))
	t.Cleanup(srv.Close)
	ims, _ := testImageStore(t)

	if got := ims.dataURLFor(0, 0, srv.URL+"/big.png"); got != srv.URL+"/big.png" {
		t.Errorf("超限响应应回退原 url, got %q", got)
	}
}

// TestImageStoreDataURLForConcurrentSameResult 多 goroutine 并发对同一 url
// 调用（-race 下验证并发安全），所有结果一致且为成功 data URL。
func TestImageStoreDataURLForConcurrentSameResult(t *testing.T) {
	srv, _ := servePNG(t, "image/png")
	ims, _ := testImageStore(t)
	u := srv.URL + "/c.png"

	const n = 16
	results := make([]string, n)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = ims.dataURLFor(0, 0, u)
		}()
	}
	wg.Wait()

	for i, r := range results {
		if r != pngDataURL {
			t.Errorf("results[%d] = %q, want 成功 data URL", i, r)
		}
	}
}

// TestImageStoreDataURLForRejectsHeaderLies CDN 把 AVIF（魔数 ftyp+avif）误标
// 成 image/jpeg 时必须拒绝——只信魔数不信 header；否则该图会以
// "unsupported image" 拖垮整个请求（线上实测）。
func TestImageStoreDataURLForRejectsHeaderLies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("\x00\x00\x00\x20ftypavif\x00\x00\x00\x00avifmif1fake-payload"))
	}))
	defer srv.Close()
	ims, _ := testImageStore(t)
	if got := ims.dataURLFor(0, 0, srv.URL+"/a.jpg"); !strings.HasPrefix(got, "http") {
		t.Fatalf("AVIF 伪装 jpeg 应下载失败回退原 URL, got %q", got[:40])
	}
	// 真 jpeg 带 octet-stream 头应凭魔数成功
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("\xFF\xD8\xFF\xE0jpeg-bytes"))
	}))
	defer ok.Close()
	ims2, _ := testImageStore(t)
	if got := ims2.dataURLFor(0, 0, ok.URL+"/b.jpg"); !strings.HasPrefix(got, "data:image/jpeg;base64,") {
		t.Fatalf("真 jpeg 应凭魔数识别成功, got %q", got[:40])
	}
}

// TestImageStoreDataURLForMissingFileFallsBack 映射在但文件被清理（如超容量
// 被删）→ 落回 URL 重新下载并重建映射。
func TestImageStoreDataURLForMissingFileFallsBack(t *testing.T) {
	srv, hits := servePNG(t, "image/png")
	ims, st := testImageStore(t)

	first := ims.dataURLFor(9, 0, srv.URL+"/a.png")
	refs, _ := st.ImageRefs(9)
	if err := os.Remove(filepath.Join(ims.dir, refs[0].Sha256)); err != nil {
		t.Fatalf("删除落盘文件失败: %v", err)
	}
	// 重建实例（清掉 url→哈希 去重），模拟重启后文件被外部清理
	ims2 := newImageStore(ims.dir, st)
	second := ims2.dataURLFor(9, 0, srv.URL+"/a.png")

	if second != first || second != pngDataURL {
		t.Errorf("文件缺失回退解析 = %q, want %q", second, first)
	}
	if hits.Load() != 2 {
		t.Errorf("HTTP 请求数 = %d, want 2（文件被删后应重新下载）", hits.Load())
	}
}
