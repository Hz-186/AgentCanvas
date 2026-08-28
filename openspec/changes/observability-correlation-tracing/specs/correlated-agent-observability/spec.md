## Purpose

为 AgentCanvas 的异步 agent 执行建立可检索、可脱敏且不影响业务事实的统一观测契约，使一次 HTTP 请求能够关联到对应的 turn、run、step、模型、工具和压缩阶段。

## ADDED Requirements

### Requirement: Correlation context continuity

系统 MUST 为每次 agent 请求建立可继承的 correlation context，并在可用时携带 `request_id`、`owner_id`、`conversation_id`、`run_id`、`turn_id`、`parent_run_id`、`step_index` 和 `tool_call_id`。同一执行链路产生的结构化日志 MUST 使用相同字段名和 ID 值。

#### Scenario: HTTP request starts a correlated turn

- **GIVEN** 请求带有合法 `X-Request-ID`，且认证得到 owner、agent 和 conversation
- **WHEN** 请求调用 StartTurn 并创建 queued Run/Turn
- **THEN** 后续诊断日志包含 request、owner、conversation、run 和 turn 关联字段
- **AND** 响应继续返回相同的 `X-Request-ID`

#### Scenario: Missing request ID is generated

- **GIVEN** 请求没有 `X-Request-ID`
- **WHEN** 请求经过 request ID middleware
- **THEN** 系统生成一个 request ID 并将它写入标准请求上下文
- **AND** 生成的 ID 写入响应头和后续 turn/run metadata

### Requirement: Asynchronous execution continuity

系统 MUST 在 queued turn 被 worker claim 后恢复可用于日志的 correlation context；worker 不得依赖 HTTP goroutine 的内存上下文才能关联 run。旧的 Run/Turn 记录缺少新增 metadata 时 MUST 仍能执行，并以已有 owner/run/turn 标识降级记录。

#### Scenario: Worker restores a queued turn

- **GIVEN** StartTurn 已持久化 Run/Turn，worker 在另一个 goroutine 或进程中 claim 该 turn
- **WHEN** worker 开始 runtime 执行
- **THEN** worker 日志包含 owner、conversation、run、turn 和 parent run（若存在）
- **AND** 日志不依赖原 HTTP context 是否仍然存在

#### Scenario: Legacy turn has no request metadata

- **GIVEN** 被恢复的旧 turn 的 input metadata 没有 request ID
- **WHEN** worker 执行该 turn
- **THEN** worker 使用已有持久化 ID 继续记录诊断日志
- **AND** 不因缺少 request ID 而失败或伪造新的持久化 request ID

#### Scenario: Malformed observability metadata falls back safely

- **GIVEN** Run/Turn input 中的 `observability` 字段不是对象或版本号不受支持
- **WHEN** worker 解析该 metadata
- **THEN** worker 忽略 malformed 字段并使用已有 owner/run/turn 标识继续执行
- **AND** 系统输出受限的 metadata parse error，不改变 turn 的业务结果

### Requirement: Structured lifecycle diagnostics

系统 MUST 为 HTTP access、turn 状态变化、LLM 请求、tool 调用和 compaction 输出结构化事件；事件至少包含阶段、结果、耗时或错误类别，并在适用时包含 provider/model/tool/step 信息。对话缓存的命中/失效事件由独立 `conversation-cache` change 定义。业务日志 MUST 保持可被现有 text/JSON `slog` handler 消费。

#### Scenario: LLM request completes

- **GIVEN** runtime 发起一次模型请求并收到成功响应
- **WHEN** 模型调用结束
- **THEN** 系统输出包含 run/turn、provider、model、result、latency 和 usage 摘要的结构化事件
- **AND** 事件不改变模型响应或 RunEvent/RunStep 写入顺序

#### Scenario: Tool call fails

- **GIVEN** tool executor 返回错误
- **WHEN** runtime 记录该 tool step
- **THEN** 系统输出包含 tool name、tool call ID、step index、错误类别和耗时的事件
- **AND** 完整工具参数与返回正文不进入默认日志

#### Scenario: HTTP request finishes with error

- **GIVEN** handler 返回应用错误或 recovery 捕获 panic
- **WHEN** HTTP 请求结束
- **THEN** access/error 日志包含 request ID、owner（若已认证）、route、status 和 latency
- **AND** recovery 仍返回既有内部错误响应

#### Scenario: Turn lifecycle changes state

- **GIVEN** queued turn 被 worker 接受并进入 running、finished 或 failed 状态
- **WHEN** 状态发生变化
- **THEN** 系统输出对应的 `turn.started`、`turn.finished` 或 `turn.failed` metadata 事件
- **AND** 事件包含 turn/run ID、result 和 latency 或 error class

### Requirement: Durable trace boundary

系统 MUST 继续以 RunEvent 和 RunStep 作为持久化审计、回放和 trace API 的事实来源；诊断日志 MAY 引用它们的 ID，但 MUST NOT 创建平行 durable trace 表或改变现有 DB-first event ordering。

#### Scenario: Run event remains DB-first

- **GIVEN** runtime 发出一个 RunEvent
- **WHEN** event emitter 处理该事件
- **THEN** RunEvent 先成功写入数据库后才向 live stream 发布
- **AND** 诊断日志失败不会阻止数据库写入或 live stream 发布

#### Scenario: Trace API remains compatible

- **GIVEN** 客户端查询既有 run trace API
- **WHEN** API 返回 run、events、steps 和 children
- **THEN** 返回结构与现有契约兼容
- **AND** 新增日志字段不会要求客户端读取另一套 trace 存储

### Requirement: Privacy-safe and fail-open observation

默认观测输出 MUST 只包含 metadata、ID、状态、耗时、usage 摘要和错误类别；MUST NOT 输出 API key、prompt、完整 tool 参数、完整 tool output 或敏感 header。日志 handler、观察 adapter 或 flush 失败 MUST fail-open，不得改变 turn 的业务成功/失败结果。

默认事件属性白名单 MUST 仅允许 `event`、`phase`、`result`、`request_id`、`owner_id`、`conversation_id`、`run_id`、`turn_id`、`parent_run_id`、`step_index`、`tool_call_id`、`route`、`status`、`provider`、`model`、`tool_name`、`error_class`、`latency_ms`、`usage` 和有限长度的错误摘要；单条序列化事件 MUST 不超过 16 KiB，超出部分 MUST 被截断或丢弃。

#### Scenario: Sensitive values are redacted

- **GIVEN** provider error 或 tool metadata 中包含 secret-like 值
- **WHEN** 系统生成结构化诊断事件
- **THEN** secret、authorization header 和完整内容被省略或脱敏
- **AND** 事件保留可定位问题所需的类型、ID 和错误类别

#### Scenario: Observation sink is unavailable

- **GIVEN** 结构化日志 handler 写入失败或可选 adapter 不可用
- **WHEN** runtime 继续处理 turn
- **THEN** turn 仍按原业务路径完成或失败
- **AND** 系统仅记录一次受限的 sink error，不循环重试阻塞执行线程

#### Scenario: Disallowed attributes are bounded

- **GIVEN** 诊断调用尝试写入 authorization、prompt、完整 tool arguments 或超过 16 KiB 的错误摘要
- **WHEN** 事件进入日志 handler
- **THEN** 不允许的属性被丢弃，超长摘要被截断或丢弃
- **AND** 保留白名单中的 correlation、状态和错误类别字段

### Requirement: Compatibility and bounded overhead

观测能力 MUST 保持现有 `X-Request-ID` 响应头、HTTP response schema、Run/Turn 状态迁移和 LLM/tool API 兼容；新增字段的序列化和日志开销 MUST 是有界的，并不得记录完整消息正文作为默认行为。

#### Scenario: Existing request ID remains compatible

- **GIVEN** 客户端提供 `X-Request-ID`
- **WHEN** 请求成功或失败
- **THEN** 响应头值保持不变
- **AND** 旧客户端无需理解新增日志字段即可正常工作

#### Scenario: High-volume logs stay metadata-only

- **GIVEN** turn 产生大量 model delta 或 tool output
- **WHEN** 诊断日志持续输出
- **THEN** 日志事件按生命周期摘要而不是按每个正文 delta 记录
- **AND** 单条事件大小受限且不会把 prompt/token 流复制到 stdout

## Verification: 验收

- [ ] HTTP、worker、runtime、LLM/tool 和 compaction 日志可通过统一 correlation 字段关联。
- [ ] RunEvent/RunStep 仍是唯一 durable trace，现有 trace API 与 DB-first ordering 测试保持通过。
- [ ] 默认日志不包含 prompt、API key、完整 tool 参数/返回值和敏感 header。
- [ ] 观测 sink 故障不会改变 turn 业务结果或阻塞 worker。
- [ ] `X-Request-ID`、Run/Turn 状态和现有 HTTP response schema 保持兼容。
