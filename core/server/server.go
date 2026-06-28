package server

import (
	"Q-Mi/app"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"Q-Mi/event"
	"log/slog"
	"net/http"
	"strings"
)

type Server struct {
	Port    int    `json:"port"`
	Host    string `json:"host"`
	Pattern string `json:"pattern"`
}

func (s *Server) StartServer() {

	http.HandleFunc(s.Pattern, handleHttp)

	err := http.ListenAndServe(s.Host+":"+strconv.Itoa(s.Port), nil)
	if err != nil {
		slog.Error("服务器错误:", err)
	}
	slog.Info("服务器启动成功,监听地址:", s.Host+":"+string(s.Port)+s.Pattern)
}

func handleHttp(w http.ResponseWriter, r *http.Request) {
	if app.GetApp().Config.NetSetting.Token != "" {
		auth := r.Header.Get("Authorization")
		theToken := strings.TrimPrefix(auth, "Bearer ")
		if theToken != app.GetApp().Config.NetSetting.Token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

	bodyBytes, err := io.ReadAll(r.Body)
	body := io.NopCloser(bytes.NewReader(bodyBytes))
	var data event.MainEvent
	err = json.NewDecoder(body).Decode(&data)
	if err != nil {
		slog.Error("解析请求失败:", err)
	}
	
	fmt.Printf("%+v\n", data)
	
	slog.Info("（测试用）收到请求", "method", r.Method, "url", r.URL.String(), "消息本体", string(bodyBytes))

}