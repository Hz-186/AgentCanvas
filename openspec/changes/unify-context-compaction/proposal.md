# Proposal: unify-context-compaction

## Why

AgentCanvas 目前有两套逐行重复的上下文压缩实现（跨轮 `conversationcontext/coordinator.go` 与运行内 `agent/auto_compaction.go`，12 处重复），且会话历史只存 user + 最终答案——工具调用/结果从未进入压缩器视野，与参照基准 Codex（单一历史、单一算法、三触发点）显著偏离。压缩策略因此不一致：运行内的工具细节在下一轮彻底丢失，跨轮摘要只覆盖文本对话。

## What Changes

- **新增统一压缩核心包** `internal/runtime/compaction`：消息无关的 `Entry` 契约 + 单一 `Compact` 算法（90% 阈值、≤20,000 tokens user 消息逐字保留、user 角色 `SUMMARY:` 摘要、滚动摘要、总结超窗丢最早条目重试、20s 超时），供轮间（PreTurn）、轮内（MidTurn）、手动三处调用。
- **删除重复实现**：`coordinator.summarize` / `runner.summarizeContext` / 两套 `retainUserMessages` / 两套截断函数收敛为核心包单一实现。
- **历史统一，纳入 tool 条目**：`messages` 表恢复 `content_type` + `metadata_json` 列（新迁移），条目类型对齐 Codex：`text` / `function_call` / `function_call_output` / `reasoning`（预留）。
- **实时写入**：runner 每产生一条 assistant/tool 消息即写入消息表（对齐 Codex rollout 实时追加）；暂停/恢复通过写入游标保证幂等，不重复入库。
- **子代理写两条**：派发子代理写一条 `function_call`、子代理最终输出写一条 `function_call_output` 进父会话；完整子 transcript 不入库。
- **压缩输入扩展**：跨轮与轮内总结器输入均包含 function_call / function_call_output 条目；单条超长 tool output 送总结前截断。
- **可忽略回声**：`/compact` 与 `Context compacted.` 消息标记 `system_echo`，前端不渲染、总结输入排除、索引排除。
- **索引排除**：tool 条目不进 ES 会话搜索与 context_resource 检索索引。
- **API/前端**：Message DTO 增加 `content_type` / `tool_call_id` / `tool_name`；前端按类型渲染（tool 条目折叠卡），`reasoning` 与 `system_echo` 不渲染。
- **保留不变**：`conversation_compactions` 快照表、claim 锁、指纹去重、snapshot_version 滚动链、First/LastMessageID 锚定 user 消息的语义、20,000/0.90/20s/`SUMMARY:\n` 常量值、token_budget 旁路模式、goal continuation 的 developer 角色。

## Capabilities

### New Capabilities

- `context-compaction`：统一压缩算法与三触发点（轮间/轮内/手动）的行为规范——阈值、保留、摘要注入、滚动、重试、快照持久化。
- `conversation-tool-history`：会话历史的统一条目模型——tool 条目实时写入、子代理条目、条目类型语义、索引排除、API 暴露与前端渲染。

### Modified Capabilities

（无——本项目此前无已归档的 openspec specs。）

## Impact

- **后端**：`internal/runtime/compaction`（新）、`internal/runtime/conversationcontext`、`internal/runtime/agent`（runner/auto_compaction）、`internal/runtime/agentruntime`（assembly/execution）、`internal/application/agent_usecase`（turn_worker/service/subagent）、`internal/domain/conversation`（Message 实体）、`internal/infrastructure/mysql`（message repo）、`internal/interface/http`（Message DTO）。
- **数据库**：新迁移为 `messages` 表加 `content_type`（默认 `text`）+ `metadata_json`；旧行自然共存，不回填。
- **前端**：`web/src/types/api.ts`、`web/src/pages/ChatPage.tsx` 消息渲染。
- **外部依赖**：无新增；不改变 LLM 供应商接口（压缩统一走 `Chat`）。
- **验证限制**：本机无 Go 工具链，`go test ./...` 需在有工具链的环境执行；前端 typecheck/vitest 可本地执行。
