# Tasks — observability-correlation-tracing

> complexity: 🟡 standard | phase: apply

## Wave 1

- [x] Task 1: 定义 correlation context 值对象与 context 读写接口
  - complexity: 🟡
  - files:
    - Create: `internal/pkg/observability/correlation.go`
    - Create: `internal/pkg/observability/correlation_test.go`
  - Interfaces:
    - Consumes: 标准库 `context.Context`。
    - Produces: `Correlation`、`WithCorrelation(context.Context, Correlation) context.Context`、`CorrelationFromContext(context.Context) (Correlation, bool)`、`Correlation.WithRequestID/WithOwnerID/WithConversationID/WithRunID/WithTurnID/WithParentRunID/WithStepIndex/WithToolCallID`。
  - RED:
    - `CorrelationContextTest#shouldRoundTripAllFields`（mock `Correlation` 含全部字段返回 context → 断言 `CorrelationFromContext` 返回每个字段原值且 `ok=true`）。
    - `CorrelationContextTest#shouldReturnEmptyForNilContext`（mock `nil` context 返回 → 断言空 correlation、`ok=false`，不 panic）。
    - `CorrelationContextTest#shouldPreserveParentAndOptionalFields`（mock `ParentRunID=nil`、`StepIndex=0` 返回 context → 断言可选字段保持 nil/0，不补造 ID）。
    - `CorrelationContextTest#shouldDistinguishContextPresenceFromOwnerValidity`（mock `WithOwnerID(..., 0)` 返回 → 断言 `CorrelationFromContext` 的 `ok=true` 仅表示 value 存在，且 `OwnerID > 0` 判定为 false）。
    - `CorrelationContextTest#shouldDeriveWithoutMutatingOriginal`（mock 原 correlation 返回 → 断言 `WithRunID` 派生值只改变 run ID，原对象的 request/owner/conversation 字段不变）。
  - GREEN:
    - `go test ./internal/pkg/observability -run 'TestCorrelationContext' -count=1`（全部测试转绿）。
  - ASSERT:
    - 精确断言 8 个字段的 round-trip 值、`ok` 状态和 nil context 行为。
    - 派生函数不得修改输入值；无效 owner、空 request ID 不得生成隐式值。
  - DoD:
    - `correlation_test.go` 覆盖上述 5 个方法并全部通过；`go test ./internal/pkg/observability` exit 0；`go vet ./internal/pkg/observability` exit 0。
  - 最小验证: `go test ./internal/pkg/observability -count=1`

- [x] Task 2: 接入 HTTP request、认证、access 与 recovery 日志上下文
  - complexity: 🟡
  - files:
    - Modify: `internal/interface/http/middleware/request_id.go`
    - Modify: `internal/interface/http/middleware/auth.go`
    - Modify: `internal/interface/http/middleware/recovery.go`
    - Create: `internal/interface/http/middleware/access_log.go`
    - Create: `internal/interface/http/middleware/request_id_test.go`
    - Create: `internal/interface/http/middleware/access_log_test.go`
    - Create: `internal/interface/http/middleware/recovery_test.go`
    - Modify: `internal/interface/http/router.go`
    - Create: `internal/interface/http/router_observability_test.go`
  - Interfaces:
    - Consumes: Task 1 的 `observability.Correlation` context API 与现有 `Principal`。
    - Produces: request ID 写入标准 context；认证 owner 写入标准 context；access/recovery 使用 context-aware `slog` 属性。
  - RED:
    - `RequestIDMiddlewareTest#shouldReuseIncomingRequestID`（mock `X-Request-ID=rid-123` 返回 → 断言响应头仍为 rid-123、`CorrelationFromContext` 得到同值）。
    - `RequestIDMiddlewareTest#shouldGenerateWhenHeaderMissing`（mock 空 header 返回 → 断言生成非空 request ID、响应头非空且 context 中相同）。
    - `AccessLogMiddlewareTest#shouldRecordStatusAndLatency`（mock 下游 handler 返回 201 且不抛异常 → 断言 logger 收到 `http.access`、phase=http、result=ok、route、status=201、latency_ms>=0 和 request_id）。
    - `RecoveryMiddlewareTest#shouldRecoverPanicWithCorrelation`（mock 下游 handler 抛 panic 返回 → 断言 HTTP 500、记录 `http.error`、phase=http、result=error 且包含 request_id/owner_id，handler 后续 0 次执行）。
    - `AuthMiddlewareTest#shouldLeaveOwnerAbsentOnUnauthenticatedRequest`（mock `authdomain.AccessTokenService.VerifyAccessToken` 返回未认证错误并构造真实 `authusecase.Service` → 断言 owner 不写入 context、scope denial 仍按现有契约返回且 logger 不输出敏感 token）。
    - `RouterObservabilityTest#shouldMountAccessLogAfterRequestIDAndBeforeRecovery`（mock access logger 收到请求、recovery handler 抛 panic → 断言 router 顺序为 RequestID→AccessLog→Recovery，事件含 request_id 且最终状态为 500）。
  - GREEN:
    - `go test ./internal/interface/http/middleware ./internal/interface/http -run 'Test(RequestID|AccessLog|Recovery|Auth|RouterObservability)' -count=1`（全部测试转绿）。
  - ASSERT:
    - request ID 必须同时出现在 Gin context、标准 context 和响应头；输入 ID 不得被改写。
    - access/error 事件必须验证 route/status/latency 与 correlation 属性；panic 路径必须验证 500 响应和下游短路。
    - 未认证路径使用真实 `authusecase.Service` + mock token/repository 依赖，不要求向具体 `*authusecase.Service` 注入 mock；日志断言不得包含 authorization、API key 或完整请求正文。
  - DoD:
    - middleware 与 router observability 测试全部转绿；`go test ./internal/interface/http/middleware ./internal/interface/http` exit 0；现有 auth 测试无回归；`go vet ./internal/interface/http/middleware ./internal/interface/http` exit 0。
  - 最小验证: `go test ./internal/interface/http/middleware -count=1`

- [x] Task 3: 持久化并恢复异步 turn correlation metadata
  - complexity: 🟡
  - files:
    - Modify: `internal/application/agent_usecase/service.go`
    - Modify: `internal/application/agent_usecase/turn_worker.go`
    - Create: `internal/application/agent_usecase/service_test.go`
    - Create: `internal/application/agent_usecase/turn_worker_test.go`
  - Interfaces:
    - Consumes: Task 1 的 correlation API；现有 `StartTurn`、Run/Turn repositories、`RunIdentity`。
    - Produces: Run/Turn `InputJSON` 中 additive `observability` metadata；worker 执行前恢复 correlation context；turn 状态变化输出 `turn.started`/`turn.finished`/`turn.failed`。
  - RED:
    - `StartTurnCorrelationTest#shouldPersistRequestMetadata`（mock turns/runs repository 返回成功 → 断言 Run 与 Turn input JSON 均包含 request_id、owner_id、conversation_id，且原 query/mode 字段不丢失）。
    - `StartTurnCorrelationTest#shouldKeepIdempotentExistingTurnMetadata`（mock `FindByIdempotencyKey` 返回既有 turn → 断言 create 依赖 0 次调用、返回既有对象且不覆盖其 metadata）。
    - `TurnWorkerCorrelationTest#shouldRestoreQueuedTurnContext`（mock run repository 返回带 observability metadata 的 queued run、runtime 返回成功 → 断言 runtime 收到 context 中的 request/run/turn/owner 字段）。
    - `TurnWorkerCorrelationTest#shouldFallbackForLegacyMetadata`（mock run input JSON 无 observability 字段返回 → 断言 runtime 仍被调用、使用持久化 owner/run/turn，request_id 为空且不抛解析错误）。
    - `TurnWorkerCorrelationTest#shouldFallbackForMalformedMetadata`（mock run input JSON 的 observability 为字符串或 version=99 → 断言 runtime 仍被调用、保留 query/mode/manual_compaction，request_id 为空且只记录受限 parse error）。
    - `TurnWorkerCorrelationTest#shouldStopBeforeRuntimeOnRunLoadError`（mock run repository 抛数据库错误返回 → 断言 runtime Execute 0 次调用、turn 进入现有失败路径且日志包含 run/turn 标识）。
    - `TurnLifecycleDiagnosticsTest#shouldLogTurnLifecycleEvents`（mock runtime 成功/失败返回、turn/run repository 更新返回成功 → 断言 `turn.started` 在 runtime 执行前输出并携带 run/turn/owner correlation，`turn.finished` 包含 result=ok 与 latency_ms，失败路径输出 `turn.failed` 与 error_class，且状态更新不重复）。
  - GREEN:
    - `go test ./internal/application/agent_usecase -run 'Test(StartTurnCorrelation|TurnWorkerCorrelation|TurnLifecycleDiagnostics)' -count=1`（全部测试转绿）。
  - ASSERT:
    - Run/Turn metadata 必须 additive，精确断言 query、mode、manual_compaction 等既有字段保留。
    - idempotency 命中时 repositories 的创建方法必须为 0 次调用；worker 旧数据不得因缺字段失败。
    - run load 失败时 runtime 必须 0 次调用，且已有 fail/retry 状态语义不改变。
  - DoD:
    - 七个 RED 方法转绿；`go test ./internal/application/agent_usecase -count=1` exit 0；`go vet ./internal/application/agent_usecase` exit 0；旧 idempotency/worker 测试全部通过。
  - 最小验证: `go test ./internal/application/agent_usecase -count=1`

- [x] Task 4: 输出 runtime、LLM、tool 与 compaction 生命周期诊断事件
  - complexity: 🟡
  - files:
    - Modify: `internal/pkg/logger/logger.go`
    - Modify: `internal/runtime/agent/model_turn.go`
    - Modify: `internal/runtime/agent/runner.go`
    - Modify: `internal/runtime/agent/auto_compaction.go`
    - Modify: `internal/application/agent_usecase/run_publisher.go`
    - Create: `internal/pkg/logger/logger_test.go`
    - Modify: `internal/runtime/agent/model_turn_test.go`
    - Modify: `internal/runtime/agent/runner_test.go`
    - Modify: `internal/application/agent_usecase/run_publisher_test.go`
    - Create: `internal/runtime/agent/auto_compaction_diagnostics_test.go`
  - Interfaces:
    - Consumes: Task 1 的 context correlation；现有 `slog.Logger`、LLM usage、tool step 和 RunEvent emitter。
    - Produces: metadata-only 生命周期事件 `llm.request`、`llm.completed`、`tool.started`、`tool.completed`、`compaction.completed`；不改变 runtime 返回值和 RunEvent DB-first 顺序。
  - Note: `turn.started/turn.finished/turn.failed` 生命周期事件与 `TurnLifecycleDiagnostics` 测试已由 Task 3 在 worker 层产出；Task 4 GREEN 命令包含该测试仅作为回归验证，不再新增。
  - RED:
    - `LoggerEventTest#shouldEmitStableMetadataAttributes`（mock logger handler 返回成功 → 断言事件包含 event/phase/result/latency_ms 与 correlation 字段，不包含 prompt 或 API key）。
    - `ModelTurnDiagnosticsTest#shouldLogSuccessfulLLMUsage`（mock tool client 返回 usage 与 response → 断言 `llm.completed` 包含 provider/model/token 摘要且 response 原值透传）。
    - `ModelTurnDiagnosticsTest#shouldLogLLMFailureAndReturnError`（mock tool client 抛 provider error → 断言输出 `llm.completed` result=error、error_class，且 Execute 返回同一错误）。
    - `RunnerToolDiagnosticsTest#shouldSummarizeToolFailure`（mock tool executor 返回含敏感 output 的错误 → 断言 `tool.completed` 包含 tool_name/tool_call_id/step_index/latency_ms，但不含完整参数或 output）。
    - `RunPublisherDiagnosticsTest#shouldPublishAfterAuditEvenWhenLoggerFails`（mock RunEvent repo 返回成功、logger handler 抛写入错误 → 断言 RunEvent 发布仍发生且顺序为 repo Create → publish，诊断错误不向 Emit 返回）。
    - `CompactionDiagnosticsTest#shouldLogCompactionSummary`（mock compaction client 返回 summary 与 usage → 断言 `compaction.completed` 包含 result、latency_ms、usage 摘要和 conversation/run ID，不包含完整历史正文）。
    - `LoggerPrivacyTest#shouldDropDisallowedAndTruncateOversizedAttributes`（mock handler 返回捕获事件，输入 authorization/prompt/完整 tool output 和超过 16 KiB 错误摘要 → 断言不允许字段不存在且序列化事件不超过 16 KiB）。
    - `LoggerFailureIsolationTest#shouldEmitAtMostOneSinkError`（mock logger sink 连续抛写入错误并重复 Emit/flush、runtime 返回业务成功 → 断言 turn 业务结果不变、sink error 最多记录 1 次且不发生阻塞重试）。
  - GREEN:
    - `go test ./internal/pkg/logger ./internal/runtime/agent ./internal/application/agent_usecase -run 'Test(LoggerEvent|ModelTurnDiagnostics|RunnerToolDiagnostics|RunPublisherDiagnostics|CompactionDiagnostics|LoggerPrivacy|TurnLifecycleDiagnostics)' -count=1`（全部测试转绿）。
  - ASSERT:
    - 断言事件名、低基数 phase/result、latency_ms、usage 摘要和 correlation 字段；禁止 prompt/API key/完整 tool 参数与输出。
    - LLM/tool 错误必须透传原业务错误，同时只让诊断 sink 失败 fail-open。
    - RunEvent 必须先落库后发布；logger 失败不得改变 `Emit` 的业务返回值或发布顺序。
  - DoD:
    - 八个 RED 方法转绿（`TurnLifecycleDiagnostics` 由 Task 3 产出，此处仅作回归）；`go test ./internal/pkg/logger ./internal/runtime/agent ./internal/application/agent_usecase -count=1` exit 0；`go vet` 覆盖三个包 exit 0；RunEvent DB-first 既有测试通过。
  - 最小验证: `go test ./internal/pkg/logger ./internal/runtime/agent ./internal/application/agent_usecase -count=1`

- [x] Task 5: 验证端到端 correlation 关联、隐私和兼容性
  - complexity: 🟡
  - files:
    - Create: `internal/pkg/observability/correlation_integration_test.go`
    - Modify: `internal/interface/http/router_observability_test.go`
    - Modify: `internal/application/agent_usecase/run_publisher_test.go`
    - Modify: `internal/runtime/agentruntime/agent_runtime_test.go`
  - Interfaces:
    - Consumes: Tasks 1–4 的 context、metadata、diagnostic event 和现有 trace API。
    - Produces: 可重复执行的跨层回归验证，不新增生产接口或 durable trace 表。
    - Test harness: 使用 `httptest` Gin chain、内存 Run/Turn/RunEvent/RunStep repositories、真实 `authusecase.Service`/`agentusecase.Service` 配合可注入 fake 依赖、fake runtime executor 和捕获式 `slog.Handler`；不对具体 handler/service 结构体做不可行的 mock。
  - RED:
    - `CorrelationIntegrationTest#shouldLinkHTTPStartTurnWorkerAndRuntime`（mock `httptest` Gin chain、内存 Run/Turn repository、fake runtime executor 依次返回成功，使用真实 JSON metadata codec → 断言 request_id、owner_id、conversation_id、run_id、turn_id 在四层事件中一致）。
    - `CorrelationIntegrationTest#shouldLinkParentRunAndToolStep`（mock 内存 child-run repository/tool executor 返回成功，使用真实 child metadata codec → 断言 parent_run_id、tool_call_id、step_index 在 child run 与 tool event 中一致）。
    - `CorrelationIntegrationTest#shouldKeepTraceAPIShape`（mock RunEvent/RunStep/child repositories 返回记录 → 断言 GetRunTrace 仍返回 run/events/steps/children 四个字段且 repository 查询次数与现有契约一致）。
    - `CorrelationIntegrationTest#shouldRejectSensitiveLogAttributes`（mock logger handler 返回捕获事件 → 断言 prompt、authorization、api_key、完整 tool arguments/output 均不存在）。
    - `CorrelationIntegrationTest#shouldRemainCompatibleWithLegacyRun`（mock legacy Run/Turn 无 request metadata 返回 → 断言请求/worker/runtime 成功完成、X-Request-ID 行为不变且不创建额外 durable record）。
  - GREEN:
    - `go test ./internal/pkg/observability ./internal/interface/http ./internal/application/agent_usecase ./internal/runtime/agentruntime -run 'TestCorrelationIntegration' -count=1`（全部测试转绿）。
  - ASSERT:
    - 精确对账 HTTP、StartTurn、worker、runtime、child tool 的 correlation 字段和值；跨层缺失字段必须按兼容规则为空而非伪造。
    - trace API 字段集合、RunEvent/RunStep repository 调用次数和 DB-first 顺序必须保持既有契约。
    - 通过负向断言确认 prompt、authorization、api_key、完整 tool 参数/输出不出现在事件 payload。
  - DoD:
    - 五个集成测试全部转绿；`go test ./... -count=1` exit 0；`go vet ./...` exit 0；未新增 durable trace migration；现有 HTTP response 和 trace API 测试无回归。
  - 最小验证: `go test ./... -count=1`

## 任务依赖关系

```text
Task 1 correlation value/context
   │
   ├──► Task 2 HTTP middleware context
   │        │
   │        └──► Task 3 StartTurn/worker async restore
   │                         │
   │                         └──► Task 4 runtime/LLM/tool diagnostics
   │                                           │
   └──────────────────────────────────────────┴──► Task 5 end-to-end verification
```

- **可并行 vs 串行**：Tasks 1–5 串行。Task 2 依赖 Task 1 的接口；Task 3 共享 StartTurn/worker 的 metadata 与 Task 1 context；Task 4 共享 runtime/logger 调用点和 Task 3 恢复结果；Task 5 必须等待全部事件契约稳定。不存在安全的并行 task。
- **Git worktree 分组建议**：全部任务使用同一 worktree，避免 context API、middleware、worker 和 logger 的接口漂移；每个 task 独立提交后再进入下一个 task。
- **汇合点**：Task 4 完成后形成完整的 HTTP→worker→runtime 诊断链；Task 5 是唯一汇合验证点。
- **阻塞点**：Task 1 的字段命名和序列化接口若改变，必须先回写 proposal/spec/design/tasks，再继续后续任务；不得在实现阶段静默改名。
