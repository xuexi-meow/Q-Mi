package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"Q-Mi/event"
)

type Server struct {
	Port    int    `json:"port"`
	Host    string `json:"host"`
	Pattern string `json:"pattern"`
	Token   string `json:"-"`

	httpServer *http.Server
}

func (s *Server) StartServer() error {
	mux := http.NewServeMux()
	mux.HandleFunc(s.Pattern, s.handleHTTP)
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("监听 %s 失败: %w", addr, err)
	}
	s.httpServer = &http.Server{
		Addr:              listener.Addr().String(),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	go func() {
		if err := s.httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("服务器错误", "err", err)
		}
	}()
	slog.Info("服务端已启动", "addr", listener.Addr().String())
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func (s *Server) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			http.Error(w, "Request Entity Too Large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "Bad Request", http.StatusBadRequest)
		}
		return
	}
	if s.Token != "" {
		// OneBot HTTP 上报按原始请求体计算 HMAC-SHA1，而非 Bearer 鉴权。
		mac := hmac.New(sha1.New, []byte(s.Token))
		mac.Write(body)
		expected := "sha1=" + hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(r.Header.Get("X-Signature"))) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
	}
	if !json.Valid(body) {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	event.Dispatch(body)
	w.WriteHeader(http.StatusOK)
}
