## Context

AgentCanvas 当前在 `internal/pkg/logger/logger.go:8-18` 提供 stdout text/JSON `slog`，在 `internal/interface/http/middleware/request_id.go:11-22` 生成并返回 `X-Request-ID`，但 request ID 只存在 Gin context。`StartTurn` 在 `internal/application/agent_usecase/service.go:726-844` 创建持久化 Run/Turn，`turnWorker.execute` 在 `internal/application/agent_usecase/turn_worker.go:30-154` 可能于另一 goroutine 或进程执行 runtime。RunEvent 已在 `internal/application/agent_usecase/run_publisher.go:64-84` 实现 DB-first，RunStep 已在 `internal/application/agent_usecase/subagent.go:237-257` 持久化；它们必须继续作为审计与回放事实。

Hermes 的可借鉴点是 session tag 自动注入、统一脱敏、turn/API 生命周期 hook 和可选 Langfuse trace；但 AgentCanvas 采用 worker + stdout + durable RunEvent/RunStep，不直接复制 Hermes 的 rotating file 或第二套 trace backend。

## Goals / Non-Goals

**Goals:**

- 建立一个可从标准 `context.Context` 读取和派生的 correlation value。
- 在 HTTP 入口、StartTurn、worker claim、runtime、LLM/tool、compaction、recovery 和 access log 中保持同一 ID 集合。
- 让 request ID 通过 Run/Turn input metadata 跨越异步边界，并兼容旧记录。
- 使用 metadata-only、bounded、fail-open 的结构化诊断事件。
- 保持 RunEvent/RunStep 的 DB-first、回放与现有 API 契约。

**Non-Goals:**

- 不新增 durable trace 表，不替代 RunEvent/RunStep。
- 不在本 change 引入 OTel、Langfuse、外部 exporter 或 rotating file logging。
- 不记录 prompt、API key、完整工具参数/输出或逐 token 的正文日志。
- 不改变公开 HTTP response schema、Run/Turn 状态机或 LLM/tool 接口。

## Decisions

### 1. Correlation value object

新增聚合值包含可选字段：`RequestID string`、`OwnerID int64`、`ConversationID int64`、`RunID int64`、`TurnID int64`、`ParentRunID *int64`、`StepIndex int`、`ToolCallID string`。这些类型与现有 `agent.Run`、`agent.Turn`、`runtime.RunRequest` 及仓储接口的 ID 类型保持一致；仅 HTTP request ID 与 tool call ID 使用字符串。`CorrelationFromContext` 的 `bool` 只表示 context 中是否存在 correlation value，不表示 owner 有效；调用方以 `OwnerID > 0` 判定 owner 是否可用。使用不可变派生函数写入 context；日志属性由单一 helper 展开，避免各层自定义 key。

`request_id.go` 负责生成/读取 request ID；认证成功后将 owner 写入同一上下文。StartTurn 将 request ID 和必要的身份摘要加入 Run/Turn input metadata，worker 读取持久化 metadata 后用 RunIdentity 补齐 run/turn 字段。旧记录缺失 request ID 时保持空值，不生成伪造的持久化关联。

Run/Turn 的 `InputJSON.observability` 使用版本化对象：`{"version":1,"request_id":"..."}`；写入时只合并该命名空间，不覆盖 `query`、`mode`、`manual_compaction` 等既有键。读取时仅接受对象且 `version=1`，缺失、malformed 或未知版本均降级为空 observability metadata，并保留 owner/run/turn 的持久化标识。

### 2. Structured event boundary

事件命名采用稳定的低基数阶段：`http.access`、`http.error`、`turn.started`、`turn.finished`、`turn.failed`、`llm.request`、`llm.completed`、`tool.started`、`tool.completed`、`compaction.completed`。生命周期事件至少含 `event`、`phase`、`result`、`latency_ms` 与 correlation attrs；provider、model、tool name 和 error class 作为受控 metadata。辅助审计诊断 `run_event.audited`（phase=`run_event`）在 RunEvent DB-first 审计写入与发布完成后输出，仅携带 correlation、run/event 标识与 result，不含 `latency_ms`（非计时操作），用于验证日志失败不影响发布顺序。缓存命中/失效事件不属于本 change，由 `conversation-cache` 定义。HTTP access/error 也必须填充 phase=`http` 和 result=`ok|error`。

在现有 `slog` 上提供 context-aware logger/helper，不把每个 model delta 变成日志事件。RunEvent/RunStep 的持久化和 live stream 代码只增加诊断调用点，不改变写入顺序。

### 3. Privacy and failure isolation

默认 formatter/handler 只接收白名单属性：`event`、`phase`、`result`、correlation IDs、`route`、`status`、provider/model/tool name、`error_class`、`latency_ms` 和 usage 摘要；不在白名单中的属性直接丢弃。单条序列化事件上限为 16 KiB，错误摘要超过上限时截断。日志写入错误通过一次受限 fallback（不携带原文）处理，绝不从诊断路径向上返回错误。任何可选 observer 都在边界层 fail-open。

### 4. Async and lifecycle integration

HTTP middleware 建立 request context，并按 `request_id → access log → recovery` 顺序挂载到 Gin router；StartTurn 记录跨进程所需 metadata；worker claim 后恢复上下文；runtime 的 model/tool/compaction 调用从该 context 取值。access log 在 middleware 结束时写 route/status/latency，recovery 使用同一 context 补齐 panic 诊断。Auth 测试保持 `Auth(*authusecase.Service, ...)` 公开签名，通过构造真实 Service 与 mock token/repository 依赖验证认证分支，不伪造一个不可注入的 auth service。

```text
RequestID middleware
        │ request_id
        ▼
Auth / owner context ──► StartTurn ──► Run/Turn input metadata
        │                                 │
        └──────────── standard context ◄──┘
                                          ▼
                              worker claim / RunIdentity
                                          ▼
                         runtime → LLM/tool/compaction
                           │             │
                           ├─────────────┴──► slog metadata events
                           └──► RunEvent / RunStep (durable)
```

### 5. Compatibility and rollout

先添加纯 context/helper 与单元测试，再接入 HTTP 和 worker，之后补 runtime 生命周期事件与 access/recovery 日志，最后做端到端关联测试。所有新字段均为 additive；未认证 owner、旧 Run/Turn metadata 和无日志 sink 场景均走兼容分支。

## Risks / Trade-offs

- **上下文遗漏**：异步边界若忘记恢复会产生断链；通过 StartTurn metadata、worker 专项测试和统一 helper 降低风险。
- **高基数日志**：直接记录原文或动态参数会造成成本和隐私问题；白名单 metadata、事件级摘要和大小上限控制该风险。
- **重复事实**：把 RunEvent/RunStep 再写入 trace backend 会造成不一致；本设计明确只引用其 ID，不复制 durable 事实。
- **日志失败处理**：过度同步写日志会阻塞 worker；handler 采用 fail-open、受限 fallback，并把完整可靠性留给已有 DB 事实。
- **观测覆盖 vs 改动面**：首版不引入外部 SDK，跨服务可视化能力较弱；后续可在稳定 context/event contract 后增加独立 adapter。
