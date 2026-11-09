# 配置子系统重构（分层 / 热重载 / WebUI 可配）

日期：2026-11-09 ｜ 状态：P1–P4 已实施，P5 收尾中 ｜ 范围：P1–P5

已拍板：
- 双文件分层：`Config.yaml`（手写、带注释、不进 git）+ `Config.webui.yaml`（WebUI 唯一写入目标）
- 数组整体替换，不做按索引合并
- 热重载边界见 §5；坏配置不生效，旧快照继续跑
- 模型档位：一档多候选 + 启停 + 能力协商（strategies 先只实现 priority）
- failover 保守引入：同轮最多换 1 个候选，仅网络/超时/429/5xx 触发；4xx 不重试

## 1. 目标与非目标

**目标**
- 配置加载从「启动一次性」变成「不可变快照 + 原子替换」
- 手写文件与机器写入分离，机器写入不破坏注释
- 文件变更热重载；校验失败保留 last-good
- WebUI 可查看/编辑配置（含模型），敏感值脱敏
- 模型配置现代化：一档多候选、候选级启停、优先级选择、能力匹配、保守 failover

**非目标**
- 不做配置热插拔到「无需重启的监听地址/存储路径」——这些标记为需重启
- 不做多进程/分布式配置中心
- 不做配置版本历史树（只留环形变更事件 + 日志）
- 不做 YAML 注释的机器保留（所以才有 overlay）

## 2. 分层与合并

```
Config.yaml        手写基础层（注释、说明、默认值）
Config.webui.yaml  覆盖层（机器生成，0600，进 .gitignore）
```

合并规则（`mergeMaps`）：
- map 递归合并；标量与数组整体替换
- overlay 中显式 `null` = 删除该键（回落到 base 或零值）→ 用于「恢复默认」
- 合并结果交给 viper 解码 + 现有 `validateConfig` 校验，避免两套解析逻辑

覆盖层只由 `Store.UpdateOverlay` 写入：读盘 → 变更 → 原子写（临时文件 + rename）→ 重载。WebUI 一次提交写整个子树（如 `agent.models`），保证删除可表达。

## 3. Store 契约

```go
type Paths struct{ Base, Overlay string }

func NewStore(paths Paths) (*Store, error)      // 加载+校验；失败 = 启动失败（语义不变）
func (s *Store) Snapshot() *Config               // 只读，不可修改；整体替换保证无撕裂读
func (s *Store) Reload() error                   // 读盘→合并→校验→交换→通知；失败保留旧值
func (s *Store) UpdateOverlay(mutate func(map[string]any)) error
func (s *Store) Overlay() map[string]any         // 覆盖层副本（WebUI 展示来源）
func (s *Store) Subscribe(fn func(old, new *Config))
func (s *Store) OnError(fn func(error))
func (s *Store) Watch() error                    // fsnotify，500ms 防抖，幂等
func (s *Store) Close() error
```

不变量：
- `Snapshot()` 返回的 `*Config` 视为不可变；任何修改必须经副本 + 整体替换
- 通知在锁外执行，订阅者可以安全调用 `Snapshot()`
- 内容未变化不通知（`reflect.DeepEqual` 比较），避免空保存触发重建
- 订阅回调按注册顺序串行执行

## 4. 校验与失败语义

| 场景 | 行为 |
|---|---|
| 启动时校验失败 | 返回 error，进程启动失败（与现状一致） |
| 运行中重载失败 | 保留旧快照；`OnError` 告警 + `KindConfig` 事件；overlay 不落盘（`UpdateOverlay` 先校验后写） |
| overlay 文件缺失 | 视为空覆盖层，合法 |
| 写入中断/半截文件 | 原子写规避；读盘 YAML 解析失败按「重载失败」处理 |
| 内容相同 | 不通知、不重建 |

## 5. 热重载分级

| 级别 | 字段 |
|---|---|
| 热生效 | `agent.models`、`wake_routes`、`temperature`、`max_tool_rounds`、`debounce`、`self_wake`、`context.*`、`persona_file`、`guide_file`、`modules` |
| 热生效但击穿一次缓存 | `modules`（重建 registry + PromptDocs）、persona/guide 内容变更 |
| 必须重启 | `netsetting.*`、`webui.host/port/token`、`storage.path/enable`、`bot.qq_number`、`agent.enable`、`browser_executable`、`workspace_dir`、`image_dir` |

WebUI 对「必须重启」字段只读展示，并标注「重启后生效」。

## 6. 运行时接入（P2）

- `app.App` 持有 `*config.Store`，`GetApp().Config` 保留为 `Store.Snapshot()` 的兼容入口
- `agent.Manager` 改为每轮取快照（不再持值拷贝）；模型池用 `atomic.Pointer` 整体替换，在途请求继续用旧 client（client 无状态）
- `DefaultBuilder.Apply(cfg)`：更新 max/trim/multimodal/workspace/persona 路径；persona/guide 正文仍每请求热读
- `modules` 变更：重建 `ToolRegistry` + `builder.SetToolDocs(PromptDocs())`，本轮之后生效
- `self_wake` 变更：停旧 ticker、起新 ticker（幂等）
- 需重启字段变更：不应用，记一条「需重启」事件，WebUI 顶部提示

## 7. 模型档位（P3）

```yaml
agent:
  models:
    chat:
      enable: true
      strategy: priority          # priority（本期唯一实现）
      candidates:
        - name: deepseek
          enable: true
          base_url: "https://api.deepseek.com"
          api_key: "sk-..."
          # api_key_env: DEEPSEEK_API_KEY
          model: "deepseek-flash"
          capabilities: [tools, vision]
          context_window: 64000
          cooldown: 5m
  wake_routes: { chat: chat, background: background }
```

- 选择：档 `enable` → 候选 `enable` → 能力匹配（图片→vision、视频→video、主循环恒需 tools）→ 冷却过滤 → priority 取首个
- 回退链：`wake_routes` 未命中 → 同名档 → `chat`；chat 档全部候选停用时退回顶层三件套
- 能力不足时降级：先找支持 vision 的候选；一个都没有时退回纯文本候选并把图片替换为 `（N 张图片已省略）` 标注
- 旧写法 `models.<tier>` 单对象自动视为单候选（name = 档名）
- `api_key` 与 `api_key_env` 二选一；候选没写接入参数时复用顶层三件套客户端
- failover：`max_attempts`（默认 2，上限 5）；仅网络/超时/429/5xx 换候选，4xx 直接失败；失败候选按 `cooldown`（默认 5m）冷却，成功后清零
- usage 按候选名统计（`UsageByModel`），事件文本追加 `candidate=`、`tier=`

## 8. WebUI 配置（P4）

路由（均需 Bearer token）：
- `GET /api/config`：effective 值 + 每键来源（base/overlay/default）+ 生效级别；`api_key` 返回掩码
- `PUT /api/config`：写入 overlay 子树，先校验后落盘
- `POST /api/config/validate`：只校验不写
- `POST /api/config/reload`：手动重载
- `POST /api/models/test`：对指定候选发 `max_tokens=1` 请求测连通

页面：现有 Alpine 单页新增「配置」tab，分区为模型（候选 CRUD/启停/优先级/测试）、行为参数、工具板块、只读区（网络/存储/密钥）。停用全部候选、删除档位需二次确认。变更记脱敏 diff 到事件日志。

## 9. 实施顺序与验收

| 期 | 内容 | 验收 |
|---|---|---|
| P1 | `utils/config` 新增 Store：分层合并、快照、重载、原子写、监听；不改调用方 | 分层/数组替换/null 删除/坏配置保留旧值/订阅/监听单测全绿 |
| P2 | 运行时接入 + 热重载分级应用 | 改 temperature/模型/模块后无需重启生效；改端口不生效但提示 |
| P3 | 模型多候选 + 启停 + 能力协商 + failover | 选择顺序、能力降级、冷却、failover、旧配置兼容 |
| P4 | WebUI 配置页 + 写回 + 脱敏 + 连通性测试 | 页面可改模型并即时生效；密钥不回传明文；坏值被拒 |
| P5 | 回归、示例配置重写、文档 | `go test ./...` 全绿；`Config.example.yaml` 覆盖新结构 |

## 10. 风险

- 与 WIP 冲突：P2 动的 `manager.go/context.go/wiring.go` 刚收尾，实施前先确认基线全绿（已确认）
- 热重载与并发：所有读侧必须走 `Snapshot()`，禁止缓存 `*Config` 跨轮
- 模型重建导致 prompt cache 一次性 miss：接受，作为配置变更的成本
- Windows 文件监听：编辑器替换文件时事件形态不同，统一监听父目录 + 文件名过滤

## 11. 实施记录

| 期 | 状态 | 落点 |
|---|---|---|
| P1 | 完成 | `utils/config/store.go`（分层/快照/重载/原子写）、`watch.go`（fsnotify 防抖）、`decodeConfig` 抽取 |
| P2 | 完成 | `config/diff.go` 分级；`app.App` 持 Store + `Current()`；Manager `config()/ApplyConfig/AttachConfigStore`；builder `Apply`；`main` 启动 Watch |
| P3 | 完成 | `ModelCandidate`/`ModelTier.candidates`/`wake_routes`/`max_attempts`；`agent/modelpool.go` 选择/能力/冷却/failover；`UsageByModel` |
| P4 | 完成 | `webui/config.go` 五个接口 + 脱敏 + 重启字段拦截；`index.html` 配置页 |
| P5 | 完成 | 移除旧 `clients/clientFor` 双份状态；`Config.example.yaml` 复核；示例配置加载测试；全量回归 |

已知边界：
- 档位不支持删除（用 `enable: false` 停用）；覆盖层里显式 `null` 可手工删除
- 配置变更历史只进 EventLog 环形缓冲，没有独立版本树
- WebUI 页面是 `go:embed` 的，页面结构改动需重启进程才可见（配置数据本身热重载）
