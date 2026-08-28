# Task 5 Brief — 验证端到端 correlation 关联、隐私和兼容性

## 目标与范围

这是本 change 的最后一个任务，也是唯一的端到端汇合验证点。Tasks 1–4 已全部完成并通过双审（事件契约已稳定，见下方"契约快照"）。本任务是**纯测试任务**：

- 只新增/修改测试文件，**禁止修改任何生产代码**。
- 不新增生产接口、不新增 durable trace 表、不新增 migration。
- 如果集成测试暴露 Tasks 1–4 的真实缺陷，**停下来**，把缺陷证据写入报告并标红，不要自行修改生产代码——由主会话决定走 fix 流程。

## tasks.md 原文（Task 5）

```text
- [ ] Task 5: 验证端到端 correlation 关联、隐私和兼容性
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
  - RED:（五个，见下）
  - GREEN: `go test ./internal/pkg/observability ./internal/interface/http ./internal/application/agent_usecase ./internal/runtime/agentruntime -run 'TestCorrelationIntegration' -count=1`（全部测试转绿）
  - DoD: 五个集成测试全部转绿；`go test ./... -count=1` exit 0；`go vet ./...` exit 0；未新增 durable trace migration；现有 HTTP response 和 trace API 测试无回归。
```

注意文件清单是 **Create 一个 + Modify 三个**。`CorrelationIntegrationTest` 的五个方法主体放在新文件 `internal/pkg/observability/correlation_integration_test.go`；三个 Modify 文件只允许添加该测试所需的共享辅助（如复用/扩展已有的捕获式 handler、内存 repo、fake executor）。如果某个 RED 测试完全可以在新文件中用已有导出接口实现，不强制改动对应文件——但四个文件都必须至少保持编译通过，且已有测试无回归。若确有一个文件无需改动，在报告中说明理由。

## 五个 RED 测试（方法名必须精确一致）

测试类型名 `CorrelationIntegrationTest`（Go 中为 `type CorrelationIntegrationTest struct{...}` + `func TestCorrelationIntegration(t *testing.T)` 驱动子测试，或直接五个 `func (s *CorrelationIntegrationTest) ...` 风格——与既有测试文件风格保持一致即可，但**方法名/测试名必须包含**以下标识）：

1. `shouldLinkHTTPStartTurnWorkerAndRuntime` — httptest Gin chain + 内存 Run/Turn repository + fake runtime executor（依次返回成功），使用真实 JSON metadata codec；断言 `request_id`、`owner_id`、`conversation_id`、`run_id`、`turn_id` 在四层事件（http.access/turn.started/tool 或 llm 诊断/runtime 事件）中一致。
2. `shouldLinkParentRunAndToolStep` — 内存 child-run repository + tool executor 返回成功，使用真实 child metadata codec；断言 `parent_run_id`、`tool_call_id`、`step_index` 在 child run 与 tool event 中一致。
3. `shouldKeepTraceAPIShape` — 内存 RunEvent/RunStep/child repositories 返回记录；断言 `GetRunTrace` 仍返回 `run`/`events`/`steps`/`children` 四个字段，且 repository 查询次数与现有契约一致（GetRun ×1、ListRunEvents ×1、ListRunSteps ×1、ListChildRuns ×1）。
4. `shouldRejectSensitiveLogAttributes` — 捕获式 slog handler 收齐全部诊断事件；负向断言：任何事件的 attrs 中不出现 prompt、authorization、api_key、完整 tool arguments/output（可扫描 key 名与 value 字符串）。
5. `shouldRemainCompatibleWithLegacyRun` — legacy Run/Turn 无 request metadata（InputJSON 无 `observability` 键）；断言请求/worker/runtime 成功完成、X-Request-ID 行为不变、不创建额外 durable record。

## TDD 硬门槛

先写 RED → 运行、捕获失败输出（失败原因必须是断言/缺失能力，不是编译错误；引用真实契约的测试若直接编译失败说明你引用错了既有接口，先核对）→ 再写最小辅助使其转绿 → 最后全量回归。RED/GREEN 证据（命令 + 关键输出）必须写进报告。

## 契约快照（已验证的 seam，含行号）

### Correlation API（Task 1，internal/pkg/observability/correlation.go）
- `Correlation{RequestID string; OwnerID, ConversationID, RunID, TurnID int64; ParentRunID *int64; StepIndex int; ToolCallID string}`（correlation.go:8-17）。
- `WithCorrelation(ctx, c)`、`CorrelationFromContext(ctx) (Correlation, bool)`（bool = 仅表示存在性）。`With*` 均为不可变派生，`WithParentRunID` 做防御性拷贝。

### HTTP 中间件（Task 2）
- 中间件顺序（router.go:45-48）：`RequestID()` → `AccessLog(logger)` → `Recovery(logger)` → `CORS(...)`；protected 组再挂 `Auth(...)` + `RequireRouteScope()`（router.go:72-73）。
- `request_id.go` 与 `auth.go` 向 ctx 写 correlation（各自 +3 行改动）；`recovery.go`/`access_log.go` 发 `http.error`/`http.access`。
- 现有 `router_observability_test.go`：`TestRouterObservabilityMiddlewareOrderAndPanicEvent` 已示范如何构造最小 `RouterDeps`（只填 Logger + HealthHandler，gin.TestMode，handler 后挂）、如何断言 `http.error`/`http.access` attrs（route/status/request_id），并提供 `routerCaptureHandler`（捕获式 slog.Handler）与 `routerRecordAttrs` 辅助——复用它们，不要重复造。

### StartTurn / worker metadata codec（Task 3）
- `service.go:469` 注释起：additive observability namespace；`inputObservabilityMetadata(ctx, ownerID, conversationID)` 构造 `{"version":1,"request_id","owner_id","conversation_id"}`；`service.go:875` 处并入 InputJSON（Run 与 Turn 同构合并，幂等路径不重复创建）。
- `service.go:490-495` 从 `input["observability"]` 反序列化（缺失/畸形均有兼容回退）。
- `service.go:515` `logTurnLifecycle(ctx, event, result, level, turn, run, latencyMS, errorClass)` 发 `turn.started/turn.finished/turn.failed`；`service.go:517` X-Request-ID 关联逻辑。
- `turn_worker.go:83` 畸形 metadata → 一条有界的 `turn.metadata_parse_error`；`turn_worker.go:85` worker 从持久化 metadata + durable 记录恢复 correlation 注入 ctx，再进 runtime；legacy 静默回退。

### Runtime 诊断（Task 4）
- `internal/pkg/logger/logger.go:46-59` `DiagnosticsHandler`（白名单过滤 + ctx correlation enrichment + 16 KiB 截断 + 至多一条有界 sink 错误 + fail-open）；`logger.New(env)` 签名不变；`logger.ErrorClass(err)` 基于 `%T` 而非错误文本。
- `internal/runtime/agent/runner.go:40-42` `Runner.Logger *slog.Logger` 公开 seam（nil → slog.Default()）；`runner.go:49-52` `diagnosticsLogger()`。
- `model_turn.go:198-202` `llm.request`（provider 取自 `cfg.ProviderType`，model 取自 `req.Model`）；一对 `llm.request`/`llm.completed` 覆盖所有路径。
- `tool.started`/`tool.completed`：`runner.go` 批执行回调包围；软失败（IsError=true, err=nil）→ result=error + 结构化 error_code；Go error → `logger.ErrorClass` 类型分类。
- `auto_compaction.go` `compactRuntimeTranscript` 每条退出路径一条 `compaction.completed`（仅 token usage + conversation/run ID）。
- `run_publisher.go:99-104` `run_event.audited`（phase=`run_event`）：审计写入与发布之后的 metadata-only 诊断；**该事件已回写 design.md Decision 2**（辅助审计诊断，不含 latency_ms）。
- 属性白名单：event, phase, result, request_id, owner_id, conversation_id, run_id, turn_id, parent_run_id, step_index, tool_call_id, route, status, provider, model, tool_name, error_class, latency_ms, usage + 有界 error summary。**敏感负向断言以这份白名单为基准。**

### Trace API 契约（shouldKeepTraceAPIShape）
- `handler/agent_handler.go:673-703` `GetRunTrace`：依次调用 `service.GetRun`、`service.ListRunEvents(ctx, ownerID, runID, 0)`、`service.ListRunSteps`、`service.ListChildRuns`，返回 `gin.H{"run": publicRun, "events": events, "steps": steps, "children": publicChildren}`。断言四字段存在 + 每个 repository/service 恰好一次调用（用计数 fake）。

### 既有测试基建（复用优先）
- `agent_usecase` fake 模式：`worker_test.go` / `service_settings_test.go` 中的 `settingsAgentRuntime`、`settingsRunRepo`、`settingsTurnRepo`、`workerTurnRepo`、`workerRunRepo`（注意：`FindByID` 对缺失记录返回 `(nil, nil)`）。
- `runtime/agentruntime/agent_runtime_test.go:80-102`：`configuredMemoryRepository`、`classifiedTestTool`（带 `toolruntime.ToolMetadata`）、`unclassifiedTestTool`、`capturedRuntimeEvents`。
- `run_publisher_test.go`：Task 4 已有 `TestRunPublisherDiagnosticsPublishesAfterAuditEvenWhenLoggerFails`（run_publisher_test.go:324）与 failing handler 模式（:318）——shouldKeepTraceAPIShape 与本文件相关的部分应与其风格一致。

## 环境验证方案（Windows 主机，必读）

**工具链**：`D:\Users\hongze01.zhang\AppData\Local\Temp\agentcanvas-go122full\go\bin\go.exe`（Go 1.22.12，不在 PATH 上）。仓库为 CRLF，`gofmt -l` 不可作为门槛。

**原生 Windows 阻塞**（均为 base commit 既有，非本 change 引入）：`internal/application/workspace_usecase/cleanup.go:144`（syscall.Kill）、`workspace_usecase/git.go:144-147`（syscall.Flock）、`internal/runtime/toolruntime/filesystem_path.go:100-106`（syscall.Flock）导致 `agent_usecase`、`runtime/agent`（及传递依赖包）测试二进制无法在 Windows 原生编译。

**Overlay 垫片**（既有方案，位于仓库外的临时目录；若之前的垫片已丢失，按同一方式重建）：复制上述三个文件为 stub（把 syscall 调用替换为返回 nil 的等价实现），写 `overlay.json` 映射，然后：

```bash
GO=...go.exe
"$GO" test -overlay=<tmp>/overlay.json -vet=off -count=1 ./internal/pkg/observability ./internal/interface/http ./internal/application/agent_usecase ./internal/runtime/agentruntime -run 'TestCorrelationIntegration'
```

原生运行必须带 `-vet=off`（本主机 vet 忽略 overlay）。

**权威门槛（主会话基线已确认当前树干净）**：

```bash
GOOS=linux GOARCH=amd64 "$GO" build ./...    # 基线已验证 exit 0
GOOS=linux GOARCH=amd64 "$GO" vet ./...      # 基线已验证 exit 0
GOOS=linux GOARCH=amd64 "$GO" test -c ./internal/pkg/observability ./internal/interface/http ./internal/application/agent_usecase ./internal/runtime/agentruntime
```

**DoD 中 `go test ./... -count=1` 与 `go vet ./...` exit 0 的等价证据**（本机无法原生跑全量）：
1. `GOOS=linux GOARCH=amd64 go build ./...` + `go vet ./...` 全仓 exit 0（vet 等价）；
2. 上述四包 `GOOS=linux go test -c` 编译通过（全仓测试编译可抽查受影响包）；
3. overlay 全量套件 `go test -overlay ... -vet=off -count=1 ./...` 尽量跑；若出现**与既有 Windows 阻塞同类的预存失败**（如 `pkg/config` 的 `/workspaces` IsAbs 断言），记录为预存失败并给出「该包与本 change 零交集」的说明，不算任务失败；任何与本 change 四包或其依赖相关的失败都必须修复/上报。
4. 把以上全部证据原样写进报告。

## 约束

- **保护文件（不得改动/删除/重置）**：`internal/runtime/eventhub/hub.go`（工作区原有改动，保留原样）、`openspec/changes/sql-memory-es-hybrid/` 下所有文件、`.codegraph/`、`.codex/`、`openspec/changes/conversation-cache/`。
- **禁止任何 git 操作**（`auto_commit: false`；不 commit、不 stash、不 checkout）。
- 不新增 migration；不引入新依赖（标准库 + 既有 module 依赖）。
- 测试中如需真实 `authusecase.Service`，使用其构造函数 + 内存/fake repository；不绕过真实中间件链。
- 全文件遵循仓库现有风格（英文注释为主，与相邻测试文件一致）。

## 报告要求（写入 `openspec/changes/observability-correlation-tracing/task-5-report.md`）

1. RED 证据：五个测试的首次失败输出（每条一句话说明失败原因是断言而非编译错误）。
2. GREEN 证据：focused 命令 + 全量回归输出。
3. 对账表：五层（HTTP → StartTurn → worker → runtime → child tool）correlation 字段逐测试对账结果。
4. 隐私负向断言清单：扫描了哪些 key/值模式。
5. 兼容性证据：legacy run 无额外 durable record 的断言方式。
6. 全仓门槛证据：Linux build/vet/test -c + overlay 全量运行结果（含预存失败说明，如有）。
7. 若暴露任何生产缺陷：标红停住，附最小复现。
