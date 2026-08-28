# Proposal — memory-evidence-reflection

## Why

对 Codex 源码（`D:\codex-src`）与 AgentCanvas 当前实现的三方审计（详见 `doc/memory-reflection-evidence-plan-v2.md`）确认了四个真实缺口：

1. 耐久提取只读消息的 `Role + Content`（`durable_memory_pipeline.go:411-421`），看不到已持久化的工具参数；120000 字节上限保留**最旧前缀**、静默丢弃最新消息；且读取路径过滤 `archived_at IS NULL`，被上下文压缩归档的历史消息对提取器不可见。
2. 每个成功 root run 各建一行边界任务（`due_at = now+6h`，幂等键含 `through_message_id`），不刷新旧任务，活跃会话会积累大量最终空跑的 pending 行。
3. `function_call_output` 落库不含 `is_error/error_code`，失败与成功输出无法区分；提取结果（`ResultJSON`）没有任何下游消费者，`source=extraction` 记忆行零生产者（管线自述缺口，`durable_memory_pipeline.go:540-545`）。
4. 在线反思只取第一个错误、提示词不含调用参数；终端反思入队失败被 `_ =` 静默吞掉。

Codex 的对照机制（6h 空闲资格线扫描、`FunctionCallOutput.success` 结构化标记、提取前后双向脱敏、保头尾截断、no_output 正常完成）验证了本方案的方向。

## What Changes

- 新增工具错误状态的耐久化：`compaction.Entry` 携带 `IsError/ErrorCode`，`function_call_output` 行的 `metadata_json` 写入 `is_error/error_code`；旧行按 `unknown` 处理。不新增迁移、不触碰 `llm.ChatMessage`。
- 新增包含归档消息的窗口读路径，仅供耐久提取使用；现有活跃读路径不变。
- 新增内部 evidence renderer：把窗口消息渲染为文本单元与按 `tool_call_id` 配对的工具交换单元，统计同参失败次数与恢复状态，排除 `reasoning/system_echo/developer`，送入模型前统一脱敏。
- 将逐边界建任务改为**会话级 debounce**：`ScheduleDurableBoundary` 原子方法——刷新既有 pending 行的 `through_message_id/due_at`；running 时创建唯一 successor；终态后以 `after-job:<id>` 幂等键创建下一行。删除 Redis `durable:pending:*` 合并键；仅新建行时发布队列唤醒。
- 调度 stop-reason 白名单扩展为 `final_answer / max_iterations_exceeded / max_tool_calls_exceeded / timeout`；其余状态与 sub-agent 不调度。
- 用定向查询"会话最近 completed durable 任务"替换 `previousBoundary` 的 200 行扫描。
- 提取改为证据分块（单元边界切块、超大输出 `part_index/part_count` 切片、相邻块 2 单元重叠），Phase 1 输出结构化候选（`title/content/type/confidence/importance/evidence_refs`），每块候选增量写入 `result_json`（重试跳过已完成块），多块后同一模型轻量归并。
- 新增确定性质量门禁（非空、`confidence>=0.7`、`importance>=0.5`、必带证据、数值有限且 ∈[0,1]、脱敏后仍有内容）；空结果记 `outcome="no_output"` 正常完成。
- 打通提取→写记忆接线：过门禁候选经 `memory_write_jobs`（`source=extraction`，幂等键 `extraction:<job-id>:<index>`）写入；`DeduplicationKey = sha256(type+normalized content)` 实现跨任务去重（仅限 extraction 来源）。
- 删除无模型文本倾倒回退：未配置模型时任务失败并走现有退避，不再把对话原文当记忆。
- 反思升级：扫描本次 run 全部工具轨迹，结构化分类（含从未赋值的 `SignalSchemaFailure`），`tool_name+normalized args+error_code` 指纹，同指纹 ≥2 次失败产生 `repeated_failure/no_progress`；提示词窗口扩为信号附近 ≤12 个 step 且含参数/错误码/恢复结果。
- 终端反思持久化内容补入 `RootCause/Applicability`；入队失败改为 `slog` warn + 复用 `finalizeCitations` 的 `StepTypeError` 载荷事件，主 run 不受影响。

## Capabilities

### New Capabilities

- `durable-memory-evidence-pipeline`: 耐久证据持久化、归档感知窗口、会话级 debounce 调度、分块提取、结构化候选、质量门禁与写接线。
- `runtime-reflection-evidence`: 全轨迹反思信号、证据化提示词窗口、终端反思结构与入队失败可观测性。

### Modified Capabilities

无。仓库根 `openspec/specs/` 目前无可复用 capability 文件；相关既有行为契约由 `sql-memory-es-hybrid` change 承载，本 change 仅在其上新增上述两个 capability，不修改其契约。

## Impact

- Go application：`internal/application/memory_usecase`（renderer、chunker、门禁、写接线、调度、提取）。
- Go domain：`internal/domain/memory`（仓储接口新增方法）、`internal/domain/conversation`（窗口读接口）。
- Go infrastructure：`internal/infrastructure/mysql`（`extraction_repo.go`、`message_repo.go` 新增方法，零 DDL）。
- Go runtime：`internal/runtime/compaction`（Entry 字段）、`internal/runtime/agentruntime`（message_sink、终端反思、触发白名单 `checkExtractionTrigger` @ assembly.go）、`internal/runtime/agent`（reflection 信号与提示词、RunStep/runner 错误码载体）。
- Worker：`cmd/worker/main.go` 依赖接线（写管线注入提取路径）。
- 数据库：**零迁移**（复用 `metadata_json`、`result_json`、现有唯一键与 source 枚举）。
- 兼容性：`durable_memory.enabled` 默认 `false`，全部新行为默认关闭；旧消息无 `is_error` 时按 `unknown` 处理。
