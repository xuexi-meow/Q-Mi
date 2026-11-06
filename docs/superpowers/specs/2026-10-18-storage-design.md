# Q-Mi storage 子系统设计（消息记忆库）

日期：2026-10-18
状态：已与用户对齐，待实现

## 背景与目标

Q-Mi 需要把过往消息落库，作为机器人"记忆"的辅助：查单条消息原文（配合撤回）、
取某群/某人最近 N 条（上下文、复读检测）、按用户/时间过滤、关键字搜索历史消息。

## 已确认的决策

| 决策点 | 结论 |
|---|---|
| 存储引擎 | SQLite，驱动 `modernc.org/sqlite`（纯 Go，无 CGO，Windows 直接编译） |
| 入库内容 | 收到的消息、发出的消息（`message_sent`）、撤回标记、其他 notice 事件 |
| 查询能力 | 按 id 取单条、最近 N 条、按用户/时间过滤、FTS5 关键字搜索 |
| 落库时机 | handler 层用 `RegisterTyped` 注册落库 handler，分发层零改动 |
| 依赖态度 | 需要分词/其他能力时后期直接引现成库，不自己造 |
| 私聊区分 | `chat_type` 字段标记 private/group；私聊时 `chat_id` = 对方 QQ |

## 包结构与依赖方向

```
storage/            新包，只依赖 modernc.org/sqlite + 标准库
  storage.go        Open/Close、建表迁移、DSN/pragma
  store.go          消息与通知的读写方法、记录类型
  storage_test.go   :memory: 实测

handler/store.go    事件结构体 -> 记录的转换 + RegisterTyped 注册（逻辑层）

app/                挂载 Store 实例；main.go 启动 Open、退出 Close
```

依赖规则：`storage` 不 import `event`/`handler`，只暴露自己的
`MessageRecord` / `NoticeRecord` 类型；转换在 handler 层完成。

## 配置

```yaml
storage:
  enable: true
  path: "Q-Mi.db"
```

`enable: false` 时不建库、handler 落库注册短路（方法内部判空即可）。

## 表结构

```sql
-- 消息表：收到的和发出的都进这里，direction 区分
CREATE TABLE IF NOT EXISTS messages (
    message_id  INTEGER PRIMARY KEY,   -- int64，QQ 全局唯一
    direction   TEXT NOT NULL,         -- 'recv' / 'sent'
    chat_type   TEXT NOT NULL,         -- 'private' / 'group'
    chat_id     INTEGER NOT NULL,      -- 群号；私聊 = 对方 QQ
    user_id     INTEGER NOT NULL,      -- 发送者（私聊 = 对方 QQ；发出 = 机器人自身）
    raw_message TEXT NOT NULL,
    segments    TEXT NOT NULL,         -- 原始消息段 JSON，保真
    time        REAL NOT NULL,
    recalled    INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_messages_chat_time ON messages(chat_id, time DESC);
CREATE INDEX IF NOT EXISTS idx_messages_user_time ON messages(user_id, time DESC);

-- 通知表：戳一戳/入退群等，通用字段 + 原始 JSON 全量存档
CREATE TABLE IF NOT EXISTS notices (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    notice_type TEXT NOT NULL,
    sub_type    TEXT,
    chat_id     INTEGER,
    user_id     INTEGER,
    raw         TEXT NOT NULL,
    time        REAL NOT NULL
);

-- FTS5 全文索引（独立表，Go 侧维护）
-- 注：unicode61 分词器会把连续 CJK 当单个 token，SQL 侧无法按字拆分，
-- 故不用 external content + 触发器方案；由 PutMessage 在 Go 侧按字分词写入，
-- 查询词同样在 Go 侧切分并引号化（中文搜索语义为"包含所有字"，非连续子串）
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(raw_message);
```

连接参数：WAL 模式 + `busy_timeout(5000)`，`db.SetMaxOpenConns(1)` 规避
modernc 驱动的多连接写锁问题。

## 对外 API（storage 包全部公共面）

```go
type MessageRecord struct {
    MessageID  int64
    Direction  string // "recv" / "sent"
    ChatType   string // "private" / "group"
    ChatID     int64
    UserID     int64
    RawMessage string
    Segments   []byte // 原始消息段 JSON
    Time       float64
    Recalled   bool
}

type NoticeRecord struct {
    NoticeType string
    SubType    string
    ChatID     int64
    UserID     int64
    Raw        []byte
    Time       float64
}

type Store struct{ /* *sql.DB */ }

func Open(path string) (*Store, error) // 建表/迁移，幂等
func (s *Store) Close() error

func (s *Store) PutMessage(r *MessageRecord) error // 按 message_id upsert
func (s *Store) GetMessage(id int64) (*MessageRecord, error) // 不存在返回 nil, nil
func (s *Store) RecentMessages(chatType string, chatID int64, limit int) ([]*MessageRecord, error)
func (s *Store) MarkRecalled(id int64) error // 撤回只打标记，不删

type MessageFilter struct { // 零值字段 = 不过滤
    ChatType       string
    ChatID, UserID int64
    Since, Until   time.Time
    IncludeRecalled bool
    Limit          int
}
func (s *Store) QueryMessages(f MessageFilter) ([]*MessageRecord, error)

func (s *Store) SearchMessages(query string, limit int) ([]*MessageRecord, error) // FTS5
func (s *Store) PutNotice(n *NoticeRecord) error
```

错误处理：所有方法返回 error 由调用方决定；落库失败在 handler 层只告警不 panic
（记忆库失败不能影响聊天流程）。FTS 查询语法错误按普通 error 返回。

## 事件接入（handler/store.go）

在 `init()` 里注册：

| key | 动作 |
|---|---|
| `message.private` / `message.group` | `PutMessage(direction=recv)`，segments 取事件的 Message JSON |
| `message_sent.private` / `message_sent.group` | `PutMessage(direction=sent)`，私聊 chat_id = target_id/user_id，群聊 chat_id = group_id |
| `notice.group_recall` / `notice.friend_recall` | `MarkRecalled` |
| 其余全部 notice | `PutNotice`（注册到 `notice.*` 无法通配，则对每个 notice key 各注册一行） |

注意：`message_sent` 事件的字段以 NapCat 实际上报为准，实现时先抓一条真实
上报核对字段（target_id/user_id 究竟是谁），不猜。

## 测试方案（可区分结果，:memory: 库）

- PutMessage/GetMessage roundtrip：字段逐一相等；
- RecentMessages：乱序插入，验证按 time DESC 排序且条数受 limit 约束；
- QueryMessages：组合过滤（chat+user+时间窗+IncludeRecalled）各自生效；
- MarkRecalled：标记后默认查询排除，IncludeRecalled 时出现；
- SearchMessages：中文关键字命中，未命中返回空；
- 幂等：同 message_id 重复 PutMessage 不报错、不重复计行。

## 范围外（后期再说）

- 分词器（引第三方库即可）、消息清理/归档策略、LLM 上下文组装。
