# 工具体系标准化设计（进程内 ToolModule Registry，全量文档注入，后台任务方案 B）

日期：2026-11-08 ｜ 状态：待评审 ｜ 已拍板：不做 MCP；不做热插拔；全量工具文档注入静态区；统一极简 schema；**异步直接上方案 B（不阻塞 turn/finish，完成即唤醒）**；任务可查/可停；模型可回溯自己的历史思维链与工具调用。

## 1. 目标与非目标

**目标**
- 工具注册、调用、使用说明标准化、隔离、可拼接
- 使用说明与工具定义同处一地：加工具 = 写一个 Tool 结构体，文档/schema/总览全自动
- 全量工具文档注入提示词静态区（每轮缓存全命中，变更=重启一次 miss）
- 异步后台任务：模型可选后台执行，不阻塞 turn/finish，完成即唤醒；任务可查/可停
- 行为回溯：模型可随时查询自己历史思维链/工具调用/输出，防跨 turn 割裂

**非目标**
- 不做 MCP、不做代码热插拔（Go plugin/.so 不可行）
- 不做运行时模块启停（配置+重启生效）
- persona.md 不动
- 任务队列不持久化（重启清零，进程内管理，见 §3）

## 2. 接口契约

```go
// ToolContext 一次 turn 的工具运行时依赖，模块按需取用。
type ToolContext struct {
    Store      *storage.Store
    Turn       *Turn
    Timer      *TimerStore
    Images     *imageStore
    Tasks      *TaskManager    // 后台任务管理器（§3）
    Events     *EventLog       // 行为事件流（§4，与 webui 共用同一份）
    NotesPath  string
    StatePath  string
    Admin      bool
}

// Tool 自描述：文档与定义共存。
type Tool struct {
    Name        string
    Description string  // 一句话（schema 用）
    Parameters  any     // 极简：{"type":"object"}，参数细节由 Usage 承载
    Usage       string  // 完整使用说明（markdown）：参数表/最小示例/错误形态
    Asyncable   bool    // true=允许 {"_async":true} 后台执行（下载/上传/抓取类）
    Run         func(json.RawMessage) (string, error)
}

// ToolModule 功能板块：注册单元 = 文档单元 = 配置启停单元。
type ToolModule interface {
    Name() string
    OneLiner() string
    Intro() string
    Tools(*ToolContext) []Tool
}
```

## 2.5 异步后台任务（方案 B，一步到位）

**协议**：调用参数加保留键 `"_async": true`（仅 `Asyncable` 工具接受，否则报错回填）。

**同步回执**（调用当轮立即返回）：

```json
{"task_id": 3, "status": "started",
 "note": "后台执行中，不阻塞本次思考；完成后自动送达结果，也可 task_get/task_list 随时查看"}
```

**TaskManager**（进程内，互斥锁保护）：

```go
type BackgroundTask struct {
    ID       int64       // 进程内自增
    Tool     string
    ArgsBrief string     // 参数摘要（总览展示，截断）
    Status   string      // running / done / failed / cancelled
    StartedAt time.Time
    Result   string      // 完成后的工具输出（与同步返回同形态）
    Err      error
    cancel   context.CancelFunc
}
// Start(tool, args) → task；List() / Get(id) / Cancel(id)
// 任务自带 ctx（不随 turn 打断而取消）；最长寿命 10 分钟，超时判 failed
```

**完成投递（唤醒模型）**：任务结束时把结果格式化为一行合成消息，走现有 `m.Enqueue` 管线：

```
[后台任务 #3] browse_qzone_feeds 完成: {result...}     ← 截断同 tool_result 规则
[后台任务 #3] upload_group_file 失败: <err>
```

- 会话 worker 空闲 → 立即开新 turn；正在思考 → 进同会话队列，本 turn 结束后自然接上（复用防抖/打断/队列全部既有语义）
- 任务被 `task_cancel` 取消 → 调用方同步拿到结果，**不再投递**（模型自己取消的自己知道）

**Asyncable 初版名单**：`upload_group_file`；后续浏览器/文件下载类工具入列（用户原话：下载这一类有真实需求）。查询/发送类不允许。

**不做持久化**：任务表进程内，重启清零（running 任务随进程消失，日志记录）。文件类任务重启后重调即可，不值得引入任务持久化的复杂度。

## 3. 任务管理工具组（并入 system 板块或独立 tasks 板块）

| 工具 | 参数 | 行为 |
|---|---|---|
| `task_list` | `status?`（running/done/failed/all） | 全部/按状态列出：id/tool/状态/耗时/参数摘要 |
| `task_get` | `id` | 单任务详情（含完成结果全文） |
| `task_cancel` | `id` | 取消 running 任务，返回取消确认；对已完成任务报错 |

- 任务完成若在 turn 思考期间，下一轮上下文已含投递行——模型通常无需主动查；`task_list/get` 覆盖"随时查看"与投递丢失的兜底
- "更改"后台任务 = `task_cancel` + 用新参数重调（不提供原地改参）

## 4. 行为回溯（思维链历史查询）

EventLog（现有 500 条环形缓冲，webui 同源）暴露为工具：

| 工具 | 参数 | 行为 |
|---|---|---|
| `self_events` | `kind?`（reasoning/tool_call/output/reply/...）、`chat_id?`、`limit?`(默认 50) | 按时间倒序返回自己的行为流：`#id [MM-DD HH:MM] [会话] kind: 摘要` |

- 用途：跨 turn 回忆"我上一轮为什么没回/调了什么工具/说过什么"，防前后割裂
- 覆盖范围 = 环形缓冲 500 条（约最近几小时~一天）；更早的行为靠消息库（self 发送的消息本来就在历史里）+ think 笔记
- guide 补一句行为约定：重要决定写 think 笔记（已有一半），self_events 用于近期回溯
- 缓存安全：工具结果在 tail
- 持久化事件流（JSONL 落盘）列为远期，本期不做

## 5. Registry

```go
func NewRegistry(modules ...ToolModule) *ToolRegistry  // 传入顺序固定注册
func (r *ToolRegistry) Build(ctx *ToolContext) []Tool
func (r *ToolRegistry) PromptDocs() string             // 启动时生成一次，进程内不可变
```

- 注册顺序硬规则：core → message 查询 → sticker → qzone → admin → tasks/self_events(system) → timer/mood
- 配置关闭的模块启动时不加载

## 6. 文档注入位置（缓存核心）

**工具文档块注入 system 尾部**：`persona + guide + [工具文档] + 当前账号QQ`。

```
请求 N   : [system+文档][H1..H40][tail_N]
请求 N+1 : [system+文档][H1..H41][tail_N+1]
公共前缀  = system+文档+H1..H40 → 文档每轮全命中 ✅
文档变更  → system 之后一次 miss（重启时机）✅
反例     : 文档放历史之后 → 历史每轮增长插在其前 → 文档每轮未命中（~5元/天）❌
```

- `PromptDocs()` 启动时生成一次；每 Tool 一段 `### 工具名`，板块 Intro+OneLiner 组成目录
- 全量文档 8-15k token，命中价 ≈0.0003 元/turn

## 7. Schema 策略：全量极简

所有工具统一 `{name, description, parameters:{type:object}}`；参数细节由 Usage 承载；Usage 必须含最小调用示例；风险（首用参数错误率）由错误回填+重试兜底，真机观察 turn 平均轮数。

## 8. 文档确定性规则（lint 强制）

Name/Description/Usage/OneLiner/Intro 禁止时间戳/随机/运行时状态；测试断言两次生成逐字节一致。

## 9. guide.md 迁移

迁出全部工具操作细节 → Usage/Intro；保留行为礼仪、上下文格式、通知/轮询语义；新增：_async 用法一句话、后台任务投递说明、self_events 行为约定。预期缩到 ~70 行。

## 10. 配置

```yaml
agent:
  modules:            # 缺省全 true，重启生效
    qzone: true
    admin: true
    sticker: true
    tasks: true       # 任务管理工具组
```

## 11. 实施步骤（TDD，每步全绿后进下一步）

| # | 内容 | 验收 |
|---|---|---|
| 1 | Tool 加 Usage/Asyncable；ToolContext 收敛(含 Tasks/Events)；ToolModule/Registry + 测试 | 顺序稳定/PromptDocs 确定性 lint |
| 2 | 现有工具按板块迁移（行为零变化），每工具补 Usage | 工具名集合等价测试 |
| 3 | 同轮 tool_call 并发执行 | N 慢工具墙钟 < 串行；结果顺序不变 |
| 4 | TaskManager + `_async` 协议 + 完成唤醒投递 | 回执/投递行/取消不投递/10min 超时/打断不影响后台任务 |
| 5 | task_list/get/cancel 工具 | 列表/详情/取消/未知 id 报错 |
| 6 | self_events 工具 | kind/chat_id 过滤、limit、环形缓冲边界 |
| 7 | PromptDocs 注入 system（builder 接 registry） | 文档在 system 内历史前；缓存前缀回归测试 |
| 8 | schema 全量极简 | wire 断言 parameters 仅 {"type":"object"} |
| 9 | guide.md 迁移瘦身 | 无工具参数细节；_async/后台任务/self_events 说明就位 |
| 10 | config modules | 解析+启停测试 |
| 11 | 真机观察 | miss tokens 对比；命中率 ≥ 改造前；turn 平均轮数不升；后台任务全链路（发起→思考中完成→投递→取消） |

## 12. 风险与已知代价

- 极简 schema 首用参数错误率可能上升 → Usage 最小示例兜底
- system +8-15k token：重启后首 turn 一次 miss（可忽略）
- 后台任务无持久化：重启丢 running 任务（日志记录，重调即可）
- 后台任务投递行占用一次 turn（成本 ≈ 一次轻量唤醒）；guide 引导模型对完成通知"知道了就不用说"
- 最大收益（决策人原话）：**加工具变得非常非常简单**
