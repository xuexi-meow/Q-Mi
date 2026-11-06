package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"Q-Mi/event"
)

func TestStartReportsBindFailure(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	s := &Server{Host: "127.0.0.1", Port: listener.Addr().(*net.TCPAddr).Port, Pattern: "/Q-Mi"}
	if err := s.StartServer(); err == nil {
		s.Shutdown(context.Background())
		t.Fatal("端口占用时应返回启动错误")
	}
}

func TestHandleHTTPValidatesBeforeDispatch(t *testing.T) {
	key := t.Name() + "." + strconv.FormatInt(time.Now().UnixNano(), 10)
	received := make(chan struct{}, 10)
	event.Register(key, func(json.RawMessage) { received <- struct{}{} })
	s := &Server{Token: "test-secret"}
	body := `{"post_type":"` + key + `"}`
	sign := func(body, secret string) string {
		mac := hmac.New(sha1.New, []byte(secret))
		mac.Write([]byte(body))
		return "sha1=" + hex.EncodeToString(mac.Sum(nil))
	}
	cases := []struct {
		name, method, signature, body string
		status                        int
	}{
		{"missing signature", "POST", "", body, http.StatusUnauthorized},
		{"bare token", "POST", "test-secret", body, http.StatusUnauthorized},
		{"wrong secret", "POST", sign(body, "wrong"), body, http.StatusUnauthorized},
		{"tampered body", "POST", sign(body, "test-secret"), body + " ", http.StatusUnauthorized},
		{"wrong method", "GET", sign(body, "test-secret"), body, http.StatusMethodNotAllowed},
		{"bad json", "POST", sign(`{`, "test-secret"), `{`, http.StatusBadRequest},
		{"large body", "POST", "", strings.Repeat("x", 8<<20+1), http.StatusRequestEntityTooLarge},
		{"valid", "POST", sign(body, "test-secret"), body, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/Q-Mi", strings.NewReader(tc.body))
			r.Header.Set("X-Signature", tc.signature)
			w := httptest.NewRecorder()
			s.handleHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d", w.Code, tc.status)
			}
		})
	}
	event.Wait()
	if len(received) != 1 {
		t.Fatalf("只有合法请求应分发，实际=%d", len(received))
	}
}

func TestShutdownStopsListeningAndDrainsEvents(t *testing.T) {
	key := t.Name() + "." + strconv.FormatInt(time.Now().UnixNano(), 10)
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	event.Register(key, func(json.RawMessage) {
		close(started)
		<-release
		close(finished)
	})
	s := &Server{Host: "127.0.0.1", Port: 0, Pattern: "/Q-Mi"}
	if err := s.StartServer(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Shutdown(context.Background()) })
	client := &http.Client{Timeout: time.Second}
	resp, err := client.Post("http://"+s.httpServer.Addr+"/Q-Mi", "application/json", strings.NewReader(`{"post_type":"`+key+`"}`))
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("事件未分发")
	}
	if err := s.Shutdown(context.Background()); err != nil {
		close(release)
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", s.httpServer.Addr, time.Second)
	if err == nil {
		conn.Close()
		close(release)
		t.Fatal("Shutdown 后仍接受连接")
	}
	close(release)
	event.Wait()
	select {
	case <-finished:
	default:
		t.Fatal("退出前未完成事件处理")
	}
}
