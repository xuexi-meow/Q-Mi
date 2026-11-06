# Q-Mi Agent P1 设计：QQ 交互最小闭环

日期：2026-11-06
状态：待评审
前置：chain/storage/bot 已完成并验证（真机闭环 2026-11-06 通过）；路线图见项目记忆 q-mi-agent-roadmap。

## 1. 目标与非目标

**目标（P1）**：AI 能像联系人一样参与 QQ 对话——私聊消息、群里 @机器人 → 组装上下文 → 调 LLM → 回复。为缓存命中优化上下文结构，为连发消息做防抖合并。

**非目标（后续阶段，本文只留契约不实现）**：
- P2：判断模型（回不回/情绪状态）、思考中插话取消
- P3：主动发消息（轮询/回调）、查询历史工具、网页浏览工具
- P4：QQ 空间等账号级操作

## 2. 架构

新增包 `Q-Mi/agent`，与现有层的关系：

```
handler(现有) ──消息事件──> agent
agent ──查历史──> storage（只读，新增只读接口）
agent ──HTTP──> OpenAI 兼容 API（新 llm 客户端）
agent ──回复──> bot.Reply ──> utils/request
```

- agent 自行在 init 注册 `message.private` / `message.group`（与 handler/store.go 同模式：事件到来时才查 `app.GetApp()`，配置未启用即跳过）。handler 包现有代码不动。
- agent 不写入 storage：发出的消息已由 handler/store.go 的 message_sent 落库，agent 读历史时自然包含自己的发言。
- persona、状态等文件型资产放 `agent/` 目录下。

## 3. 配置块

```yaml
agent:
  enable: true
  base_url: "https://api.example.com/v1"   # OpenAI 兼容，含 /v1
  api_key: "sk-..."
  model: "deepseek-chat"                    # 主模型
  persona_file: "agent/persona.md"          # 固定系统提示词
  debounce: 3s                              # 防抖窗口
  temperature: 0.8
  debug: false                              # 每个 turn 落盘完整请求/响应
  history:
    max: 40                                 # 上下文携带的历史条数上限
    trim: 20                                # 超限时一次从头砍掉的条数(保缓存前缀)
```

- 校验（进 utils/config 的 validateConfig）：enable 时 base_url 为合法 http(s) URL、api_key/model 非空、history.max >= history.trim > 0、debounce 为正的 duration。
- **P2 预留**：judge 模型块（judge.base_url/api_key/model），未配置回退主模型。P1 不实现 judge，但配置结构注释中说明扩展位。
- **密钥卫生**：Config.yaml 已被 Git 跟踪且近期将写入真实密钥。P1 包含一个任务：新增 `Config.example.yaml`（占位密钥）并把 `Config.yaml` 移入 .gitignore（用户已确认的本地文件保留在磁盘）。

## 4. persona 文件

- `agent/persona.md`：他自己维护的固定系统提示词（人格、说话风格、行为边界）。
- 实现 P1 时提交一份最小占位版（几行说明"你是 QQ 上的 AI 成员"），他后续自行改写。
- 文件缺失：启动告警一次，用内置兜底提示词，不阻断运行（便于先跑通再写真人格）。

## 5. 触发规则（P1）

| 场景 | 行为 |
|---|---|
| 私聊 | 一律响应 |
| 群聊 @机器人 | 响应 |
| 群聊未 @ | 忽略（P2 交判断模型） |

@检测用现有 `bot.AtMe`；@段本身会进上下文但提示词要求模型忽略 "@QQ号" 字样。

## 6. 会话队列与防抖

以 (chat_type, chat_id) 为键，每会话一个串行 worker（懒创建 goroutine + channel，不回收——会话量级个人机器人无压力）：

1. 消息到达 → 追加进该会话的 pending 缓冲，起/重置一个 debounce 定时器（默认 3s）
2. 定时器到 → 把 pending 合并成一个 turn 投递给 worker
3. worker 忙 → turn 排队（**不打断正在进行的生成**，P1 无 abort）
4. worker 处理 turn：组装上下文 → 调 LLM → `bot.Reply` 发文本
5. LLM 失败：重试 0 次，记日志即止（消息已在库里，下轮可见）

连发的多条消息在 turn 内按顺序全部呈现；同一用户 3 秒内的分条消息对模型表现为多条 user 消息，不做文本拼接（保留原始节奏）。

## 7. 上下文组装（缓存分层）

```
messages = [
  system:  persona + 机器人自身QQ号               ← 完全静态,缓存锚点
  user/assistant 交替: 最近 N 条历史              ← 只追加
  user:   本轮新消息(防抖窗口内合并) + 当前时间    ← 每次变,放末尾
]
```

注意：当前时间**不放 system**——否则缓存前缀随时间失效，把时间放进末尾的新消息里。

- 历史来源：`storage.QueryMessages(MessageFilter{ChatType, ChatID, Limit})`，默认排除已撤回。
- 每条格式（"较详细信息"）：`[MM-DD HH:MM] <昵称或QQ> 内容`。P1 库里没有昵称，用 QQ 号；自己（direction=sent）标注为"我"。**增强项 P1.5**：get_friend_info / get_group_member_info 取昵称并缓存。
- 裁剪：超过 `history.max` 时按 `history.trim` 块跳删，保证两次调用间历史前缀字节级一致 → 命中 prompt cache。
- 更早的消息 P1 不提供查询工具（P3），但格式设计已保证加工具时无需改历史结构。

## 8. LLM 客户端（Q-Mi/agent/llm.go）

- 仅实现 `POST {base_url}/chat/completions`，非流式（P1 回复一次性发出；流式/分条气泡留到活人感迭代）。
- 请求体只填 model/messages/max_tokens/temperature（可配，默认 0.8）；不实现 tool_calls（P3）。
- 响应取 `choices[0].message.content`；retcode 式错误、HTTP 非 200、超时（默认 120s）均显式报错。
- 客户端无全局状态，`NewClient(cfg) ` 纯构造，测试用 httptest 起假 OpenAI 服务。

## 9. 可定制与调试（为实验上下文策略服务）

上下文怎么做更好是开放问题，所以组装环节必须是**可替换的代码接缝**，不是写死在流程里：

- **ContextBuilder 接口**：`type ContextBuilder interface { Build(turn Turn) []llm.Message }`。agent 流程只依赖该接口；默认实现 `DefaultBuilder`（persona + 块裁剪历史 + 新消息）。替换实现 = 换一个构造参数，不动队列/触发/发送逻辑。
- **Turn 结构**：进 builder 的输入是显式结构体（chat 上下文、本轮消息、storage 引用），保证任何自定义 builder 拿得到全部原料。
- **调试落盘**：`agent.debug: true` 时，每个 turn 把完整请求 JSON 与响应写入 `agent/debug/<chat_type>-<chat_id>/<时间戳>.json`，直接可回放对比。目录进 .gitignore。
- 其余可调点已在配置暴露：persona 文件、history.max/trim、debounce、temperature。

## 10. 错误处理与边界

- agent.enable=false 或配置缺失：不注册行为，零影响。
- storage 未启用：历史为空，仅当前消息也可对话（告警一次）。
- 回复为空字符串：不发消息，记日志。
- 单条消息超长（>20000 字符）：截断并标注。
- 会话 worker 内 panic：recover 记日志，worker 存活（沿用 event.invokeHandler 之外自己再兜一层，因为 worker 是长驻 goroutine）。

## 11. 测试策略

- llm 客户端：httptest 假 OpenAI（成功/retcode 错/超时/坏 JSON）。
- 上下文组装：给定固定历史与 persona，断言 messages 结构与分块裁剪行为（裁剪后前缀不变的回归测试）。
- 防抖与队列：假时钟或短窗口（ms 级）验证合并、串行、排队不打断。
- 端到端：httptest 假 NapCat + 假 OpenAI，Dispatch 真实消息 JSON → 断言回复内容发到 /send_group_msg|/send_private_msg。
- 真机验证：私聊与群里 @ 各发一条，确认回复与 Q-Mi.db 记录。

## 12. 提交切分（预计）

1. feat(agent): llm 客户端与配置块
2. feat(agent): 上下文组装与 persona
3. feat(agent): 会话队列防抖与事件接线
4. chore: Config.example.yaml 与密钥出库
