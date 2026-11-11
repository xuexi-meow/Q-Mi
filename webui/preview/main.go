// A loopback-only synthetic UI server. It imports no bot or application packages.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:6088", "loopback address")
	assets := flag.String("static", "webui/static", "actual console assets")
	flag.Parse()
	host, _, err := net.SplitHostPort(*addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() { log.Fatal("preview must bind loopback") }
	var mu sync.Mutex
	version := 1
	now := time.Now()
	metrics := map[string]any{"estimated_tokens":18420,"budget":64000,"message_count":8,"tool_count":2,"compactions":2,"text_tokens":12000,"reasoning_tokens":1500,"image_tokens":2048,"tool_tokens":2872}
	entries := []map[string]any{
		{"role":"system","kind":"prompt","text":"你是红薯。保持自主判断，发言必须通过 send_message。","estimated_tokens":48},
		{"role":"user","kind":"state","text":"当前情绪：平静。待办：查阅刚才的图片。","estimated_tokens":80},
		{"role":"user","kind":"summary","text":"晚间群聊围绕游戏展开；小林说的“又菜又爱玩”是在自嘲。","estimated_tokens":120},
		{"role":"user","kind":"input","chat_type":"group","chat_id":"888","message_id":"10","user_id":"10001","origin":"live","text":"{\"text\":\"你好香 @小林 这张截图是不是昨天那局？\",\"mentions\":[\"10002\"],\"reply\":{\"message_id\":\"8\",\"user_id\":\"10002\"}}","images":1,"estimated_tokens":2150},
		{"role":"assistant","kind":"output","text":"<script>window.injected=true</script>","reasoning":"先确认引用来源，再决定是否加入谈话。","tool_calls":[]any{map[string]any{"id":"call-1","type":"function","function":map[string]any{"name":"get_recent_messages","arguments":"{\"chat_type\":\"group\",\"chat_id\":888}"}}},"estimated_tokens":320},
		{"role":"tool","kind":"tool_result","tool_call_id":"call-1","text":"{\"messages\":[{\"sender\":\"小林\",\"text\":\"确实是昨天，最后还是输了\"}],\"token\":\"[redacted]\"}","estimated_tokens":150},
		{"role":"assistant","kind":"output","text":"我这轮先看看。","reasoning":"对话的对象是小林，不需要回应每一条。","estimated_tokens":60},
		{"role":"tool","kind":"tool_result","tool_call_id":"finish-1","text":"{}","estimated_tokens":5},
		{"role":"tool_definition","kind":"tool_definition","text":"{\"name\":\"get_recent_messages\",\"parameters\":{\"chat_id\":\"integer\"}}","estimated_tokens":350},
		{"role":"tool_definition","kind":"tool_definition","text":"{\"name\":\"send_message\",\"parameters\":{\"text\":\"string\"}}","estimated_tokens":300},
	}
	for i,e := range entries { e["index"]=i; e["original_runes"]=len([]rune(fmt.Sprint(e["text"]))); e["truncated"]=false }
	entries[0]["truncated"], entries[0]["original_runes"] = true, 72000
	usage := map[string]any{"requests":128,"prompt_tokens":428600,"output_tokens":12580,"cache_hit_tokens":360000,"cache_miss_tokens":68600}
	effective := map[string]any{
		"agent":map[string]any{"enable":true,"model":"legacy-model","base_url":"https://example.invalid/v1","api_key":"sk-***-mask","tool_choice":"required","temperature":0.8,"max_tool_rounds":10,"max_attempts":2,"debounce":3000000000,"self_wake":"10m","persona_file":"agent/persona.md","guide_file":"agent/guide.md","context":map[string]any{"max_input_tokens":64000,"review_after_wakes":8,"keep_recent_messages":12,"summary_tokens":1024},"history":map[string]any{"max":100,"trim":40},"wake_routes":map[string]any{"chat":"chat","background":"background"},"models":map[string]any{
			"chat":map[string]any{"strategy":"priority","candidates":[]any{map[string]any{"name":"primary","model":"chat-model","base_url":"https://example.invalid/v1","api_key":"sk-***-mask","api_key_env":"MODEL_KEY","tool_choice":"required","context_window":128000,"weight":2,"cooldown":"3m","capabilities":[]any{"tools","vision"}}}},
			"background":map[string]any{"model":"small-model","base_url":"https://example.invalid/v1","tool_choice":"required","capabilities":[]any{"tools"}},
		},"modules":map[string]any{"qq":true,"memory":true,"browser":false,"computer":false}},
		"bot":map[string]any{"qq_number":123456,"admin_number":[]any{10001}},
		"storage":map[string]any{"enable":true,"path":"preview.db"},
		"webui":map[string]any{"host":"127.0.0.1","port":6088},
	}
	prompts := map[string]string{"persona":"你是红薯。喜欢游戏和朋友，保持自己的节奏。","guide":"不把群友之间的 @ 和引用误认为在和自己说话。"}
	notes := []map[string]any{{"index":0,"time":now.Unix(),"text":"小林最近在玩新的合作游戏。"}}
	timers := []map[string]any{{"id":"timer-1","fire_at":now.Add(time.Hour),"note":"看看下载是否完成"}}
	configView := func() map[string]any { return map[string]any{"effective":effective,"overlay":map[string]any{},"paths":map[string]any{"base":"Config.yaml","overlay":"Config.webui.yaml"},"restart_fields":[]string{"agent.enable","webui","storage"}} }
	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/",http.FileServer(http.Dir(*assets))))
	mux.HandleFunc("GET /{$}",func(w http.ResponseWriter,r *http.Request){ http.ServeFile(w,r,filepath.Join(*assets,"index.html")) })
	mux.HandleFunc("/api/",func(w http.ResponseWriter,r *http.Request){
		w.Header().Set("Content-Type","application/json; charset=utf-8")
		w.Header().Set("Cache-Control","no-store")
		if r.Header.Get("Authorization") != "Bearer preview-token" { http.Error(w,"Unauthorized",401); return }
		mu.Lock(); defer mu.Unlock()
		var out any = map[string]any{"ok":true}
		q := r.URL.Query()
		switch r.URL.Path {
		case "/api/runtime":
			out = map[string]any{"available":true,"phase":"idle","round":3,"candidate":"primary","model":"chat-model","tier":"chat","pending":0,"updated_at":now,"request_version":version,"working_version":version,"metrics":metrics,"last_usage":map[string]any{"prompt_tokens":18412,"completion_tokens":380}}
		case "/api/overview":
			out = map[string]any{"napcat":map[string]any{"online":true,"version":"preview"},"uptime_seconds":18640,"messages":24580,"notices":128,"mood":"有点开心","notes_count":len(notes),"timers_count":len(timers),"usage":usage,"usage_by_model":map[string]any{"primary":usage}}
		case "/api/context":
			filtered := []map[string]any{}
			for _, e := range entries {
				data,_ := json.Marshal(e)
				if k:=q.Get("kind"); k!="" && k!="all" && k!=e["kind"] { continue }
				if k:=q.Get("chat_id"); k!="" && k!=e["chat_id"] { continue }
				if k:=q.Get("q"); k!="" && !strings.Contains(strings.ToLower(string(data)),strings.ToLower(k)) { continue }
				filtered=append(filtered,e)
			}
			offset,_ := strconv.Atoi(q.Get("offset")); if offset<0 {offset=0}; if offset>len(filtered){offset=len(filtered)}
			limit,_:=strconv.Atoi(q.Get("limit")); if limit<=0 {limit=40}
			end:=offset+limit; if end>len(filtered){end=len(filtered)}
			source:=q.Get("source"); if source=="" {source="working"}
			out=map[string]any{"version":version,"source":source,"updated_at":now,"metrics":metrics,"entries":filtered[offset:end],"total":len(filtered),"offset":offset,"limit":limit}
		case "/api/context/message":
			v,_:=strconv.Atoi(q.Get("version")); i,_:=strconv.Atoi(q.Get("index"))
			if v!=version { w.WriteHeader(409); out=map[string]any{"error":"上下文版本已更新，请刷新列表","code":"version_conflict"}
			} else if i<0 || i>=len(entries) {w.WriteHeader(404);out=map[string]any{"error":"not found"}} else {out=entries[i]}
		case "/api/preview/bump": version++
		case "/api/tasks":
			out=[]any{map[string]any{"id":"12","tool":"download_file","chat_type":"group","chat_id":"888","status":"done","started_at":now.Add(-time.Minute),"done_at":now,"args":"{\"name\":\"参考资料\"}","result":"下载完成：workspace/reference.txt","error":""}}
		case "/api/events":
			out=[]any{}
			after,_:=strconv.Atoi(q.Get("after"))
			for i,kind:=range []string{"turn_start","reasoning","tool_call","tool_result","usage","finish"} {
				if i+1<=after {continue}
				text:=[]string{"收到 group:888 的新消息","先确认是谁在和谁说话。","get_recent_messages {\"chat_id\":888}","找到引用消息，来自小林。","hit=18000 miss=412 out=380 candidate=primary","本次活动结束"}[i]
				out=append(out.([]any),map[string]any{"id":i+1,"time":now.Add(time.Duration(i-6)*time.Second),"kind":kind,"chat_type":"group","chat_id":888,"text":text})
			}
		case "/api/chats":
			out=[]any{map[string]any{"chat_type":"group","chat_id":888,"last_text":"今晚还开吗？","last_time":now.Unix(),"count":120},map[string]any{"chat_type":"private","chat_id":10001,"last_text":"明天见","last_time":now.Unix()-600,"count":24}}
		case "/api/messages", "/api/search":
			rows:=[]any{}; start,end:=71,120
			if q.Get("before_time")!="" {start,end=21,70}
			if q.Get("around_message_id")!="" || r.URL.Path=="/api/search" {start,end=1,20}
			ct,cid:=q.Get("chat_type"),q.Get("chat_id"); if ct=="" {ct="group"}; if cid=="" {cid="888"}
			for i:=end;i>=start;i-- {
				text:=fmt.Sprintf("第 %d 条：今晚一起开游戏吗？",i)
				if i==10 {text="你好香，昨天那局的截图找到了 <img src=x onerror='window.injected=true'>"}
				rows=append(rows,map[string]any{"message_id":i,"chat_type":ct,"chat_id":cid,"direction":"recv","user_id":10001,"sender_name":"小林","text":text,"time":now.Unix()-int64(120-i)*60,"mentions":[]string{"10002"},"addressing_known":true,"reply":map[string]any{"message_id":"8","status":"known","sender_name":"小林"}})
			};out=rows
		case "/api/notes":
			if r.Method=="POST" {var body map[string]string;_ = json.NewDecoder(r.Body).Decode(&body);notes=append(notes,map[string]any{"index":len(notes),"time":now.Unix(),"text":body["text"]})};out=notes
		case "/api/mood":
			out=map[string]any{"mood":"有点开心"}
		case "/api/timers": out=timers
		case "/api/file":
			if r.Method=="PUT" {var body map[string]string;_ = json.NewDecoder(r.Body).Decode(&body);prompts[body["name"]]=body["content"];out=map[string]any{"ok":true,"effect":"next_request"}
			} else {out=map[string]any{"content":prompts[q.Get("name")],"path":"agent/"+q.Get("name")+".md"}}
		case "/api/friends":out=[]any{map[string]any{"user_id":10001,"nickname":"小林","remark":"朋友","level":64}}
		case "/api/groups":out=[]any{map[string]any{"group_id":888,"group_name":"今晚打什么","member_count":24}}
		case "/api/members":out=map[string]any{"total":1,"returned":1,"truncated":false,"members":[]any{map[string]any{"user_id":10001,"nickname":"小林","card":"今晚还开吗","role":"member"}}}
		case "/api/napcat":
			if r.Method=="GET" {out=map[string]any{"actions":[]string{"get_status","send_group_msg","set_group_kick"},"dangerous":[]string{"set_group_kick"}}}
		case "/api/config", "/api/config/reload":
			if r.Method=="PUT" {var patch map[string]any;_ = json.NewDecoder(r.Body).Decode(&patch);merge(effective,patch)}
			out=configView()
		case "/api/config/validate":out=map[string]any{"ok":true}
		case "/api/models/test":out=map[string]any{"ok":true,"latency_ms":18}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
	log.Printf("Synthetic preview: http://%s (token: preview-token)",*addr)
	log.Fatal(http.ListenAndServe(*addr,mux))
}

func merge(dst, patch map[string]any) {
	for k,v:=range patch { if obj,ok:=v.(map[string]any);ok { if old,ok:=dst[k].(map[string]any);ok {merge(old,obj);continue} };dst[k]=v }
}
