# Exploration Handoff — memory-usecase-dead-code-cleanup

探索基于上一轮完整审查（全库调用点追踪，证据均为 file:line）。用户指令原话：「把所有冗余的部分都删除，现在开始 explore，然后自动进入 propose 之后 apply，整个过程要迅速！」

## 决策清单

| ID | 类别 | 决策 | 来源 |
| --- | --- | --- | --- |
| D1 | 战略 | 独立新 change `memory-usecase-dead-code-cleanup`，不扩展 unify-context-compaction / rename change | 用户原话「把所有冗余的部分都删除」；rename PR 独立 pending |
| D2 | 战略 | 复杂度 🟢 lightweight：纯删除、零行为变化、不新建 Mapper/Service | router 规则比对（「新建 Mapper/Service → 最低 🟡」不触发） |
| D3 | 依赖核实 | DreamWorker 死：`NewDreamWorker` 零生产构造点；`cmd/worker/main.go:356-359` 对 `memory:dream` 只 ACK 不执行 | B: cmd/worker/main.go:356-359 |
| D4 | 依赖核实 | ExtractionService 死：`ScheduleDream/StartExtraction/CompleteExtraction/FailExtraction/ProcessNextDream/calculateSimilarity/calculateJaccardSimilarity/FormatExtractionPrompt` 零调用 | B: 全库 grep 零外部调用 |
| D5 | 依赖核实 | CandidateService 死：`NewCandidateService` 零调用；路由表无 candidate 路由 | B: internal/interface/http/router.go:144-147 仅 4 条读路由 |
| D6 | 依赖核实 | MemoryCommandService 死：`Execute/Revoke/Supersede` 唯一调用者是 `Service.Create/Update/Delete`，后者零外部调用 | B: service.go:108,268,279；handler 写端点全 403 桩（memory_handler.go:112-150）；improvement.go:299-300 硬停 memory 提案 |
| D7 | 依赖核实 | Service 死成员：`retriever` 字段只赋值不读；`List/listCacheKey/Create/Update/Delete/CreateMemoryRequest/UpdateMemoryRequest/manualMemorySource/NewService/NewServiceWithCache` 无生产调用 | B: service.go:17,30；bootstrap/app.go:262 传入 retriever 但包内不读 |
| D8 | 选型+命名 | `DreamMessageRepository/dreamMessageBoundaryReader/dreamMessageRangeReader` 三接口从 dream_worker.go 挪入 durable_memory_pipeline.go，保持原名（最小 diff） | B: pipeline 仍用（durable_memory_pipeline.go:121,446,639） |
| D9 | 数值/常量 | 保留 `DreamJobType` 常量与 `cmd/worker` 的 `memory:dream`/`memory:codex` 排空 case（升级前在途旧 job 需 ACK） | B: cmd/worker/main.go:343,356 |
| D10 | 战略/范围 | 相邻层纳入清理：memory_handler 六个未路由方法 + `ConfigureCandidates`/`candidates`/`improvement` 字段；toolruntime `MemoryWriteTool`；agentruntime `MemoryCandidates` 字段；domain `CandidateWriter` 接口 | 用户原话「把所有冗余的部分都删除」 |
| D11 | 选型 | 有意保留：observability `RecordDream*` 指标（遥测表面，仪表盘依赖不可查）；config `memory_dream`/`codex_memory` 解析别名（有意兼容，config.go:173-176 注释） | B: config.go:173-176；仪表盘为外部依赖 |
| D12 | 战略 | 分支：从当前 HEAD（rename 分支）新建 `refactor/memory-usecase-cleanup`，stacked PR | 用户原话「整个过程要迅速」+ 既定 PR 工作流 |
| D13 | 依赖核实 | 验证：`~/sdk/go1.26.6/bin/go.exe` 做 `GOOS=linux go build ./... && go vet ./...`（本机无 runtime 测试环境） | B: 既有记忆 no-go-toolchain-on-machine（go1.26.6 交叉编译可用） |
| D14 | 选型 | 提交：英文 commit、按逻辑步拆分；push 后交付 PR title/body（gh 未认证） | 既定记忆 english-commits-pr-workflow |

## 客观阻塞

无。（runtime 测试不可用不构成阻塞：删除型变更以交叉编译 + 既有测试文件同步删除为验证手段，见 D13。）

## 下一步建议

进入 `vsdd-workflow-propose`（🟢 lightweight）：产出 proposal.md + tasks.md（删除清单即任务清单），随后 `vsdd-workflow-apply` 执行：先挪接口（D8）→ 删包内死文件/死成员 → 删相邻层 → 同步删除 dream_worker_test.go / extraction_test.go / service_test.go 死用例 → 交叉编译验证 → 英文提交、push、交付 PR 信息。
