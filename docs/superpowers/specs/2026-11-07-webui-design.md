# Q-Mi WebUI 设计

日期：2026-11-07
状态：已确认（用户选定：全用途控制台 + Go 内嵌轻量 JS 形态）

## 1. 目标

单二进制内置的 Web 控制台，覆盖四类用途：观察调试 agent、控制 agent 行为、QQ 管理、配置与状态。无 node 构建链，前端库内嵌。

## 2. 架构

- 新包 `Q-Mi/webui`：独立 HTTP server（独立端口，与 OneBot 上报端口隔离）
- 静态资源 `go:embed`（含内嵌的 alpine.min.js，一次性 vendor 进仓库）
- 鉴权：除静态文件与登录页外，全部 API 要求 `Authorization: Bearer <token>`
- 实时性：浏览器 1s 轮询 `/api/events?after=<id>`（环形缓冲按 ID 增量拉取；SSE 留作后续优化，首版轮询足够且简单）

### 配置块

```yaml
webui:
  enable: false
  host: "127.0.0.1"   # 要手机/局域网访问改 0.0.0.0
  port: 6077
  token: ""           # enable 时必填
```

校验进 validateConfig：enable 时 token 非空、host 非空、port 1..65535。DefaultConfig/Config.example.yaml 同步。

## 3. 数据管道（agent 包新增）

### 3.1 事件环形缓冲

- `type EventLog struct`：容量 500 的环形缓冲，每条 `{ID uint64, Time, ChatType, ChatID, Kind, Text}`
- Kind：`turn_start / reasoning / output / tool_call / tool_result / reply / error / finish / interrupted`
- Manager 在 handleTurn 现有 slog 输出的同一批点位写入（复用信息，不重复逻辑）；fireTimer/通知入队也可写入
- 查询：`agent.EventsAfter(afterID uint64) []Event`（nil 安全，agent 未启用返回空）

### 3.2 agent 包导出的 UI 钩子（薄封装包级单例，nil 安全）

- `EventsAfter(after uint64) []Event`
- `Wake(text string) error` —— 注入 `[WebUI]` 行到 self 队列
- `TimerList() []ScheduledTimer` / `TimerSchedule(delaySec int, note string) (string, error)` / `TimerCancel(id string) error`
- `NotesList() []Note` / `NoteAdd(text string) error` / `NoteDelete(idx int) error`（直接读写 notes.json，沿用现有 helpers）
- `MoodGet() string` / `MoodSet(mood string) error`（写 state.json，复用 writeState）

## 4. API（全部 JSON；前缀 /api/）

| 路由 | 方法 | 说明 |
|---|---|---|
| /api/overview | GET | NapCat 状态/版本（request.Call）、Q-Mi 运行时长、消息库统计、mood/notes/timers 计数 |
| /api/events?after= | GET | 环形缓冲增量 |
| /api/chats | GET | 会话列表（messages 表 distinct + 最后一条摘要） |
| /api/messages | GET | 按 chat_type/chat_id 分页查询（复用 QueryMessages） |
| /api/search | POST | 全文搜索（复用 SearchMessages） |
| /api/notes | GET/POST/DELETE | 笔记读/增/删（DELETE 带 index） |
| /api/mood | GET/POST | 情绪读写 |
| /api/timers | GET/POST/DELETE | 计时器列表/新增/取消 |
| /api/wake | POST | 手动唤醒（text 进 self 队列） |
| /api/file?name= | GET/PUT | persona.md / guide.md 编辑器（白名单仅这两个文件，PUT 直接覆盖写回） |
| /api/debug | GET | agent/debug 目录文件列表（时间倒序，限 100） |
| /api/debug/file?name= | GET | 单个 debug JSON 原文 |
| /api/friends | GET | 好友列表 |
| /api/groups | GET | 群列表 |
| /api/members?group_id= | GET | 群成员（截断 100） |
| /api/napcat | POST | 直调动作 {action, params}（复用 P3-2 的 actionRegistry 思路或直接 request.Call+构造；仅供已登录 WebUI 管理员=token 持有人） |

存储查询需要 storage 新增：`ChatSummaries()`（chat_type/chat_id 分组 + max(time) + count）——W2 实现。

## 5. 页面（Alpine.js 单页，4 tab）

1. **概览**：状态卡片（在线/版本/运行时长/统计）+ mood/notes/timers 计数
2. **消息流**：左侧会话列表，右侧消息滚动 + 底部搜索框；顶部开关"实时思考链"（轮询 /api/events 渲染，带 Kind 徽标）
3. **Agent**：notes 列表（增删）、mood 编辑、timers 表格（新增/取消）、手动唤醒输入框、persona/guide 两个编辑器（textarea + 保存）
4. **QQ**：好友/群列表、群成员展开、动作面板（下拉选动作+JSON 参数框，调 /api/napcat）

视觉风格实施时出 2-3 张 AI 生成示例图供用户挑选（用户偏好），暗色为主基调预期。

## 6. 安全边界

- token 为唯一凭证；静态资源无鉴权但无敏感内容
- /api/napcat 是全能力直调（含踢人等），token 即管理员——文档注明不要泄露
- /api/file 白名单锁定 persona.md/guide.md，杜绝路径穿越

## 7. 提交切分

1. W1 feat(webui): 包骨架/鉴权/嵌入框架页 + agent 事件环形缓冲与导出钩子 + 概览页 + main 接线
2. W2 feat(webui): 消息流页与搜索 + storage.ChatSummaries
3. W3 feat(webui): agent 控制页（notes/mood/timers/wake/文件编辑器）
4. W4 feat(webui): QQ 管理页（好友/群/成员/动作直调）
