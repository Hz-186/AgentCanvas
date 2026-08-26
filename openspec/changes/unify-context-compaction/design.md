# Design: unify-context-compaction

> 决策依据：`exploration-handoff.md`（22 项决策，A=用户原话 / B=代码证据）。参照基准：Codex @ d52478c5。

## 1. 架构总览

```
                    ┌────────────────────────────────────┐
                    │  internal/runtime/compaction (新)   │
                    │  Entry 契约 + 单一 Compact 算法      │
                    │  常量组：20000 / 0.90 / 20s / 前缀   │
                    └───────────▲────────────▲───────────┘
                 FromMessages │            │ FromChat
                ┌─────────────┴───┐   ┌────┴──────────────────┐
   PreTurn/手动 │ conversationctx │   │ agent.Runner (MidTurn) │
                │ coordinator     │   │ compactRuntimeTranscript│
                └────────┬────────┘   └────┬──────────────────┘
              持久化外壳：快照表(不变)   持久化外壳：runtime 快照行(不变)

   会话消息表 messages（+content_type +metadata_json）
     ▲ 实时写入钩子（父 run 每产生一条即写；子代理委派对由父 sink 写 2 条，
     │  DelegationDepth>0 不挂 sink；完成路径不写）
     │ 幂等：checkpoint.PersistedMessageCount 写入游标（压缩后重置）
```

单一算法，两个持久化外壳（D8：快照持久化保留）；历史统一为带类型的消息表（D5 实时写入、D6 子代理两条）。

## 2. 核心包 `internal/runtime/compaction`

### 2.1 Entry 契约（D3）

```go
type Entry struct {
    MessageID   int64           // 来源为消息表时的行 ID；内存历史为 0
    Role        string          // user/assistant/tool/developer/system
    ContentType string          // text/function_call/function_call_output/reasoning/system_echo
    Content     string          // 文本内容或 tool output 内容
    ToolCallID  string          // function_call/function_call_output 的调用关联
    ToolName    string
    Arguments   json.RawMessage // function_call 的参数
}
```

- `FromMessages([]conversation.Message) []Entry`：消息行 → 条目；`metadata_json` 解析出 ToolCallID/ToolName/Arguments；解析失败降级为 text 条目（容错，不报错）。
- `FromChat([]llm.ChatMessage) []Entry`：transcript → 条目；assistant 带 `ToolCalls` 的消息拆为（可选 1 条 text + N 条 function_call）；role=tool → function_call_output。
- `ToChat([]Entry) []llm.ChatMessage`：条目 → 请求消息；**相邻的 text + 连续 function_call 条目合并为一条 assistant 消息（Content=文本部分，ToolCalls 数组）**，其后同批 function_call_output 各自为 role=tool 消息——保证回放给 LLM 时是合法的调用/输出配对。

### 2.2 Compact 算法

```go
type Request struct {
    SystemPrompt, CompactPrompt string
    Provider llm.ChatProviderConfig; ProviderID int64; Model string
    ParentSummary string        // 滚动压缩的上一份摘要（可空）
    UserBudgetTokens int        // 默认 20_000
    PerEntryLimitTokens int     // 单条送总结上限，默认 8_000（超长 tool output 截断）
    Timeout time.Duration       // 默认 20s
}
type Result struct {
    Summary string; Retained []Entry; Usage llm.Usage; ModelCalls int
}
func Compact(ctx, client llm.ChatClient, req Request, entries []Entry) (Result, error)
```

流程（收敛自两套重复实现）：
1. 组装总结输入：`ParentSummary` 非空则以摘要前缀作为首条 user 输入（滚动，D7）；随后按序追加条目内容——**排除** `system_echo` 与 `reasoning`（D19/D9）；单条超过 `PerEntryLimitTokens` 的截断（D7 保护）。渲染格式（审查补充）：function_call 条目为 `[tool call: ToolName] Arguments 原始 JSON`；function_call_output 为 `[tool result: ToolName] Content`；text 条目原样。
2. 调用 `client.Chat`（D20：统一 Chat，不用 ChatWithTools）；遇 `llm.ErrContextWindowExceeded` 丢最早一条重试，剩 1 条仍失败 → 返回 `ErrCompactionFailed`。
3. 从尾向前保留 Role=user 且 ContentType=text、内容不以摘要前缀开头的条目，预算 `UserBudgetTokens`；首条超预算者截断保留后终止。
4. 摘要为空 → 兜底 `"(no summary available)"`。

常量组（单一来源，D18 数值不变）：`UserMessageBudgetTokens=20_000`、`ThresholdRatio=0.90`、`SummarizeTimeout=20s`、`SummaryPrefix="SUMMARY:\n"`、`FallbackSummary="(no summary available)"`、`PerEntryLimitTokens=8_000`。

## 3. 消息条目映射（入库形态）

| 运行时产物 | 行 | role | content_type | metadata_json |
|---|---|---|---|---|
| 用户输入 / 最终文本 / 中间助手文本 | 1 行/条 | user / assistant | text | NULL |
| 助手响应文本部分（非空时） | 1 行，**先于 function_call 行写入** | assistant | text | NULL |
| 助手响应含 N 个工具调用 | N 行（每调用 1 行） | assistant | function_call | `{"tool_call_id","tool_name","arguments"}` |
| 工具结果 | 1 行/结果 | tool | function_call_output | `{"tool_call_id","tool_name"}` |
| 子代理委派对（由父 run sink 写入，§5 裁决） | 2 行 | assistant / tool | function_call / function_call_output | 同上（tool_name=子代理委派工具名） |
| `/compact` 与 `Context compacted.`（§6 打标） | 1 行/条 | user / assistant | system_echo | NULL |
| reasoning | 本期不写入（枚举预留，D9） | — | — | — |

拆分粒度对齐 Codex 的条目模型（每个 function_call 是独立 item），这也是 `ToChat` 能无损合并回放的前提。**写入顺序钉死（审查补充）**：同一助手响应的 text 行先写、随后按 ToolCalls 顺序写 function_call 行——与 FromChat 的「可选 1 条 text + N 条 function_call」拆分顺序一致，也符合供应商配对校验要求。

## 4. 实时写入与幂等（D5，最高风险点）

### 4.1 写入钩子

- 新增 `MessageSink` 接口（`runtime/agent` 定义，`agentruntime`/`turn_worker` 注入实现）：`PersistEntries(ctx, entries []compaction.Entry) (firstID int64, err error)`，实现内部按 §3 映射写消息表并返回首行 ID。
- 挂载点枚举（transcript 追加点，`runner.go`）：assistant + tool 批次入 transcript 处（:374-377）、反思反馈 `maybeReflect` 追加 assistant 消息处（:379-382，按 spec「每产生一条助手消息即写入」同样覆盖）。
- **子代理抑制规则（审查裁决）**：`DelegationDepth > 0` 的 run 不挂 MessageSink——子代理自身 transcript 永不写入父会话（spec「子代理自身的中间轮次 MUST 不写入父会话」）。注入点在 `execution.go`：`rc.DelegationDepth > 0` 时置空。
- 注入接线：`agentruntime/dependencies.go` 现仅有只读 `MessageHistoryReader`（:35-38），Task 5 需补 sink 注入点（经 dependencies 或 application 层注入到 RunRequest）。
- 写入失败策略：记录错误事件但不中断运行（降级为旧行为：运行内可见、跨轮丢失）——运行可用性优先。

### 4.2 恢复幂等（暂停/审批后恢复不得重复入库）

- `Checkpoint` 增加 `PersistedMessageCount int`（已写入消息表的 transcript 条目计数），随 `hydrateCheckpoint` 一起进 `agent_run_checkpoints` JSON。
- **两个 checkpoint 构造器都必须携带计数（审查补充）**：`hydrateCheckpoint`（`runner.go:786`）与 `checkpointFromMessages`（`runner.go:752`，用于审批暂停 :707 及其他暂停路径 :600）——后者缺计数则审批恢复必重复入库。
- 恢复时（`resumer.go` 重建 `ResumeTranscript` 后）：跳过前 `PersistedMessageCount` 条（已在库），后续新条目续接写入。
- **与运行内压缩的交互（审查裁决）**：压缩成功后游标重置为 `len(压缩后 transcript)`——保留的 user 条目与 SUMMARY 条目一律视为「已持久化/豁免」（保留 user 条目已有对应消息行：初始用户输入 / goal-continuation 行；SUMMARY 永不入表，见 §4.4）；压缩后新产生的条目从 0 重新计数。恢复按新游标跳过，保证摘要/保留条目 0 次入库、新条目不重不漏。
- 不依赖 DB 唯一索引（避免二次迁移）；单 runner 串行写入，无并发写同一 run。

### 4.3 最终答案不重复

- 最终助手消息也是 transcript 一员，运行内已实时写入。契约变更：`RunResult.Output` 增加 `assistant_message_id`（runner 写入的最终条目行 ID）。
- `turn_worker.go:636` 处的写入改为：有 `assistant_message_id` → 直接引用（`AssistantMessageID` 指向已存在行，仅补 run 级字段更新）；无（零迭代/异常路径）→ 保留现有创建逻辑兜底。

### 4.4 与压缩的交互

- 运行内压缩替换的是**内存 transcript**；已写入的消息行是事实记录，不删除、不修改（对齐 Codex"原始条目留盘"）。摘要仅存快照表，不入消息表。
- 跨轮压缩输入 = `FromMessages(全部活跃消息)`（含 tool 行，D7）；快照锚点语义不变：`First/LastMessageID` 仍锚定保留的 user-text 消息（D15），`filterFrozen`/`composeWindow` 逻辑不变。
- **窗口回放**：`composeWindow` 的尾段现在可能含 tool 行；`buildPreparedConversationContext` 的 Render 必须走 `ToChat` 合并出合法配对，不能按行直发（role=tool 无配对会被供应商拒绝）。
- **读侧消费者影响声明（审查补充）**：handoff 附表 11 处读侧消费者中——fork 正确复制新列并遵循索引排除（Task 5 修复）；遗留构建器 `assembly.go:21-82`（Coordinator==nil 回退路径）可能产生无配对 role=tool 块，仅测试/无 coordinator 场景触发，**本期容忍**；查询改写（`assembly.go:192-210` 喂 planner）、improvement、dream_worker、extraction 等只读取文本内容，tool 行的 JSON 内容本期容忍（不截断内容、仅以 content_type 区分），行为均不变。

## 5. 子代理委派对唯一写入方裁决（D6，审查裁决）

**唯一写入方 = 父 run 的 sink**。委派工具调用与其结果本身就在父 run transcript 中（`runner.go:374-377`），由 §4.1 sink 按 §3 映射写入：
1. `function_call`：role=assistant，tool_name=委派工具名，arguments=子任务参数（含子代理标识），RunID=父 run。
2. `function_call_output`：role=tool，content=子代理最终输出（失败时为错误信息 → 仍恰 2 条），tool_call_id 与上一条配对。

裁决依据与配套约束：
- 子代理 run 与父会话共享 `ConversationID`（`subagent_tool.go:157` → `subagent.go:80`），且走同一条 `execution.go` 路径——若无条件挂 sink，子代理完整 transcript 会泄漏进父会话。故 §4.1 抑制规则（DelegationDepth>0 不挂 sink）是必要前提。
- 子代理完成路径**不再**额外写消息：`completeSubagentRun`（`run_control.go:202`，经 `subagent.go:186` / `run_control.go:197` 调用）与 `turn_worker.go:612-633` 分支仅更新 run/turn 状态。若不裁决，同一次委派将落 4 行（sink 2 行 + 完成路径 2 行），违反「恰好两条」。
- 子代理自身 transcript 不入库（仍留 `agent_run_steps` 审计）。

## 6. 索引排除（D21）

- `message_repo.Create` 的 `enqueueContextResource` 调用前判断 `ContentType`：非 `text` 不入 context_resource 索引。
- ES 会话搜索 `IndexMessage` 调用点完整清单（审查修正，不止两处）：
  - user 消息（`service.go:813`）与最终答案（`turn_worker.go:671`）：text，保留。
  - **fork 复制（`service.go:600-613`，每条复制消息调用 IndexMessage，:612）**：改造后会把 tool 行当 text 复制进索引——Task 5 修复：fork 复制 `ContentType`/`MetadataJSON` 两字段，且非 text 行跳过 `IndexMessage`。
  - 实时写入路径（sink）不调用 `IndexMessage`，天然排除；子代理委派对由父 sink 写入（§5），同样不调用。
- **system_echo 打标责任方（审查补充，D19）**：`/compact` user 消息（`service.go:757-764` 创建 → :813 索引）创建时置 `ContentType=system_echo` 且跳过 `IndexMessage`；`Context compacted.`（`execution.go:169-172` 零迭代、无 assistant_message_id，由 turn_worker 兜底创建）创建时置 `ContentType=system_echo` 且跳过 `IndexMessage`。两处均由 Task 5 覆盖。

## 7. API 与前端（D11/D9/D19）

- Message DTO 增加 `content_type`、`tool_call_id`、`tool_name`（metadata_json 解析，解析失败留空）。
- `web/src/types/api.ts`：`Message` 增加同名字段；`MessageRole` 不变（developer 仍不出现于 API）。
- `ChatPage.tsx`：`visibleChatMessages` 过滤条件改为"渲染 text 的 user/assistant + function_call/function_call_output"；排除 `system_echo` 与 `reasoning`。工具条目渲染为折叠卡（默认折叠，标题=工具名，展开显示参数/结果）。

## 8. 迁移（D4）

新迁移（序号取当前最大+1）：

```sql
ALTER TABLE messages ADD COLUMN content_type varchar(32) NOT NULL DEFAULT 'text';
ALTER TABLE messages ADD COLUMN metadata_json json DEFAULT NULL;
```

旧行缺省 `text`，自然共存（D10）。down 迁移删除两列。

## 9. 明确不做

- 不回填旧会话工具条目（D10）。
- 不实现 reasoning 写入（无数据来源，D9）。
- 不改变 token_budget 旁路行为（D17）、developer 角色（D16）、常量数值（D18）。
- 不引入 Codex 的远程压缩/JSONL rollout（服务端架构不适用）。

## 9b. 与 Codex 的有意偏离（审查补充）

- **摘要前缀**：Codex 用长句模板（`prompts/templates/compact/summary_prefix.md`，"Another language model started to solve this problem…"）；AgentCanvas 保留字面量 `"SUMMARY:\n"`——D18 用户拍板常量数值不变，且既有快照链按该前缀识别。
- **PerEntryLimitTokens=8,000**：本设计新增的保护参数，Codex 无对应项（handoff D7 拍板复用现有截断语义并收敛到核心包）。

## 10. 风险与缓解

| 风险 | 缓解 |
|---|---|
| 恢复重放导致重复入库 | §4.2 写入游标 + Task 5 的幂等测试 |
| 窗口回放非法配对（role=tool 无 tool_calls 前序） | §4.4 强制 ToChat 合并 + Task 3 回放测试 |
| 实时写入失败影响运行 | §4.1 降级策略（记录不中断） |
| 超大 tool output 挤爆总结窗口 | §2.2 PerEntryLimitTokens 截断 + 既有的丢最早重试 |
| 无 Go 工具链无法本地跑测试 | 测试代码随任务产出；交付清单列明需在有工具链环境执行 `go test ./...`；前端 typecheck/vitest 本地执行 |
