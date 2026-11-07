// stickers_test.go 表情收藏表的 CRUD 往返、空库、删除不存在、
// kind CHECK 约束与建表幂等（老库自动补建）。
package storage

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func faceSticker() Sticker {
	return Sticker{Kind: "face", FaceID: "28", Summary: "憨笑"}
}

func mfaceSticker() Sticker {
	return Sticker{Kind: "mface", EmojiID: "12345", EmojiPackageID: "9", Key: "abcKEY", Summary: "小鸡啄米"}
}

func imageSticker() Sticker {
	return Sticker{Kind: "image", URL: "https://example.com/a.jpg", Summary: "看看"}
}

// TestStickerCRUDRoundTrip 三类 kind 各存一条 → List/Get 逐字段还原；
// CreatedAt 为零值时自动填当前时间。
func TestStickerCRUDRoundTrip(t *testing.T) {
	s := openTestStore(t)

	want := []Sticker{faceSticker(), mfaceSticker(), imageSticker()}
	for i := range want {
		id, err := s.AddSticker(want[i])
		if err != nil {
			t.Fatalf("AddSticker(%s): %v", want[i].Kind, err)
		}
		want[i].ID = id
		if id <= 0 {
			t.Fatalf("AddSticker 应返回正数自增 id, got %d", id)
		}
		// CreatedAt 自动填充在读取侧验证（AddSticker 按值收参不回填）
		saved, err := s.GetSticker(id)
		if err != nil || saved == nil {
			t.Fatalf("GetSticker(%d): %v, %v", id, saved, err)
		}
		if saved.CreatedAt == 0 {
			t.Fatalf("CreatedAt 应自动填充, got 0")
		}
		if delta := time.Since(time.UnixMilli(int64(saved.CreatedAt * 1000))); delta < -time.Minute || delta > time.Minute {
			t.Errorf("CreatedAt 应接近当前时间, got %v", saved.CreatedAt)
		}
		want[i].CreatedAt = saved.CreatedAt // 读取侧验证后同步，供 roundtrip 比较
	}

	got, err := s.ListStickers()
	if err != nil {
		t.Fatalf("ListStickers: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListStickers roundtrip:\n want %+v\n got  %+v", want, got)
	}

	for i := range want {
		one, err := s.GetSticker(want[i].ID)
		if err != nil || one == nil {
			t.Fatalf("GetSticker(%d): %v, %v", want[i].ID, one, err)
		}
		if !reflect.DeepEqual(*one, want[i]) {
			t.Errorf("GetSticker(%d) = %+v, want %+v", want[i].ID, *one, want[i])
		}
	}
}

// TestAddStickerKeepsExplicitCreatedAt 显式传入 CreatedAt 时保留原值。
func TestAddStickerKeepsExplicitCreatedAt(t *testing.T) {
	s := openTestStore(t)
	st := faceSticker()
	st.CreatedAt = 1729200000.5
	id, err := s.AddSticker(st)
	if err != nil {
		t.Fatalf("AddSticker: %v", err)
	}
	got, err := s.GetSticker(id)
	if err != nil || got == nil {
		t.Fatalf("GetSticker(%d): %v, %v", id, got, err)
	}
	if got.CreatedAt != 1729200000.5 {
		t.Errorf("CreatedAt = %v, want 1729200000.5", got.CreatedAt)
	}
}

// TestListStickersEmpty 空库返回空切片不报错。
func TestListStickersEmpty(t *testing.T) {
	s := openTestStore(t)
	got, err := s.ListStickers()
	if err != nil {
		t.Fatalf("空库 ListStickers: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("空库应返回空切片, got %+v", got)
	}
}

// TestGetStickerMissing 查不存在的 id 返回 nil, nil。
func TestGetStickerMissing(t *testing.T) {
	s := openTestStore(t)
	got, err := s.GetSticker(99999)
	if err != nil || got != nil {
		t.Errorf("不存在的表情应返回 nil, nil, got %v, %v", got, err)
	}
}

// TestDeleteSticker 删除后查不到；删除不存在的 id 不报错。
func TestDeleteSticker(t *testing.T) {
	s := openTestStore(t)
	id, err := s.AddSticker(faceSticker())
	if err != nil {
		t.Fatalf("AddSticker: %v", err)
	}
	if err := s.DeleteSticker(id); err != nil {
		t.Fatalf("DeleteSticker(%d): %v", id, err)
	}
	got, err := s.GetSticker(id)
	if err != nil || got != nil {
		t.Errorf("删除后应查不到, got %v, %v", got, err)
	}
	if err := s.DeleteSticker(id); err != nil {
		t.Errorf("删除不存在的 id 不应报错, got %v", err)
	}
}

// TestAddStickerRejectsUnknownKind kind 超出 face/mface/image 被 CHECK 约束拒绝。
func TestAddStickerRejectsUnknownKind(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.AddSticker(Sticker{Kind: "gif", URL: "https://x"}); err == nil {
		t.Error("非法 kind 应被 CHECK 约束拒绝")
	}
}

// TestOpenStickersTableIdempotent 新库建表；老库（无 stickers）Open 自动补建；
// 重开不丢数据。
func TestOpenStickersTableIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stickers.db")
	s := openFileStore(t, path)
	id, err := s.AddSticker(mfaceSticker())
	if err != nil {
		t.Fatalf("AddSticker: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	s = openFileStore(t, path) // 重开：数据还在
	got, err := s.GetSticker(id)
	if err != nil || got == nil || got.EmojiID != "12345" {
		t.Errorf("重开后数据丢失: got=%+v, err=%v", got, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// 老库（只有旧版 schema，无 stickers 表）→ Open 自动补建
	legacy := filepath.Join(t.TempDir(), "legacy.db")
	st2 := openFileStore(t, legacy)
	if err := st2.Close(); err != nil {
		t.Fatal(err)
	}
	st3 := openFileStore(t, legacy)
	if _, err := st3.AddSticker(faceSticker()); err != nil {
		t.Errorf("老库升级后 stickers 应可用: %v", err)
	}
}
