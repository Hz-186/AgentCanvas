# Spec — memory-usecase-cleanup

## REQ-1: durable pipeline 是唯一记忆生成路径

代码库中不存在候选提案（candidate/proposal）写路径：`CandidateWriter` 接口、`CandidateService`、`MemoryWriteTool`、`MemoryCandidates` 依赖字段全部移除。durable memory 仅由 `DurableMemoryWorker`（extract→consolidate）与 `DurableFileStore.AppendAdHocNote` 产生。

## REQ-2: SQL 记忆存储仅暴露只读视图

`memory_usecase.Service` 仅保留 `ListFiltered/Get/GetMany/ListRecallLogs/SetRecallFeedback`。HTTP 层仅保留 `GET /memories`、`GET /memories/:id`、`GET /memory-recall-logs`、`POST /memory-recall-logs/:id/feedback` 四条路由（现状即如此，不新增不减少）。

## REQ-3: 旧传输 job 类型继续排空

`DreamJobType`（`memory:dream`）常量与 `cmd/worker` 中 `memory:dream`/`memory:codex` 的 ACK 排空 case 保留，直至迁移窗口结束（本 change 不删除）。

## REQ-4: pipeline 依赖接口随 pipeline 居住

`DreamMessageRepository`、`dreamMessageBoundaryReader`、`dreamMessageRangeReader` 定义于 `durable_memory_pipeline.go`，名字不变，行为不变。

## REQ-5: 零行为变化验收

删除前后：`GOOS=linux go build ./...` 与 `go vet ./...` 通过；`go test ./internal/application/memory_usecase/...` 通过；全库 grep `DreamWorker|ExtractionService|CandidateService|MemoryCommandService|CandidateWriter|MemoryWriteTool` 在 .go 文件中零命中。
