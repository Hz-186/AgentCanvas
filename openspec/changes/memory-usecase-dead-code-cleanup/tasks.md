# Tasks — memory-usecase-dead-code-cleanup

## 任务依赖关系

```
T1（包内清理）──▶ T2（相邻层清理）──▶ T3（验证+交付）
```

串行原因：T2 删除的 `CandidateWriter` 接口仍被 T1 前的包内文件实现/引用；T3 的全库 grep 零残留断言依赖 T1+T2 完成。不可并行，不拆 worktree。

- [x] Task 1: 清理 memory_usecase 包内死代码

  **Files:**
  - Move: `dream_worker.go:27-44` 三接口 → `durable_memory_pipeline.go`
  - Delete: `dream_worker.go`, `dream_worker_test.go`, `extraction.go`, `extraction_test.go`, `candidate_service.go`, `command_service.go`
  - Modify: `dream_config.go`（仅留 `DreamJobType`）、`service.go`（删 retriever 字段/`NewService`/`NewServiceWithCacheAndRetriever`/`List`/`listCacheKey`/`Create`/`Update`/`Delete`/DTO/`manualMemorySource`/`commands` 字段/`ConfigureCommands`/`invalidateCache`）、`service_test.go`（删对应死用例）
  - Modify: `internal/bootstrap/app.go:262-264`（改 `NewServiceWithCache`，删 `memoryCommandService` 与 `ConfigureCommands`）
  - Modify: `internal/interface/http/handler/memory_handler.go`（删 `candidates`/`improvement` 字段、`ConfigureCandidates`、`ListCandidates`/`ApproveCandidate`/`RejectCandidate`/`decideCandidate`/`Create`/`Update`/`Delete`/`memoryWritesDisabled`——编译耦合：直接引用 T1 删除的 `CandidateService` 类型，须同 task 删除）

  **验收:** 包内仅余 `durable_memory_pipeline.go`、`durable_memory_files.go`、精简 `service.go`、`dream_config.go`（常量）及活测试。
  **最小验证:** `go build ./...` exit 0。
  **DoD:** 上述文件/成员删除完毕且全库编译通过；`DreamJobType` 与三接口保留。
  **Commit:** `refactor(memory): remove retired dream and candidate write paths`

- [x] Task 2: 删除相邻层死链

  **Files:**
  - Modify: `internal/runtime/toolruntime/memory_tools.go`（删 `MemoryWriteTool` 及注册点）
  - Modify: `internal/runtime/agentruntime/dependencies.go`（删 `MemoryCandidates` 字段与传递）、`runtime.go`（同）、`agent_runtime.go`（删 `ConfigureMemoryCandidates`）
  - Modify: `internal/domain/memory/repository.go`（删 `CandidateWriter` 接口与 `CandidateRequest`）

  **验收:** 全库 grep `CandidateWriter|MemoryWriteTool|MemoryCandidates|ConfigureMemoryCandidates` 在 .go 零命中。
  **最小验证:** `go build ./...` exit 0。
  **DoD:** 相邻层死链删除完毕且编译通过；路由表四条读路由不变。
  **Commit:** `refactor(runtime): drop retired candidate writer chain`

- [x] Task 3: 全量验证与交付

  **步骤:** `GOOS=linux go build ./... && go vet ./...`；`go test ./internal/application/memory_usecase/...`；grep 零残留断言（REQ-5）；push 分支 `refactor/memory-usecase-cleanup`；交付 PR title/body（gh 未认证则文本交付）。
  **验收:** REQ-5 全部满足。
  **DoD:** 验证命令输出全绿 + 分支已 push + PR 信息交付。
  **Commit:** （如无残留修复则无额外 commit）
