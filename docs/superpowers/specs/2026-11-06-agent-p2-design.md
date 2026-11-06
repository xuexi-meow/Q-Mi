# Q-Mi Agent P2 设计：思维链工具循环（judge 移除，主模型全权决策）

日期：2026-11-06
状态：待评审（v3：加 help/think 工具，明确跨 turn 思考连贯性方案）
前置：P1 已真机验证。混合上下文与昵称入库部分沿用 v1 结论（见本文 §2）。

## 1. 核心模型

**不再有代码侧的"回不回"判断**。每条（防抖合并后的）消息都开启一轮"思考 turn"：

- 主模型看到全会话混合的近期消息流
- 它可以调用工具：查询、发消息、调整状态……**工具返回结果后思维链继续**，不结束
- 它随时可以调用 **`finish` 工具主动结束思考**（可以发了消息再结束，也可以什么都不发直接结束）
- "回不回"= 模型调不调 send_message 的自决；"情绪"= 模型自调 update_mood

安全阀：轮数上限（默认 10）强制结束；finish 前已发出的消息不受影响。

## 2. 混合上下文（沿用 v1 结论）

### 2.1 昵称入库

- messages 表加 `sender_name TEXT NOT NULL DEFAULT ''`，落库时写 `Sender.Nickname`（群名片 card 优先）；message_sent 写"我"；旧库幂等迁移
- MessageRecord 加字段；旧行昵称空 → 展示回退 QQ号

### 2.2 上下文结构

```
system:   persona + agent格式说明 + 自身QQ号 + 当前情绪状态   ← 静态,保缓存
user/assistant: 全会话混合的最近 N 条（带来源）              ← 只追加,块裁剪
user:     [思考笔记块] + 本轮新消息 + 当前时间               ← 末尾
```

历史行格式：

```
[11-06 23:29] [群:928952016] <香菜不吃屿烬燃(3929930584)> 这个我不背锅！！！！
[11-06 23:31] [私聊:3394859101] <小奶猫雪溪(3394859101)> 在吗
[11-06 23:32] [群:928952016] <我> 收到
```

全局按时间取最近 N 条再正序；@ 在文本中保留 `<@QQ号>` 形态。

## 3. 工具集（P2）

| 工具 | 参数 | 返回 | 备注 |
|---|---|---|---|
| help | （无） | 全部工具的详细用法说明 | 文档以代码中工具定义为唯一源头，help 输出即定义；system 只写 agent 格式与行为准则 |
| get_recent_messages | chat_type?, chat_id?, limit≤100 | 消息行 JSON 数组 | 复用 QueryMessages |
| search_messages | query, chat_type?, chat_id?, limit≤50 | 消息行 JSON 数组 | 复用 SearchMessages |
| send_message | chat_type, chat_id, text | {message_id} | 经 bot.Reply 发出；一个 turn 可多次调用（分条气泡） |
| update_mood | mood | {} | 写 agent/state.json，注入 system |
| think | note | {} | **思考笔记**：持久化到 agent/notes.json，下一轮注入上下文末尾 |
| **finish** | （无参数） | — | **唯一终止工具**；调用即结束本轮思考 |

- 触发范围：私聊、群@、**群未@**（都进思考，模型自决回不回）。成本由防抖合并（3s）控制；后续嫌贵可加开关
- 工具报错返回 `{"error": ...}`，思维链继续（模型自行调整）

## 3.5 思考连贯性

- **turn 内**：消息数组保留全部 assistant(tool_calls) 与 tool 消息，思维链天然连续
- **turn 间**：靠 think 笔记——agent/notes.json 按时间存最近 50 条，下一轮思考时作为"[思考笔记]"块注入**最终 user 消息头部**（不进 system，保缓存前缀；笔记只在尾部变化）
- 模型若返回 reasoning_content（DeepSeek 系）不注入上下文，仅 debug 落盘，便于观察
- 上轮已发送的消息、改过的 mood 本来就在历史/状态里，不额外搬运
- agent/notes.json 与 state.json 均进 .gitignore

## 4. 思考循环（Manager.handleTurn 重构）

```
1. 组装混合上下文 + 本轮新消息
2. 调 LLM → 若响应含 tool_calls:
     逐个执行(除 finish) → 结果作为 role=tool 消息追加 → 回到 2
   若响应为纯文本(未调工具): 视为直接回复发出(兼容不会用工具的模型),结束
   若调用 finish: 结束
3. 轮数 ≥ max_tool_rounds: 强制结束,记日志
4. 全程 debug 落盘(每轮请求/工具结果)
```

- LLM 超时/网络错误：本轮结束，记日志，不重试（消息在库里，下轮可见）
- send_message 失败：工具结果返回 error，模型可决定换方式或放弃

## 5. 插话打断

- 紧急度用**代码规则**（judge 移除后的替代）：新消息来自**私聊或带@** → 若该会话正在思考 → cancel 当前 LLM 请求，pending 合并成新 turn 立即重开
- 群未@消息：入 pending 正常排队，不打断
- 被打断的轮次不补处理（消息都在 storage，新 turn 上下文自然包含）；debug 记录 `"interrupted": true`
- 工具执行中不取消（NapCat 调用是短请求，等它返回再检查取消标记）

## 6. 配置增量

```yaml
agent:
  max_tool_rounds: 10     # 思考轮数上限
  # judge 块取消,不引入
```

## 7. 测试策略

- 昵称入库/旧库迁移（同 v1）
- 混合上下文：跨会话行格式、块裁剪前缀稳定、角色正确
- 思考循环：假 LLM 依次返回 [tool_calls(help), tool_calls(think+查询), tool_calls(发消息), finish] → 断言 tool 消息回填、笔记持久化并注入下轮、真实发出消息、循环终止；纯文本响应直接回复的兼容路径；轮数上限强制结束
- 打断：慢 LLM 期间来 @ 消息 → 第一请求 ctx 取消、新 turn 重开；群未@ 不打断
- 端到端：未@群消息 → 模型自决不发 finish → 无回复；@ → 发消息后 finish

## 8. 提交切分（预计）

1. feat(storage): 昵称入库与旧库迁移
2. feat(agent): 混合上下文
3. feat(agent): 思考循环与工具集(含 send_message/finish)
4. feat(agent): 插话打断
5. docs: P2 spec v3
