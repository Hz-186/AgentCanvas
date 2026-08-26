# unify-context-compaction 交付验证清单

## 前置条件

- Go 1.22+ 工具链（`go` 在 PATH 中）
- POSIX 环境执行测试（`toolruntime`/`workspace_usecase` 使用 `syscall.Flock/Kill`，Windows 原生无法编译这些包；本仓库既有约束，与本变更无关）
- Node 18+（前端）；vitest 需要支持 `localStorage` 的运行时（Node 22 需 `--localstorage-file`，否则全部前端测试在 `beforeEach` 崩溃——本环境既有问题，与本变更无关）

## 完整命令集（需有 Go 工具链环境执行）

```bash
# 后端
go build ./...
go vet ./...
go test ./internal/...

# 前端
cd web
npm install
npm run typecheck
npx vitest run
cd ..

# OpenSpec
npx openspec validate unify-context-compaction
```

## RED 清单 → 测试映射

| RED 项 | 覆盖测试 | 位置 |
| --- | --- | --- |
| legacyConversationCompacts | TestPrepareCompactsFullHistoryAndRetainsUsers（纯文本历史端到端）+ TestCompactSummarizesAllEntryTypes | coordinator_test.go / compact_test.go |
| mixedHistoryEndToEnd | TestPrepareSummarizerInputIncludesToolEntries + TestPrepareRenderReplaysToolPairing + TestLoadRebuildsFrozenUsersAndTail | coordinator_test.go |
| goalContinuationDeveloperRetained | TestTokenBudgetCompactionSkipsSummaryModel（token_budget 总结 0 次、仅留 developer）+ TestRetainEntriesByRoleKeepsOnlyRequestedRole | coordinator_test.go / entry_test.go |
| memoryAndReflectionBlocksUnaffected | TestContextAssemblerSortsCoreMemoryBeforeHistoryAndRetrieval + TestContextAssemblerDropsReflectionBeforeMandatoryRules + TestContextAssemblerKeepsPinnedBlocksAndOmitsOverflow | context_assembler_test.go |
| handoffTableRemovalsVerified | grep 清扫（下表核对，全部无残留）；`go vet` 证明无残留引用 | 本次审计实测 |

## 12 处重复点删除核对（exploration-handoff.md 附表）

| # | 重复点 | 原位置已删除 | 核心包唯一实现 |
| --- | --- | --- | --- |
| 1 | 总结函数 | T3（coordinator）/ T4（runner） | compaction.Compact / summarize — compact.go:90/:133 |
| 2 | 总结 prompt 文案 | 同上 | summarize prompt — compact.go:138 |
| 3 | 缺省 system | 同上 | summarize 缺省 system — compact.go:142-145 |
| 4 | 丢最早重试 | 同上 | ErrContextWindowExceeded → drop oldest — compact.go:179-186 |
| 5 | 退避重试 | 同上 | 泛型错误退避 ≤2 次 — compact.go:187-195 |
| 6 | retain | T3/T4 委托核心；T8 审计收敛 coordinator/runner 两侧残留的本地 retain 循环 → 核心 | RetainEntriesByRole / RetainUserEntries — retain.go:11/:19 |
| 7 | 阈值 0.90 | grep 无本地字面量 | compaction.ThresholdRatio — compact.go:18 |
| 8 | 预算 20,000 | grep 无本地字面量 | compaction.UserMessageBudgetTokens — compact.go:17 |
| 9 | 前缀 | 两侧引用核心常量 | compaction.SummaryPrefix — compact.go:20 |
| 10 | 超时 20s | 同上 | compaction.SummarizeTimeout — compact.go:19 |
| 11 | 空摘要兜底 | 两侧引用核心常量 | compaction.FallbackSummary — compact.go:21（:136/:175 使用） |
| 12 | 二分截断 | T8 审计删除 coordinator 本地 truncateTokens；runner 侧 T4 已删 | compaction.TruncateToTokens — retain.go:51 |

**grep 实测（2026-08-26）**：`summarizeContext|retainUserMessages|truncateMessageTokens|truncateRunesToTokens|defaultAutoCompactRatio`、`func (c Coordinator) summarize`、字面量 `20_000|0.90`、retain/二分循环体——在 `coordinator.go` 与 `auto_compaction.go` 中全部 0 匹配。

## 本会话已执行证据（2026-08-26，Windows + go1.26.6 交叉编译）

| 命令 | 结果 |
| --- | --- |
| `GOOS=linux go build ./...` | exit 0 |
| `GOOS=linux go vet ./...`（含测试文件） | exit 0 |
| `go test -count=1 ./internal/...`（原生） | 41 包通过，含 `runtime/compaction`（16 测试）、`runtime/conversationcontext`（12 测试） |
| 原生构建失败包（16 个） | 全部为既有 `syscall.Flock/Kill` Linux-only 约束；另有 1 个既有 Windows 路径配置测试失败（`pkg/config`，本分支未触碰，`git diff main -- internal/pkg/config/` 为空） |
| `cd web && npx tsc --noEmit` | exit 0 |
| `npx vitest run` | 既有环境问题（`localStorage` 不可用导致 `beforeEach` 崩溃）；stash 基线对照 15/15 同样失败，证明非本变更引入 |
| `npx openspec validate unify-context-compaction` | valid |

## 需在完整环境补跑的项

1. `go test ./internal/runtime/agent/... ./internal/runtime/agentruntime/... ./internal/application/agent_usecase/...`——T4/T5/T6 新增测试（编译已由 `GOOS=linux go vet` 覆盖，运行需 POSIX）
2. `npx vitest run` 在支持 localStorage 的运行时下跑绿（含新增 typed-message 过滤测试）
