package agent

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

var pngBytes = append(
	[]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 'I', 'H', 'D', 'R'},
	make([]byte, 16)...,
)

var pngDataURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)

func servePNG(t *testing.T, contentType string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		_, _ = w.Write(pngBytes)
	}))
	t.Cleanup(server.Close)
	return server, &hits
}
