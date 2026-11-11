package message

import (
	"encoding/json"
	"testing"
)

func TestAddressingIDsAcceptNumbersWithoutLosingPrecision(t *testing.T) {
	for _, tc := range []struct {
		kind, data string
		want       any
	}{
		{"at", `{"qq":9007199254740993}`, At{QQ: "9007199254740993"}},
		{"at", `{"qq":"all"}`, At{QQ: "all"}},
		{"reply", `{"id":-12345}`, Reply{ID: "-12345"}},
	} {
		msg := Message{Type: tc.kind, Parsed: json.RawMessage(tc.data)}
		got, err := msg.TypeHandler()
		if err != nil || got != tc.want {
			t.Errorf("%s(%s)=%#v, %v; want %#v", tc.kind, tc.data, got, err, tc.want)
		}
	}
}
