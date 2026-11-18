package workspacepath_test

import (
	"errors"
	"path/filepath"
	"testing"

	"Q-Mi/agent/workspacepath"
)

func TestResolveKeepsOutsideTargetForApproval(t *testing.T) {
	root := t.TempDir()
	inside, err := workspacepath.Resolve(root, "sub/note.txt")
	if err != nil || inside != filepath.Join(root, "sub", "note.txt") {
		t.Fatalf("inside = %q, %v", inside, err)
	}
	outside, err := workspacepath.Resolve(root, "../outside.txt")
	var boundary workspacepath.Outside
	if !errors.As(err, &boundary) || outside != filepath.Join(filepath.Dir(root), "outside.txt") {
		t.Fatalf("outside = %q, %v", outside, err)
	}
	if _, err := workspacepath.Resolve(root, ""); err == nil {
		t.Fatal("empty path must remain invalid")
	}
}

func TestNapcatPathMapsDriveWithoutChangingRemoteMedia(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`F:\files\a.png`, "/mnt/f/files/a.png"},
		{"https://example.com/a.png", "https://example.com/a.png"},
		{"base64://AAAA", "base64://AAAA"},
		{"/home/user/a.png", "/home/user/a.png"},
	} {
		got, err := workspacepath.NapcatPath(t.TempDir(), tc.input)
		if err != nil || got != tc.want {
			t.Fatalf("%q: %q, %v", tc.input, got, err)
		}
	}
}
