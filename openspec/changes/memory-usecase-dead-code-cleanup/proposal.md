# Proposal — memory-usecase-dead-code-cleanup

## Why

上一轮记忆重构（working-memory 清理 + codex→durable 更名）采用「入口拆信管、函数体保留」策略：`CandidateService.Suggest` 硬报错、HTTP 写端点 403、`memory:dream` job 只 ACK 不执行。全库调用点追踪证实：旧链路（SQL 记忆写入 + 候选提案 + Dream/legacy extraction）的所有入口均已切断，函数体成为零生产调用者的僵尸代码，约 1,850 行（含测试），另加相邻层约 150 行。删除不改变任何运行时行为。

## What Changes

- 删除 `memory_usecase` 包内死文件：`dream_worker.go(+test)`、`extraction.go(+test)`、`candidate_service.go`、`command_service.go`。
- 精简 `dream_config.go` 至仅保留 `DreamJobType` 常量（排空旧队列 job 需要）。
- 精简 `service.go` 至只读视图：删 `retriever` 死字段、`List/Create/Update/Delete`、请求 DTO、`NewService/NewServiceWithCacheAndRetriever`、`commands` 字段；保留 `ListFiltered/Get/GetMany/ListRecallLogs/SetRecallFeedback` 与缓存辅助。
- 将仍被 durable pipeline 复用的三个接口（`DreamMessageRepository`、`dreamMessageBoundaryReader`、`dreamMessageRangeReader`）从 `dream_worker.go` 挪入 `durable_memory_pipeline.go`，保持原名。
- 删除相邻层死链：`memory_handler.go` 六个未路由方法 + `ConfigureCandidates`/`candidates`/`improvement` 字段；`toolruntime.MemoryWriteTool` 及其注册；`agentruntime` 的 `MemoryCandidates` 字段与 `ConfigureMemoryCandidates`；`domain/memory` 的 `CandidateWriter` 接口与 `CandidateRequest`。
- `bootstrap/app.go` 同步：`NewServiceWithCacheAndRetriever`→`NewServiceWithCache`，删 `memoryCommandService` 构造与 `ConfigureCommands`。

## Non-Goals

- 不删 observability `RecordDream*` 指标（遥测表面，仪表盘依赖不可查）。
- 不删 config `memory_dream`/`codex_memory` 解析别名（有意兼容，config.go:173-176）。
- 不删 `cmd/worker` 的 `memory:dream`/`memory:codex` 排空 case（在途旧 job）。
- 不动 SQL 读视图与 durable pipeline/files 本身。

## Risk

零行为变化：所有被删代码入口今天已返回 disabled/403/ACK。唯一风险为编译级遗漏，以 `go build/vet` 全库 + 包内测试兜底。
