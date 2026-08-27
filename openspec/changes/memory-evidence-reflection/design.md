# Design — memory-evidence-reflection

## Context

动机与行为契约见 `proposal.md`；评审证据见 `doc/memory-reflection-evidence-plan-v2.md`。当前 durable 管线：每轮建一行边界任务（`durable_memory_pipeline.go:184,198`）、提取只读 Role+Content 且 120k 截断保留最旧前缀（`:411-421`）、读路径过滤 `archived_at IS NULL`（`message_repo.go:117-139`）、`ResultJSON` 无消费者、`source=extraction` 零生产者（`:540-545`）、无模型时文本倾倒（`:426-428`）。反思侧：首错即返回（`reflection.go:95-106`）、窗口 6 step 无参数（`:112-120`）、终端入队 `_ =` 吞错（`agentruntime/reflection.go:60`）。

## Goals / Non-Goals

**Goals:** 证据完整（归档可见、工具参数/错误状态可读）；会话级 debounce；超长对话全量覆盖；候选门禁与统一写接线；反思证据化与可观测。

**Non-Goals:** 零迁移、零新依赖；不改 `llm.ChatMessage`；不启用旧文档型 Phase 2；不做供应商额度查询；不持久化思维链；不清理无关死代码。

## Decisions

### 1. 错误状态走 `metadata_json`，不做 DDL（接缝已按代码事实修正）

**当前真实链路**（设计审查 round 1 Must Fix 1 修正）：runner 持久化的是 `[]llm.ChatMessage` 转录，经 `persistTranscriptEntries` → `compaction.FromChatAt` 进入 MessageSink（`runner.go:847-870`）；ChatMessage 仅含 Role/Content/ToolCallID/ToolCalls，**不存在 RunStep→Entry 的转换**。非目标禁止改 `llm.ChatMessage`。

**选定接缝 = runner 侧 entry 富化**：`FromChatAt` 转换后，runner 按 `ToolCallID` 在 `result.Steps` 中查表，为 `function_call_output` entry 填充 `compaction.Entry.IsError/ErrorCode`（Entry 新增字段）；sink 照常把两字段写入 `metadata_json`。该方案依赖 Decision 11 的 `RunStep.ErrorCode`（同批改动，归入 Task 1）。

**回放确定性**（必须满足）：`verifyTranscriptPayload`（`message_repo.go:201-221`）对同 `transcript_entry_id` 的重试行做 `MetadataJSON` 字节比对。步骤在 checkpoint-resume 时由 `hydrateCheckpoint` 恢复，重放查表结果与首次一致；**查表未命中（步骤缺失）时不添加错误键**（行与旧格式字节一致），下游按 `unknown` 处理。禁止任何"成功时省略键"之类依赖上下文的条件写入。

已验证 `ToChat` 只转发 Role/Content/ToolCallID/ToolCalls（`entry.go:111-136`），新键不会泄漏到模型 API。旧行缺键 = `unknown`，禁止文本猜测。

### 2. 归档感知读路径仅限耐久提取

`MessageRepository` 新增 `ListThroughIncludingArchived(ownerID, conversationID, afterID, throughID)`：与 `ListActiveAfterThrough` 同语义但不过滤 `archived_at`，按 id 升序。仅耐久提取窗口使用；活跃读路径（上下文构建、边界计算）保持 `archived_at IS NULL` 语义不变，避免影响压缩窗口。

### 3. debounce 用条件 UPDATE + 唯一键兜底，不加列

`ScheduleDurableBoundary` 单事务：`SELECT ... FOR UPDATE` 取会话最近一条 `trigger_reason='durable'` 任务（含旧幂等键格式的历史行，天然兼容）→ 分支：
- pending：`UPDATE ... SET through_message_id=?, due_at=? WHERE id=? AND status='pending'`；0 行（竞态被 claim）→ 转 successor 分支；
- running：创建 successor，幂等键 `durable:<owner>:<conv>:after-job:<running-id>`；
- 终态/不存在：新行，键 `after-job:<last-id>` / `initial`；
- 唯一键 `(owner_id, idempotency_key)` 冲突 → 重读返回既有行。
generation 语义编码在幂等键内，无需 `generation` 列。刷新不发布队列事件（旧事件的 `AvailableAt` 提前唤醒只会被 `ListPending` 的 `due_at<=now` 过滤掉，无害）。

**竞态语义（设计审查 round 1 Must Fix 修正）**：事务以 `SELECT ... FOR UPDATE` 开始时，条件 UPDATE 对锁读所见为 pending 的行不可能影响 0 行——真实竞态表现为**锁读观察到 `running`**（worker 已先提交 claim）→ 走 successor 分支。"条件 UPDATE 影响 0 行"分支仅作为无 FOR UPDATE 实现（内存测试 fake）的防御性兜底保留，两条路径都必须有测试。

### 4. 调度白名单显式化

`final_answer`（现状）+ `max_iterations_exceeded` + `max_tool_calls_exceeded` + `timeout`。理由：这四类是"干到预算/时间耗尽"的 run，失败循环教训价值最高；触发点无法廉价判断"有效工具证据"，故不用存在性查询。其余状态（人为暂停、基础设施失败）不提取。

### 5. 窗口起点定向查询

`previousBoundary` 的 200 行 `ListByStatus` 扫描替换为"该会话最近 completed durable 任务"定向查询（复用 Decision 3 的会话查询，过滤 status=completed），走现有 `idx_conversation_id`。**"最近" = MAX(id)**；窗口起点 = 该任务 `through_message_id`。乱序影子语义保留：若最近 completed 的 through ≥ 当前 through → 空窗口（与现 `previousBoundary` 意图一致）。

**与既有 `LatestCompletedThrough` 的关系（设计审查 round 1 Should Improve 澄清）**：`extraction_repo.go:151-160` 已有 `LatestCompletedThrough`（`MAX(through_message_id) WHERE id < beforeJobID`），语义为"某任务之前的最大 through"，与本需求的"会话最近 completed 行"不同，且当前零调用方。本 change **不复用、不修改**它；新方法按会话定向查询，避免 MAX(through) 在任务乱序完成时的歧义。

### 6. 分块参数

单块上限沿用 120000 字节；只在完整证据单元边界切块；单个超大工具输出切成 `part_index/part_count` 连续片段（片段粒度 ≤ 上限）；相邻块重叠 2 个单元。块数 = N 时每块独立提取，随后一次归并（同一提取模型）。

### 7. 候选 schema 与门禁阈值

候选字段：`title/content/type/confidence/importance/evidence_refs`（消息 id 范围或 tool_call_id 列表）。门禁：非空、`confidence>=0.7`（与反思 `MinConfidence` 一致）、`importance>=0.5`、必带证据、数值有限且 ∈[0,1]、脱敏后仍有内容。全部落空 → `result_json.outcome="no_output"`，status 仍 `completed`，不新增状态枚举。

### 8. 增量 result_json 实现崩溃安全重试

每块候选提取完成即写入 `result_json`（`chunks: {index: candidates}` + `merge` 槽位）；重试时按已完成 index 跳过。`result_json` 为 json 列，零迁移。合并失败回退 `pending` 时保留各块候选，不重跑提取。

apply 期补充（2026-08-28，Task 5 双审裁定）：`outcome` 值集为 `{extracted, no_output}`（`extracted`=提取完成；`no_output` 见 Decision 7）。result 同时记录其分块所依据的窗口 `window_after`/`window_through`；resume 时若当前窗口与记录不符（边界在两次尝试间移动），作废部分块从头提取——保证"已完成 index == 同一内容"的跳过不变式。旧格式（无 `chunks`）负载仍不可变、终态。

### 9. 写接线复用 MemoryWritePipeline，去重键按 source 分策略

worker 侧把 `MemoryWritePipeline`（或 jobs 仓储）注入 `DurableMemoryWorker`。候选 → `WriteJobRequest{Source:"extraction", IdempotencyKey:"extraction:<job-id>:<index>"}`。`SQLMemoryWriter` 对 `source=extraction` 计算 `DeduplicationKey=hex(sha256(type+"\n"+normalize(content)))`（normalize = trim + 折叠空白）；其他 source 保持"默认 = 任务幂等键"。64 字符 hex 适配 `varchar(191)` 唯一索引。

### 10. 无模型即失败（倾倒点全集已核实，round 2 Must Fix 修正）

`summarizeDurableText` 调用点全集（含定义共 5 处，已逐一核实）：

| 站点 | 性质 | 处置 |
|---|---|---|
| `durable_memory_pipeline.go:427` | extract 无模型文本倾倒 | **删除**：返回错误 |
| `durable_memory_pipeline.go:600` | Consolidate 无模型回退 | **删除**：返回错误 |
| `durable_memory_pipeline.go:617` | Consolidate 空摘要兜底 | **删除**：返回错误 |
| `consolidation_projection.go:157` | projection 对**已归并的 handbook** 文本做空摘要兜底 | **保留**：输入是 LLM 归并产物而非原始对话，不属证据倾倒；随 `:617` 删除，其在提取链路中仅剩归并产物兜底语义 |
| `:737` 定义 | — | 保留（仍有 :157 一个调用方） |

**退避通道（round 2 Should Improve 澄清，两通道不得混淆）**：`extract` 失败 → 现有**线性**退避（`(AttemptCount+1)` 分钟，`durable_memory_pipeline.go:290`）+ 5 次尝试上限（`extraction_repo.go:49-54`）；`Consolidate` 失败 → 现有 **phase2 指数**退避通道（`durablePhase2RetryDelay`，attempts 上限 8，无硬失败，`:346-354,682-694`）。两通道均为既有行为，本决策只改"无模型/空输出时返回什么"。配置校验已在 `config.go:568` 要求启用时四字段齐全，本决策是运行期双保险。

DoD grep 期望（修正后）：`grep -rn "summarizeDurableText" internal/` 恰剩 2 行——`consolidation_projection.go:157` 调用与 `:737` 定义。

### 11. 反思指纹语义（含 error_code 载体修正）

**载体（设计审查 round 1 Must Fix 2 修正）**：`RunStep` 当前**没有** ErrorCode 字段（`types.go:162-180`）；`error_code` 仅存在于 `ToolResult.Metadata["error_code"]`（`tool_batch_executor.go:45,59`：call-issue 路径写入 issue code、ctx 取消写入 `cancelled`），并在 runner 构建 tool_result step 时被丢弃。因此 Task 1 必须为 `RunStep` 增加 `ErrorCode` 并在 runner 批结果循环（`runner.go:769-780` 站点）从结果元数据填充——reflection（Task 8）与转录富化（Task 1）共同消费它。

扫描全部 `StepTypeToolResult && IsError` step：分类优先级确定为 schema/argument（结构化 error_code 优先）> not-found > denied > 普通错误，单分支判定避免现有两个 `if` 相互覆盖。指纹 = `tool_name|normalizeArgs|error_code`；同指纹 ≥2 次失败且其间无同指纹成功 → `repeated_failure/no_progress`。信号选择：重复指纹最新出现点优先，否则最后一个错误点。提示词 = 信号点附近 ≤12 step（居中截取），参数截断 1200 字符与 content 一致。

### 12. 可观测性复用既有模式

入队失败 → 注入式 `slog` warn（run_id/agent_id 字段）+ 复用 `finalizeCitations` 的 `StepTypeError` 载荷模式（`execution.go:481-485`）尽力发事件。不新增事件类型；`emitRuntimeEvent` 自身吞错特性不改，日志为可靠通道。

## Risks / Mitigations

| 风险 | 缓解 |
|---|---|
| 反思提示词变大（参数注入）影响在线路径 | 12 step 上限 + 1200 字符截断 + 单测锁定窗口大小 |
| 多块提取成本（N+1 次模型调用） | debounce 已降低任务数；`enabled=false` 默认关闭，先在开发环境观察 |
| 写接线首次激活 `source=extraction`，Phase 2 归并输入构成变化 | Task 7 专项测试 `gatherConsolidationInputs` 构成；归并仍受全局锁与版本化 artifacts 保护 |
| 归档感知读路径被误用到活跃路径 | 方法名显式 `IncludingArchived`；仅 pipeline 内调用；测试断言活跃方法行为不变 |
| Windows 无法编译 mysql 包，且 `toolruntime/filesystem_path.go:100,106` 的 `syscall.Flock`（commit 7e624eb 引入，无构建标签）使其上游 `agent`/`agentruntime` 在 Windows 同样无法编译 | 验证门禁 = 非 toolruntime 依赖链包的原生单测（如 `compaction`、`memory_usecase`）+ 依赖链包测试用 `go test -overlay` 将该文件映射为 Windows 可编译等价副本（仅测试期，不进装运代码；`acquirePathLock` 路径不被这些包测试触达）+ `GOOS=linux go build ./...` 全量编译门禁；集成测试需 `AGENTCANVAS_TEST_MYSQL_DSN`。Windows flock 兼容层不在本 change 范围，如需则另立 change |
| 旧 pending 边界行（旧幂等键）残留 | 调度按会话取最近任务，不依赖键格式；残留行到期后按旧逻辑完成（空窗口或正常提取），不迁移数据 |

## Test Strategy

- 单测：memory_usecase fakes（延续现有手写 fake 风格）、agent、agentruntime、compaction。
- Windows 环境事实（2026-08-28 apply 期 Reverse Sync 回写）：`internal/runtime/toolruntime/filesystem_path.go:100,106` 直接使用 `syscall.Flock/LOCK_EX/LOCK_UN`（commit 7e624eb 引入，无构建标签），因此依赖它的 `agent`/`agentruntime` 包在 Windows 上无法原生 `go test`（与 mysql 包同类）。这些包的测试改用 `go test -overlay`，将 `filesystem_path.go` 映射为位于 `%TEMP%` 的 Windows 可编译等价副本（仅两处 Flock 替换为进程内等价实现；`acquirePathLock` 路径不被这些包测试触达）；不修改任何装运代码。`compaction` 与 `memory_usecase` 包仍原生执行。
- 编译门禁：`GOOS=linux go build ./...`，无 overlay（工具链 `D:\Users\hongze01.zhang\sdk\go1.26.6\bin\go.exe`）。
- MySQL 集成：`AGENTCANVAS_TEST_MYSQL_DSN` 提供时覆盖 `ScheduleDurableBoundary` 竞态与归档读路径；否则 skip。
- 每任务 DoD 含 `grep` 断言（旧 Redis 键、文本倾倒路径移除）。
