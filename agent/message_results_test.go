package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"Q-Mi/storage"
)

func messageRowsOf(t *testing.T, out string) []map[string]any {
	t.Helper()
	var result struct {
		Messages  []map[string]any `json:"messages"`
		Returned  *int             `json:"returned"`
		Omitted   *int             `json:"omitted"`
		Truncated *bool            `json:"truncated"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("message query must return an object: %v (%s)", err, out)
	}
	if result.Messages == nil || result.Returned == nil || result.Omitted == nil || result.Truncated == nil {
		t.Fatalf("missing message query metadata: %s", out)
	}
	if *result.Returned != len(result.Messages) || *result.Omitted < 0 {
		t.Fatalf("inconsistent message query counts: %s", out)
	}
	return result.Messages
}

func TestMessageQueryLimitsEachTextWithoutChangingArchive(t *testing.T) {
	for _, tool := range []string{"get_recent_messages", "search_messages"} {
		t.Run(tool, func(t *testing.T) {
			store := openTestStore(t)
			text := strings.Repeat("界", 1800) + "end"
			putRecord(t, store, 1, "recv", "group", 888, 7001, "friend", text, 1)
			tools, _, _ := testToolSet(t, store)
			out := runToolOK(t, tools, tool, `{"chat_type":"group","chat_id":888,"limit":1}`)
			result := objOf(t, out)
			rows := messageRowsOf(t, out)
			if len(rows) != 1 || rows[0]["text"] != strings.Repeat("界", 1000) ||
				rows[0]["text_truncated"] != true || rows[0]["text_length"] != float64(1803) {
				t.Fatalf("long message was not explicitly bounded: %s", out)
			}
			if result["truncated"] != true || result["omitted"] != float64(0) {
				t.Fatalf("incorrect partial-text status: %#v", result)
			}
			record, err := store.GetMessage(1)
			if err != nil || record == nil || record.RawMessage != text {
				t.Fatalf("return limit changed stored text: %v", err)
			}
		})
	}
}

func TestMessageQueryTotalBudgetKeepsNewestAndValidJSON(t *testing.T) {
	for _, tool := range []string{"get_recent_messages", "search_messages"} {
		for _, tc := range []struct{ name, text string }{
			{"unicode", strings.Repeat("界", 1000)},
			{"json-escapes", strings.Repeat("\x00", 1000)},
		} {
			t.Run(tool+"/"+tc.name, func(t *testing.T) {
				text := tc.text
				store := openTestStore(t)
				for id := int64(1); id <= 50; id++ {
					putRecord(t, store, id, "recv", "group", 888, 7001, "friend", text, id)
				}
				tools, _, _ := testToolSet(t, store)
				out := runToolOK(t, tools, tool, `{"chat_type":"group","chat_id":888,"limit":50}`)
				if !utf8.ValidString(out) || !json.Valid([]byte(out)) || utf8.RuneCountInString(out) > 12000 {
					t.Fatalf("result exceeds complete-JSON budget: %d characters", utf8.RuneCountInString(out))
				}
				result := objOf(t, out)
				rows := messageRowsOf(t, out)
				if len(rows) == 0 || len(rows) >= 50 || result["truncated"] != true ||
					result["omitted"] != float64(50-len(rows)) {
					t.Fatalf("missing budget/omission status: %#v", result)
				}
				for i, row := range rows {
					if row["message_id"] != float64(51-len(rows)+i) || row["text"] != text {
						t.Fatalf("must retain contiguous newest messages, old to new: %#v", row)
					}
				}
			})
		}
	}
}

func TestMessageQueryOnlyAttachesImagesFromReturnedMessages(t *testing.T) {
	store := openTestStore(t)
	srv, hits := servePNG(t, "image/png")
	for id := int64(1); id <= 10; id++ {
		record := &storage.MessageRecord{
			MessageID: id, Direction: "recv", ChatType: "group", ChatID: 888, UserID: 7001,
			RawMessage: strings.Repeat("\x00", 1000), Time: float64(id),
		}
		if id == 1 {
			record.Segments = []byte(`[{"type":"image","data":{"url":"` + srv.URL + `/old.png"}}]`)
		}
		putRecordFull(t, store, record)
	}
	tools, turn := testToolSetForViewImages(t, store, newImageStore(t.TempDir(), store))
	out := runToolOK(t, tools, "get_recent_messages", `{"chat_type":"group","chat_id":888,"limit":10}`)
	rows := messageRowsOf(t, out)
	if len(rows) != 1 || rows[0]["message_id"] != float64(10) {
		t.Fatalf("unexpected bounded result: %s", out)
	}
	if hits.Load() != 0 || len(turn.pendingImages) != 0 {
		t.Fatal("omitted message image was downloaded or attached")
	}
}

func TestMessageQueryCanReadLatestNFromOneConversation(t *testing.T) {
	store := openTestStore(t)
	for id := int64(1); id <= 12; id++ {
		putRecord(t, store, id, "recv", "group", 888, 7001, "friend", "short", id)
	}
	putRecord(t, store, 13, "recv", "private", 888, 888, "friend", "private", 13)
	putRecord(t, store, 14, "recv", "group", 889, 7001, "friend", "other group", 14)
	tools, _, _ := testToolSet(t, store)
	for _, tool := range []string{"get_recent_messages", "search_messages"} {
		out := runToolOK(t, tools, tool, `{"chat_type":"group","chat_id":888,"limit":3}`)
		rows := messageRowsOf(t, out)
		result := objOf(t, out)
		if len(rows) != 3 || rows[0]["message_id"] != float64(10) ||
			rows[2]["message_id"] != float64(12) || result["truncated"] != false ||
			result["omitted"] != float64(0) {
			t.Fatalf("latest N conversation messages were not returned intact: %s", out)
		}
	}
}

func TestMessageQueryEmptyWindowHasExplicitMetadata(t *testing.T) {
	tools, _, _ := testToolSet(t, openTestStore(t))
	for _, tool := range []string{"get_recent_messages", "search_messages"} {
		out := runToolOK(t, tools, tool, `{"chat_type":"private","chat_id":888}`)
		rows := messageRowsOf(t, out)
		result := objOf(t, out)
		if len(rows) != 0 || result["returned"] != float64(0) ||
			result["omitted"] != float64(0) || result["truncated"] != false {
			t.Fatalf("empty result invented missing messages: %s", out)
		}
	}
}
