package storage

import "testing"

func TestMessageWindowIncludesOldTargetAndRejectsOtherChat(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for id := int64(1); id <= 120; id++ {
		if err := s.PutMessage(&MessageRecord{MessageID: id, Direction: "recv", ChatType: "group", ChatID: 100, UserID: 11, RawMessage: "row", Time: 1000}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.MessageWindow("group", 100, 10, 20)
	if err != nil {
		t.Fatal(err)
	}
	found, seen := false, map[int64]bool{}
	for _, row := range rows {
		if seen[row.MessageID] {
			t.Fatal("duplicate neighbor")
		}
		seen[row.MessageID] = true
		found = found || row.MessageID == 10
	}
	if !found || len(rows) > 20 {
		t.Fatal("window lost old target")
	}
	if _, err := s.MessageWindow("group", 200, 10, 20); err == nil {
		t.Fatal("wrong-chat lookup succeeded")
	}
}
