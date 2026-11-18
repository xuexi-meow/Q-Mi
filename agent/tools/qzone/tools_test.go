package qzone_test

import (
	"encoding/json"
	"strings"
	"testing"

	"Q-Mi/agent/tool"
	qzonetools "Q-Mi/agent/tools/qzone"
	"Q-Mi/api"
)

func run(t *testing.T, tools []tool.Tool, name, args string) (string, error) {
	t.Helper()
	for _, item := range tools {
		if item.Name == name {
			return item.Run(json.RawMessage(args))
		}
	}
	t.Fatalf("missing %s", name)
	return "", nil
}

func TestBrowsePreservesUnknownImagesAndBoundsSummary(t *testing.T) {
	tools := qzonetools.Tools(&qzonetools.Dependencies{
		Call: func(req api.Request) (json.RawMessage, error) {
			if req.Path() != "/get_qzone_feeds" {
				t.Fatalf("wrong API request: %s", req.Path())
			}
			return json.RawMessage(`{"feeds":[{"uin":7,"nickname":"friend","time":100,"tid":"post","content":"` +
				strings.Repeat("中", 150) + `","images":null}],"has_more":true}`), nil
		},
	})
	out, err := run(t, tools, "browse_qzone_feeds", `{}`)
	if err != nil || !json.Valid([]byte(out)) || !strings.Contains(out, `"images":null`) ||
		!strings.Contains(out, `"image_count":null`) || !strings.Contains(out, `"has_more":true`) || strings.Count(out, "中") != 100 {
		t.Fatalf("browse response = %s, %v", out, err)
	}
}

func TestDeleteFailsClosedWithoutAuthorizationService(t *testing.T) {
	calls := 0
	tools := qzonetools.Tools(&qzonetools.Dependencies{
		Admin: true,
		Call: func(api.Request) (json.RawMessage, error) {
			calls++
			return json.RawMessage(`{}`), nil
		},
	})
	if _, err := run(t, tools, "delete_qzone", `{"tid":"post"}`); err == nil || calls != 0 {
		t.Fatalf("delete bypassed missing authorization service: calls=%d, err=%v", calls, err)
	}
}

func TestQzoneToolsConstructWithoutServices(t *testing.T) {
	tools := qzonetools.Tools(nil)
	if len(tools) != 8 {
		t.Fatalf("qzone tools = %d, want 8", len(tools))
	}
	if _, err := run(t, tools, "qzone_profile", `{}`); err == nil {
		t.Fatal("missing API service did not return an error")
	}
}
