# Tasks: unify-context-compaction

> 全局约束：
> - 常量数值不得改动（20,000 / 0.90 / 20s / `SUMMARY:\n`）；新增常量收敛到 `internal/runtime/compaction` 单一常量组。
> - 快照表 `conversation_compactions`、claim 锁、指纹去重、snapshot_version 链、First/LastMessageID 锚定 user 消息的语义保持不变。
> - token_budget 旁路、developer 角色、旧数据（不回填）行为保持不变。
> - 本机无 Go 工具链：Go 侧 GREEN 命令在**有工具链的环境**执行（交付清单列明）；前端 GREEN（typecheck/vitest）可本地执行。
> - 每个任务独立提交（atomic commit），提交信息遵循仓库现有风格（`feat:`/`test:`/`chore:` 前缀）。

## 任务依赖关系

```
Task 1 (类型化消息实体+迁移)
  ├──► Task 3 (跨轮切换核心) ◄── Task 2 (压缩核心包)
  ├──► Task 5 (实时写入+幂等) ◄── Task 2
  │       └──► Task 6 (子代理两条)
  │       └──► Task 7 (API+前端) ◄── Task 1
  └──► Task 4 (轮内切换核心) ◄── Task 2
Task 8 (回归验证) ◄── 全部
```

- **可并行**：Task 2 与 Task 1 无共享文件，可并行；Task 4 仅依赖 Task 2，可与 Task 3/5 并行。
- **必须串行**：Task 3、Task 5、Task 6 共享 `runner.go`/`turn_worker.go`/消息写入路径，串行执行；Task 3 与 Task 4 都改压缩调用方但文件不重叠，理论可并行，因共享核心包接口演化建议串行（先 3 后 4）。
- **汇合点**：Task 8 依赖全部前置完成。
- **Worktree 分组建议**：单分支串行即可（共享文件多，并行收益低）。

---

- [x] Task 1: 新增消息类型迁移与类型化消息实体

**文件**：
- 新建：`migrations/0000XX_message_content_type.up.sql` / `.down.sql`（序号取当前最大+1）
- 修改：`internal/domain/conversation/message.go`（Message 增加 `ContentType string`、`MetadataJSON json.RawMessage`；新增常量 `ContentTypeText/FunctionCall/FunctionCallOutput/Reasoning/SystemEcho`）
- 修改：`internal/infrastructure/mysql/message_repo.go`（Create/查询透传新列；`enqueueContextResource` 仅对 `ContentType=text` 执行）
- 测试：`internal/infrastructure/mysql/message_repo_test.go`（或就近既有测试文件）

**RED 清单**：
1. `MessageRepoTest#createPersistsContentType`（mock DB 记录写入参数 → 断言 function_call 行的 content_type 与 metadata_json 原样落库）
2. `MessageRepoTest#createDefaultsToText`（入参 ContentType 为空字符串 → 断言写入值为 `text`，不抛错）
3. `MessageRepoTest#listReturnsTypedRows`（mock 返回含 metadata_json 的行 → 断言读出的 Message 字段可解析出 tool_call_id/tool_name）
4. `MessageRepoTest#createSkipsIndexForToolEntries`（mock 索引入口可计数，写入 function_call_output 行 → 断言索引调用 0 次；写入 text 行 → 断言 1 次）
5. `MessageRepoTest#invalidMetadataDegradesToText`（metadata_json 为非法 JSON → 断言读取不抛错、按 text 语义降级处理）
6. `MigrationTest#upAddsColumns`（mock 迁移脚本内容读取返回 up/down SQL → 断言 up 含两条 ADD COLUMN 且 down 对称 DROP、列名/默认值与 design §8 一致）

**GREEN**：`go test ./internal/infrastructure/mysql/... ./internal/domain/conversation/...`（有工具链环境）全部转绿。

**ASSERT**：索引入口（`enqueueContextResource`）对非 text 行 0 次调用、对 text 行 1 次调用；写入 SQL 参数中 content_type 精确匹配枚举值；非法 metadata 路径不产生 error 返回。

**DoD**：RED 6 项全部转绿 + `go build ./internal/...` exit 0 + 迁移 up/down 成对且列定义与 design §8 逐字一致。

---

- [x] Task 2: 创建统一压缩核心包

**文件**：
- 新建：`internal/runtime/compaction/entry.go`（Entry、FromMessages、FromChat、ToChat）
- 新建：`internal/runtime/compaction/compact.go`（Request/Result、Compact、常量组）
- 新建：`internal/runtime/compaction/retain.go`（尾部保留+截断）
- 测试：`internal/runtime/compaction/compact_test.go`、`entry_test.go`

**RED 清单**：
1. `CompactTest#summarizesAllEntryTypes`（mock ChatClient 返回摘要文本，输入 2 text + 3 function_call + 3 function_call_output → 断言客户端收到 1 次调用且输入包含全部 8 条内容）
2. `CompactTest#rollingCarriesParentSummary`（ParentSummary 返回非空 → 断言总结输入首条为带 `SUMMARY:` 前缀的旧摘要）
3. `CompactTest#excludesIgnorableEntries`（mock ChatClient 记录输入，输入含 system_echo 与 reasoning 条目 → 断言总结输入不含其内容）
4. `CompactTest#truncatesOversizedToolOutput`（mock ChatClient 记录输入，单条 function_call_output 超过 PerEntryLimitTokens → 断言该条被截断到上限内，其余条目完整）
5. `CompactTest#emptySummaryFallsBack`（mock 返回空内容 → 断言 Summary == "(no summary available)"）
6. `CompactTest#overflowDropsOldestAndRetries`（mock 第一次抛 ErrContextWindowExceeded、第二次返回摘要 → 断言共调用 2 次且第二次输入不含最早条目）
7. `CompactTest#overflowWithSingleEntryFails`（仅 1 条时 mock 抛 ErrContextWindowExceeded → 断言返回错误，不再重试（调用恰 1 次））
8. `RetainTest#keepsUserMessagesWithinBudget`（mock 条目列表返回尾部 5 条 user 共 9,000 tokens → 断言全部保留且顺序正确）
9. `RetainTest#truncatesFirstOverflowingMessage`（mock 条目列表返回尾部第 3 条超剩余预算 → 断言该条截断保留、更早条目不保留）
10. `RetainTest#skipsSummaryPrefixedMessages`（mock 条目列表返回以 `SUMMARY:` 开头的 user 消息 → 断言不进入保留集合、不消耗预算）
11. `EntryTest#fromChatSplitsAssistantToolCalls`（mock ChatMessage 列表返回 1 条 assistant 带文本+2 ToolCalls → 断言产出 1 text + 2 function_call 条目）
12. `EntryTest#toChatMergesConsecutiveCalls`（mock 条目列表返回 1 text + 2 function_call + 2 function_call_output → 断言合并为 assistant(ToolCalls×2) + 2 tool 消息的合法配对）

**GREEN**：`go test ./internal/runtime/compaction/...`（有工具链环境）全部转绿。

**ASSERT**：对 mock 客户端 verify 调用次数与每次输入的逐条内容；保留结果的 ID/内容/顺序精确断言；ToChat 输出的 ToolCallID 配对精确匹配；常量组数值与全局约束逐字一致。

**DoD**：RED 12 项全部转绿 + `go vet ./internal/runtime/compaction/...` exit 0 + 包内不依赖 `runtime/agent` 与 `runtime/conversationcontext`（防循环，import 清单检查）。

---

- [x] Task 3: 跨轮压缩切换到核心包并纳入工具条目

**文件**：
- 修改：`internal/runtime/conversationcontext/coordinator.go`（compact/summarize/retain 改为调用核心包；删除本地重复实现）
- 修改：`internal/runtime/agentruntime/assembly.go`（Render 走 `compaction.FromMessages`+`ToChat` 回放，替代按行直发）
- 测试：`internal/runtime/conversationcontext/coordinator_test.go` 增改

**RED 清单**：
1. `CoordinatorCompactTest#usesCoreAlgorithm`（mock 核心可计数，触发跨轮压缩 → 断言核心 Compact 恰被调用 1 次，入参条目含 function_call/function_call_output）
2. `CoordinatorCompactTest#retainedAnchorsUnchanged`（mock 核心返回摘要与保留的 3 条 user 消息，压缩后 → 断言快照 First/LastMessageID 仍等于保留 user-text 消息的首/尾 ID）
3. `CoordinatorCompactTest#manualForceCompactsBelowThreshold`（mock 总结器可计数，未达阈值 + Force → 断言产生新快照且总结器被调用 1 次）
4. `CoordinatorLoadTest#renderProducesValidToolPairing`（mock 消息行返回窗口尾段含 function_call+output 行 → 断言渲染结果中 role=tool 消息前存在对应 ToolCalls 的 assistant 消息）
5. `CoordinatorCompactTest#snapshotPersistenceUnchanged`（mock 快照仓库可计数 → 断言 ClaimSnapshot/CompleteSnapshot 各 1 次、fingerprint 字段非空、版本号=parent+1）
6. `CoordinatorCompactTest#tokenBudgetBypassStillWorks`（mock 总结器可计数，配置 token_budget 模式 → 断言总结调用 0 次、仅按角色保留）
7. `CoordinatorPrepareTest#triggersAtThreshold`（mock 计数器返回测量值 116,000、窗口阈值 115,200 → 断言压缩触发、总结器恰调用 1 次、产生新快照）
8. `CoordinatorPrepareTest#belowThresholdNoCompact`（mock 计数器返回测量值 115,199 → 断言总结器调用 0 次、无新快照行）
9. `CoordinatorPrepareTest#singleInputOverHardLimitOverflows`（mock 返回单条输入超过硬上限（窗口-预留）→ 断言返回溢出错误且错误信息含估计 token 数与允许上限，总结器调用 0 次；实现提示：现行单条溢出错误消息不含数值（coordinator.go:192-197），需按 spec 授权把数值补进消息，勿按「保留不变」削弱断言）
10. `CoordinatorCompactTest#reusesClaimedSnapshot`（mock claim 表返回既有快照的持有记录（复用分支位于 compact 的 !claimed 路径，coordinator.go:266-276）→ 断言复用既有快照、不发起新压缩）
11. `CoordinatorCompactTest#duplicateFingerprintNoop`（mock 快照仓库第二次写入返回唯一键冲突（相同指纹）→ 断言第二次写入为无操作、快照行数不增、加载仍取最新版本）

**GREEN**：`go test ./internal/runtime/conversationcontext/... ./internal/runtime/agentruntime/...`（有工具链环境）全部转绿。

**ASSERT**：coordinator 内不再存在本地总结/保留函数（grep 验证 `summarize`/`retainUserMessages` 定义已移除）；快照行字段（trigger_type/prompt_version/前后 tokens）语义与改造前一致；回放消息无孤立 role=tool；阈值三态（达到/未达/溢出）各有独立断言。

**DoD**：RED 11 项全部转绿 + 重复代码清单（handoff 附表中跨轮侧 6 处）全部删除 + `go build ./internal/runtime/...` exit 0。

---

- [x] Task 4: 轮内压缩切换到核心包并删除重复代码

**文件**：
- 修改：`internal/runtime/agent/auto_compaction.go`（compactRuntimeTranscript/summarizeContext/retainMessagesByRole/truncateMessageTokens 改为调用核心包）
- 修改：`internal/runtime/agent/runner.go`（调用点适配）
- 测试：`internal/runtime/agent/runner_test.go` / auto_compaction 相关测试增改

**RED 清单**：
1. `RuntimeCompactTest#midTurnUsesCore`（transcript 达阈值，mock 核心可计数 → 断言核心 Compact 恰 1 次，入参条目覆盖 assistant/tool 消息）
2. `RuntimeCompactTest#transcriptReplacedByRetainedPlusSummary`（mock 返回摘要 → 断言新 transcript = 保留 user 消息 + 1 条 `SUMMARY:` 前缀 user 消息，原 assistant/tool 消息不在其中）
3. `RuntimeCompactTest#summaryFailureKeepsOriginal`（mock 抛非超窗错误 → 断言 transcript 原样保留、运行不中断、失败写入 trace）
4. `RuntimeCompactTest#tokenBudgetPathUnchanged`（mock 总结器可计数，token_budget 模式 → 断言总结调用 0 次、保留行为与改造前一致）
5. `RuntimeCompactTest#persistRuntimeSnapshotUnchanged`（mock 快照仓库可计数 → 断言仍写 trigger_type=runtime 行、prompt_version="runtime-compaction-v1"、锚定 InitialUserMessageID）
6. `RuntimeCompactTest#thresholdUnchanged`（mock RunRequest 返回窗口 128,000 → 断言触发限 = 115,200，精确到整数）

**GREEN**：`go test ./internal/runtime/agent/...`（有工具链环境）全部转绿。

**DoD**：RED 6 项全部转绿 + 重复代码清单（handoff 附表中运行内侧 6 处）全部删除 + `go vet ./internal/runtime/agent/...` exit 0。

**ASSERT**：`auto_compaction.go` 中不再有总结实现与二分截断函数定义；阈值/预算常量引用核心包单一常量组（grep 验证无本地字面量 20_000/0.90）。

---

- [x] Task 5: 实现运行内条目实时写入与恢复幂等

**文件**：
- 修改：`internal/runtime/agent/types.go`（RunRequest 增加 `MessageSink`；Checkpoint 增加 `PersistedMessageCount`；RunResult 输出增加 assistant_message_id）
- 修改：`internal/runtime/agent/runner.go`（assistant/tool 消息产生处调用 sink；更新计数；压缩后游标重置；`hydrateCheckpoint` 与 `checkpointFromMessages` 两处构造器均携带计数）
- 修改：`internal/runtime/agent/resumer.go`（恢复时按计数跳过已持久化条目）
- 修改：`internal/runtime/agentruntime/execution.go`（注入 sink 实现：按 design §3 映射写消息表；`rc.DelegationDepth > 0` 时不挂）
- 修改：`internal/runtime/agentruntime/dependencies.go`（补 sink 注入点；现仅有只读 `MessageHistoryReader` :35-38）或经 application 层注入到 RunRequest
- 修改：`internal/application/agent_usecase/turn_worker.go`（最终答案写入改为引用 assistant_message_id，缺失时兜底创建；兜底创建 `Context compacted.` 行置 system_echo 且跳过 IndexMessage）
- 修改：`internal/application/agent_usecase/service.go`（`/compact` user 消息创建置 system_echo 且跳过 :813 的 IndexMessage；fork 循环 :600-613 复制 ContentType/MetadataJSON 且非 text 行跳过 :612 的 IndexMessage）
- 测试：`internal/runtime/agent/runner_test.go`、`resumer_test.go`、usecase 侧测试增改

**RED 清单**：
1. `RealtimeWriteTest#assistantAndToolEntriesPersistedOnProduction`（mock sink 可计数，一轮含 1 文本+2 调用+2 结果 → 断言 sink 收到 5 次条目写入且顺序一致）
2. `RealtimeWriteTest#sinkFailureDegradesWithoutAbort`（mock sink 抛错 → 断言运行继续、最终答案正常产出、错误事件被记录 1 次）
3. `ResumeIdempotencyTest#resumedTranscriptSkipsPersisted`（mock checkpoint 返回 PersistedMessageCount=5，ResumeTranscript 共 8 条 → 断言恢复后仅后 3 条进入待写入集合，前 5 条 0 次写入）
4. `RealtimeWriteTest#checkpointCarriesPersistedCount`（mock sink 返回成功写入 3 条后触发 hydrateCheckpoint → 断言 checkpoint.PersistedMessageCount==3）
5. `FinalAnswerTest#turnWorkerReusesWrittenMessage`（RunResult 返回 assistant_message_id=42 → 断言 turn_worker 不创建新消息行、AssistantMessageID==42）
6. `FinalAnswerTest#turnWorkerFallsBackWhenMissing`（mock RunResult 返回不含 assistant_message_id → 断言沿用创建逻辑恰 1 次）
7. `RealtimeWriteTest#failedRunKeepsWrittenEntries`（mock sink 返回成功写入 3 条后运行失败 → 断言无删除调用、3 条保留且关联 run_id）
8. `RealtimeWriteTest#cancelKeepsWrittenEntries`（mock sink 返回成功写入 2 条后运行被取消 → 断言无删除调用、2 条保留；取消与失败同路径仅计数语义不同）
9. `ResumeIdempotencyTest#compactionResetsCursor`（mock 核心返回摘要触发运行内压缩，再产生 2 条新条目后暂停 → 断言恢复时保留 user 条目与 SUMMARY 条目 0 次入库、新条目不重不漏）
10. `RealtimeWriteTest#approvalPauseCarriesCount`（mock 审批暂停走 `checkpointFromMessages` 路径 → 断言其携带的 PersistedMessageCount 与已写入数一致，审批恢复后重复写入 0 次）
11. `EchoMarkingTest#manualCompactUserMessageSystemEcho`（mock 写入记录参数，触发 `/compact` 分支 → 断言该行 ContentType=system_echo 且 IndexMessage 调用 0 次）
12. `EchoMarkingTest#compactedFallbackSystemEcho`（mock RunResult 返回 content="Context compacted." 且无 assistant_message_id → 断言兜底创建行 ContentType=system_echo、IndexMessage 调用 0 次）
13. `ForkCopyTest#forkCarriesContentTypeAndSkipsIndex`（mock 源会话返回 1 条 text + 1 条 function_call 行并执行 fork → 断言复制行 content_type/metadata_json 与源行一致、IndexMessage 仅对 text 行调用 1 次）
14. `IndexPairingTest#normalTurnTextStillIndexed`（mock 索引入口可计数，执行一次普通（非 /compact）turn 含 1 条 user text 与 1 条最终答案 → 断言二者各被 IndexMessage 恰 1 次，正向验证「文本条目照常索引」）

**GREEN**：`go test ./internal/runtime/agent/... ./internal/runtime/agentruntime/... ./internal/application/agent_usecase/...`（有工具链环境）全部转绿。

**ASSERT**：sink 调用次数与条目内容逐条精确断言；恢复路径对已持久化条目 0 次写入；压缩-游标交互场景摘要/保留条目 0 次入库；两个 checkpoint 构造器均断言携带计数；turn_worker 两分支互斥（有 ID 时创建调用 0 次，无 ID 时 1 次）；消息行 role/content_type 与 design §3 映射表一致；system_echo 两处的索引调用均为 0 次。

**DoD**：RED 14 项全部转绿 + `go build ./internal/...` exit 0 + design §4.1/§4.2/§4.3 与 §6 打标条款在测试中均有覆盖。

---

- [x] Task 6: 裁决落地：子代理委派对唯一写入方与内部 transcript 抑制

**文件**：
- 修改：`internal/runtime/agentruntime/execution.go`（`DelegationDepth > 0` 不挂 sink，Task 5 已加则本任务仅补子代理场景测试）
- 修改（核验不写入）：`internal/application/agent_usecase/run_control.go`（`completeSubagentRun` :202 不新增任何消息写入）、`internal/application/agent_usecase/subagent.go`（:186 调用处）、`internal/application/agent_usecase/turn_worker.go`（:612-633 分支不新增消息写入）
- 测试：跨层联合场景测试（父 run 委派 → sink 写入 → 子代理完成路径）增改

**RED 清单**：
1. `SubagentWriteTest#delegationPairExactlyTwo`（mock 写入可计数，父 run 委派一次且子代理成功完成 → 断言父会话合计新增恰 2 行：1 function_call + 1 function_call_output，无第 3/4 行（sink 路径与完成路径联合场景，而非孤立测完成路径））
2. `SubagentWriteTest#innerTranscriptSuppressed`（mock DelegationDepth=1 的子代理运行内部 8 轮返回完成 → 断言子代理自身 transcript 写入 0 条、父会话因其内部轮次新增 0 行）
3. `SubagentWriteTest#completionPathWritesNothing`（mock `completeSubagentRun`（run_control.go:202 / subagent.go:186）与 `turn_worker.go:612-633` 分支执行 → 断言该两条完成路径对消息表写入 0 行）
4. `SubagentWriteTest#failureWritesErrorOutput`（mock 子代理运行抛错 → 断言仍恰 2 行，output 行内容为错误信息）
5. `SubagentWriteTest#pairingConsistent`（mock 写入返回已写入两行的 ID → 断言 output 行的 tool_call_id 与 call 行一致、tool_name 相同）
6. `SubagentWriteTest#parentConversationTarget`（mock 写入记录目标会话 → 断言目标为父会话 ID 而非子代理自身会话）

**GREEN**：`go test ./internal/runtime/agentruntime/... ./internal/application/agent_usecase/...`（有工具链环境）全部转绿。

**ASSERT**：写入目标会话 ID、role/content_type/metadata 字段逐条精确断言；委派对唯一写入方 = 父 sink（完成路径写入计数恒 0）；失败分支不抛未处理错误、不影响父运行状态。

**DoD**：RED 6 项全部转绿 + 子代理完成路径不触碰 `agent_run_steps` 既有审计行为（审计调用次数不变）+ grep 核验 `completeSubagentRun` 与 `turn_worker.go:612-633` 分支无消息创建调用。

---

- [x] Task 7: 扩展消息 API 与前端类型化渲染

**文件**：
- 修改：消息序列化处（审查修正：`internal/interface/http/handler/agent_handler.go` 的消息路由；`ListMessages` 实际直接返回 `[]conversation.Message`（service.go:554-558）经实体 json tag 序列化，无独立 DTO 结构——扩展 `internal/domain/conversation/message.go` 的 json tag 暴露 content_type，并在 application 层或 handler 侧解析 metadata_json 出 `tool_call_id`/`tool_name`）
- 修改：`web/src/types/api.ts`（Message 类型扩展）
- 修改：`web/src/pages/ChatPage.tsx`（visibleChatMessages 过滤；工具条目折叠卡组件；排除 system_echo/reasoning）
- 测试：`web/` 内组件测试（vitest）；Go 侧 DTO 测试

**RED 清单**：
1. `MessageDTOTest#exposesContentTypeAndToolFields`（mock DB 返回含 metadata_json 的消息行 → 断言 DTO 输出 content_type/tool_call_id/tool_name 字段值精确）
2. `MessageDTOTest#legacyRowsDefaultText`（mock DB 返回无 metadata 的旧行 → 断言 content_type=="text"、工具字段为空）
3. `ChatPage.test.tsx#rendersToolEntriesAsCollapsedCards`（mock 消息列表返回 1 function_call + 1 function_call_output → 断言渲染 2 个折叠卡且默认折叠、显示工具名）
4. `ChatPage.test.tsx#hidesEchoAndReasoning`（mock 消息列表返回 system_echo 与 reasoning 各 1 条 + 1 条 text → 断言 DOM 仅出现 text 消息）
5. `ChatPage.test.tsx#userAssistantTextUnchanged`（mock 消息列表返回 user/assistant text 各 1 → 断言渲染结果与改造前一致（既有快照/断言不回退））
6. `ChatPage.test.tsx#cardExpandsToContent`（mock 卡片渲染返回参数/结果内容，点击折叠卡 → 断言展开后可见参数/结果文本）

**GREEN**：本地执行 `cd web && npm run typecheck && npx vitest run` 全部转绿；Go 侧 `go test ./internal/interface/http/...`（有工具链环境）转绿。

**ASSERT**：DOM 断言精确到折叠卡数量与文本可见性；typecheck exit 0；既有 ChatPage 测试无回退失败。

**DoD**：RED 6 项全部转绿 + `npm run typecheck` exit 0 + `npx vitest run` exit 0（本地执行并记录输出）。

---

- [x] Task 8: 执行跨路径回归与交付验证

**文件**：
- 修改（如需）：`internal/runtime/conversationcontext/coordinator_test.go`、`internal/runtime/agent/runner_test.go`、`internal/runtime/agentruntime/*_test.go`、`internal/infrastructure/mysql/agent_runtime_integration_test.go` 中因接口变化失败的既有断言
- 新建：`openspec/changes/unify-context-compaction/verification-checklist.md`

**RED 清单**：
1. `RegressionTest#legacyConversationCompacts`（mock 旧会话数据返回仅含 user/assistant text 的行 → 断言压缩正常完成、无类型相关错误、快照行字段齐全）
2. `RegressionTest#mixedHistoryEndToEnd`（mock 混合历史返回 text+tool 条目并达阈值 → 断言压缩后窗口 = 摘要 + 保留 user + 尾段，且回放配对合法）
3. `RegressionTest#goalContinuationDeveloperRetained`（mock 历史返回 developer 角色消息，配置 token_budget 模式 → 断言保留行为与改造前一致）
4. `RegressionTest#memoryAndReflectionBlocksUnaffected`（mock 记忆/反思召回返回内容 → 断言两块的注入位置/角色/可省略性与改造前一致）
5. `RegressionTest#handoffTableRemovalsVerified`（mock 文件系统读取返回旧实现源码，对 12 处重复点逐一 grep → 断言每处在原位置已不存在、核心包中恰存在 1 份）

**GREEN**：`go test ./internal/...`（有工具链环境）全仓库转绿；`cd web && npm run typecheck && npx vitest run` 本地转绿；`openspec validate unify-context-compaction` exit 0。

**ASSERT**：既有测试仅允许因接口签名的机械适配修改，行为断言不得弱化；12 处重复点删除清单逐项核对；verification-checklist.md 列明"需有 Go 工具链环境执行"的完整命令集。

**DoD**：RED 5 项全部转绿 + 全仓 `go build ./...` 与 `go vet ./...` exit 0（有工具链环境）+ `openspec validate` 通过 + checklist 落盘。
