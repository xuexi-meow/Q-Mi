package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"Q-Mi/utils/config"
)

var senseNovaProbe = flag.Bool("qmi-sensenova-probe", false, "make one paid SenseNova diagnostic request; never execute tools")

func TestSenseNovaRawProbe(t *testing.T) {
	if !*senseNovaProbe {
		t.Skip("live model probe requires explicit -qmi-sensenova-probe")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	store, err := config.NewStore(config.Paths{
		Base: filepath.Join(root, "Config.yaml"), Overlay: filepath.Join(root, "Config.webui.yaml"),
	})
	if err != nil {
		t.Fatal("cannot load model configuration")
	}
	cfg := *store.Snapshot().Agent
	var candidate *config.ModelCandidate
	for _, tier := range cfg.Models {
		if !tier.Enabled() {
			continue
		}
		for _, c := range tier.EffectiveCandidates("chat") {
			if c.Enabled() && c.Model == "sensenova-6.8-flash" {
				copy := c
				candidate = &copy
				break
			}
		}
		if candidate != nil {
			break
		}
	}
	if candidate == nil {
		t.Fatal("no enabled sensenova-6.8-flash candidate")
	}
	if candidate.BaseURL == "" {
		candidate.BaseURL = cfg.BaseURL
	}
	key := candidate.APIKey
	if key == "" && candidate.APIKeyEnv != "" {
		key = os.Getenv(candidate.APIKeyEnv)
	}
	if key == "" {
		key = cfg.APIKey
	}
	if key == "" {
		t.Fatal("candidate has no API key")
	}
	for _, path := range []*string{&cfg.PersonaFile, &cfg.GuideFile} {
		if *path != "" && !filepath.IsAbs(*path) {
			*path = filepath.Join(root, *path)
		}
	}
	cfg.WorkspaceDir = t.TempDir()
	if cfg.GuideFile == "" {
		cfg.GuideFile = filepath.Join(root, defaultGuidePath)
	}
	builder := NewDefaultBuilder(nil, cfg)
	registry := NewRegistry(enabledModules(cfg)...)
	builder.SetToolDocs(registry.PromptDocs())
	turn := &Turn{Now: time.Now()}
	payload, err := json.Marshal(ChatRequest{
		Model: candidate.Model, MaxTokens: 2048, Temperature: cfg.Temperature,
		Messages: []ChatMessage{
			builder.SystemMessage(turn),
			{Role: "user", Content: "当前时间: " + turn.Now.Format(nowLayout) +
				"\n[定时轮询] 当前没有新聊天、没有待办，也没有正在进行的研究。"},
		},
		Tools: toolDefs(registry.Build(&ToolContext{})),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(candidate.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		t.Fatal("invalid model endpoint")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := (&http.Client{Timeout: 120 * time.Second}).Do(req)
	if err != nil {
		t.Fatal("model request failed:", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		t.Fatal(err)
	}
	safeBody := strings.ReplaceAll(string(body), key, "[REDACTED]")
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, []byte(safeBody), "", "  "); err != nil {
		t.Fatalf("HTTP %d, non-JSON response", resp.StatusCode)
	}
	t.Logf("model=%s HTTP=%d request_bytes=%d; response only, no tools executed:\n%s",
		candidate.Model, resp.StatusCode, len(payload), pretty.String())
	if resp.StatusCode != http.StatusOK {
		t.FailNow()
	}
}
