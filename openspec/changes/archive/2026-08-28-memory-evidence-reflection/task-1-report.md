# Task 1 Implementer Report — Persist tool error state into message rows

> change: memory-evidence-reflection | branch: refactor/memory-usecase-cleanup | complexity 🔴 | date: 2026-08-28

### Skills Loaded:
test-driven-development, vsdd-workflow-reverse-sync

---

## REVERSE SYNC (recorded before continuing)

**冲突点**：派发说明环境节声称仅 `internal/infrastructure/mysql` 在 Windows 上不可编译（syscall.Flock），并要求原生执行 `go test ./internal/runtime/agent ./internal/runtime/compaction ./internal/runtime/agentruntime`。代码事实：`internal/runtime/toolruntime/filesystem_path.go:100,106` 直接使用 `syscall.Flock/LOCK_EX/LOCK_UN`（commit 7e624eb 引入，无构建标签），导致 toolruntime 及其上游 `agent`/`agentruntime` 在 Windows 上同样无法编译（实测；本机无 WSL 分发、无 Docker）。

**处理**：已按 reverse-sync skill 记录于本 change `log.md`，`.vsdd-state.yaml` 置 `reverse_sync_required: true` 并写入 `known_issues`。实现者**未修改任何装运代码**：测试执行改用 `go test -overlay`，把 `filesystem_path.go` 映射为位于 `%TEMP%\agentcanvas-task1-overlay\` 的等价 Windows 可编译副本（仅两处 Flock 调用替换为进程内 no-op；`acquirePathLock` 路径不被这三个包的任何测试触达；进程内互斥锁语义保留）。装运构建门禁为无 overlay 的 `GOOS=linux go build ./...`（exit 0）。待主会话回写：更正派发模板环境说明，并决定是否单独立项为 toolruntime 增加 Windows flock 兼容层。

---

## RED evidence

### Cycle 1 — ToolResultStepTest（agent 包，新文件 `internal/runtime/agent/tool_error_code_test.go`）

RED-1a（编译失败，缺失生产 API）：

```
# agentcanvas/internal/runtime/agent [agentcanvas/internal/runtime/agent.test]
internal\runtime\agent\tool_error_code_test.go:66:11: step.ErrorCode undefined (type RunStep has no field or method ErrorCode)
internal\runtime\agent\tool_error_code_test.go:85:56: (&Runner{…}).newToolResultStep undefined (type *Runner has no field or method newToolResultStep)
internal\runtime\agent\tool_error_code_test.go:111:27: step.ErrorCode undefined (type RunStep has no field or method ErrorCode)
FAIL	agentcanvas/internal/runtime/agent [build failed]
```

RED-1b（仅补字段与等价提取后的行为失败，因缺失而失败，非拼写错误）：

```
--- FAIL: TestToolResultStep (1.36s)
    --- FAIL: TestToolResultStep/shouldCarryErrorCodeFromResultMetadata (0.66s)
    tool_error_code_test.go:67: tool_result step must carry the metadata error code, got ""
    --- FAIL: TestToolResultStep/shouldCarryIssueErrorCode (0.00s)
    tool_error_code_test.go:90: issue results must surface their issue code on the step, got ""
    --- PASS: TestToolResultStep/shouldLeaveErrorCodeEmptyOnSuccess (0.70s)   ← 成功路径基线本就为空
```

### Cycle 2 — TranscriptErrorEnrichmentTest + ToChat 泄漏锁（编译失败）

```
internal\runtime\agentruntime\transcript_error_enrichment_test.go:58:9: undefined: agent.EnrichTranscriptEntries
internal\runtime\agentruntime\transcript_error_enrichment_test.go:61:13: failed.IsError undefined (type compaction.Entry has no field or method IsError)
internal\runtime\agentruntime\transcript_error_enrichment_test.go:61:58: failed.ErrorCode undefined (type compaction.Entry has no field or method ErrorCode)
…（同类错误共 10+ 行）
FAIL	agentcanvas/internal/runtime/agentruntime [build failed]

internal\runtime\compaction\entry_error_fields_test.go:19:145: unknown field IsError in struct literal of type Entry
internal\runtime\compaction\entry_error_fields_test.go:19:169: unknown field ErrorCode in struct literal of type Entry
FAIL	agentcanvas/internal/runtime/compaction [build failed]
```

### Cycle 3 — MessageSinkRowTest（行为失败）

```
--- FAIL: TestMessageSinkRow (0.00s)
    --- FAIL: TestMessageSinkRow/shouldWriteErrorMetadataForFailedOutput
    message_sink_error_metadata_test.go:62: metadata key set = map[tool_call_id:call_1 tool_name:lookup], want exactly [tool_call_id tool_name is_error error_code]
    --- FAIL: TestMessageSinkRow/shouldWriteExplicitSuccessFlag
    message_sink_error_metadata_test.go:79: enriched success rows must carry an explicit is_error flag so legacy unknown rows stay distinguishable: map[tool_call_id:call_1 tool_name:lookup]
    --- FAIL: TestMessageSinkRow/shouldWriteEmptyErrorCodeWhenStepLacksCode
    message_sink_error_metadata_test.go:96: metadata key set = map[tool_call_id:call_1 tool_name:lookup], want exactly [tool_call_id tool_name is_error error_code]
    --- PASS: TestMessageSinkRow/shouldNotAddErrorKeysToFunctionCallRows      ← 旧行为守卫
    --- PASS: TestMessageSinkRow/shouldKeepReasoningAndSystemEchoDropped      ← 旧行为守卫
--- FAIL: TestTranscriptErrorEnrichment/shouldBeDeterministicAcrossReplays
    transcript_error_enrichment_test.go:122: replayed failed output rows must carry is_error metadata
```

---

## GREEN evidence

命令（Windows 主机；`-overlay` 仅为测试执行环境垫片，见 REVERSE SYNC；GO 为 `D:\Users\hongze01.zhang\sdk\go1.26.6\bin\go.exe`）：

```
$GO test -overlay %TEMP%\agentcanvas-task1-overlay\overlay.json -count=1 ./internal/runtime/agent ./internal/runtime/agentruntime
$GO test ./internal/runtime/compaction
```

Cycle 1：

```
--- PASS: TestToolResultStep (1.42s)
    --- PASS: TestToolResultStep/shouldCarryErrorCodeFromResultMetadata (0.77s)
    --- PASS: TestToolResultStep/shouldCarryIssueErrorCode (0.00s)
    --- PASS: TestToolResultStep/shouldLeaveErrorCodeEmptyOnSuccess (0.65s)
ok  agentcanvas/internal/runtime/agent 3.106s
```

Cycle 2：

```
--- PASS: TestTranscriptErrorEnrichment (0.00s)
    --- PASS: TestTranscriptErrorEnrichment/shouldEnrichOutputEntriesFromStepLookup
    --- PASS: TestTranscriptErrorEnrichment/shouldLeaveUnknownWhenStepMissing
    --- PASS: TestTranscriptErrorEnrichment/shouldBeDeterministicAcrossReplays
--- PASS: TestMessageSinkRowShouldNotLeakErrorFieldsThroughToChat (0.00s)
ok  agentcanvas/internal/runtime/agentruntime 1.686s / ok internal/runtime/compaction 1.358s
```

Cycle 3：

```
--- PASS: TestMessageSinkRow (0.00s)        （5 子测试全过）
--- PASS: TestTranscriptErrorEnrichment (0.00s)
ok  agentcanvas/internal/runtime/agentruntime 1.488s
```

最终全量（-count=1，非缓存）：

```
ok  agentcanvas/internal/runtime/agent          8.165s
ok  agentcanvas/internal/runtime/agentruntime   4.610s
ok  agentcanvas/internal/runtime/compaction    56.810s
go vet 三包：退出码 0（含 -overlay 变体）
```

构建门禁（无 overlay，装运代码）：

```
$ GOOS=linux $GO build ./...
LINUX_BUILD_EXIT=0
```

DoD grep：

```
$ grep -rn "is_error" internal/runtime/agentruntime/message_sink.go
internal/runtime/agentruntime/message_sink.go:108:			meta["is_error"] = *entry.IsError
```

---

## Files changed

生产代码（4 个，均在任务文件清单内）：
- `internal/runtime/agent/types.go` — RunStep 增 `ErrorCode string \`json:"error_code,omitempty"\``（:174）。
- `internal/runtime/agent/runner.go` — ① 批结果循环站点（原 :768-783）改为 `r.newToolResultStep(item, post)`；② 新增 `newToolResultStep`（含 ErrorCode 填充与 Error 归位）与 `toolResultErrorCode`（从 `ToolResult.Metadata["error_code"]` 提取，nil/非字符串安全）；③ `persistTranscriptEntries` 在 FromChatAt 之后调用新增的导出函数 `EnrichTranscriptEntries(persisted, result.Steps)`。
- `internal/runtime/compaction/entry.go` — Entry 增 `IsError *bool`（nil=unknown，三态语义）与 `ErrorCode string`。
- `internal/runtime/agentruntime/message_sink.go` — `rowFor` 的 function_call_output 分支：`entry.IsError != nil` 时确定性地写入 `is_error` 与 `error_code` 两键；未富化保持旧两键形状。

测试代码（3 个新文件）：
- `internal/runtime/agent/tool_error_code_test.go` — TestToolResultStep×3 子测试 + 辅助（`errorMetadataTool` fake、`toolResultStepByCallID`）。
- `internal/runtime/agentruntime/transcript_error_enrichment_test.go` — TestTranscriptErrorEnrichment×3 子测试（富化/未命中/回放确定性，经真实 sink 双通道字节比对）。
- `internal/runtime/agentruntime/message_sink_error_metadata_test.go` — TestMessageSinkRow×5 子测试 + 辅助。
- `internal/runtime/compaction/entry_error_fields_test.go` — ToChat 泄漏锁 + `llm.ChatMessage` 字段集编译期锁。

流程工件：`log.md`（Reverse Sync 记录）、`.vsdd-state.yaml`（`reverse_sync_required: true` + known_issues）。

## Implementation notes

1. **三态表达**：`Entry.IsError *bool` —— nil=未富化（unknown，行字节兼容旧行）；`&true`=失败；`&false`=显式成功（可区分旧行）。`ErrorCode` 恒为确定性字符串（无码失败为 `""`，键仍写入）。
2. **富化键集**（json.Marshal 对 map 按键排序 → 字节确定）：富化失败输出 = `{error_code, is_error, tool_call_id, tool_name}`（即任务断言的四键集）；未富化输出 = `{tool_call_id, tool_name}` 与旧行字节一致；**富化成功输出同样写四键**（`is_error:false, error_code:""`）——任务只钉死失败/未富化两集，成功集取均匀键集以简化下游三态解析（见偏差 §3）。
3. **查表只认 `StepTypeToolResult`**：tool_call step 与 tool_result step 共享同一 ToolCallID，若不过滤类型会用 tool_call 的零值 IsError 错误覆盖；测试用同 ID 的 tool_call+tool_result 步骤对锁定。同名工具跨批不误匹配由三个同名单测调用（不同 ID）覆盖。
4. **issue 路径的测试接缝**：真实 runner 中带 Issue 的调用在 `runner.go:589-606` 内联短路、不进批循环；`executeOne` 对 Issue 的处理是防御性路径。`shouldCarryIssueErrorCode` 因此由两段真实生产代码组合：`ExecuteToolBatch`（真实执行，断言 issue 结果不触达 execute 回调）→ `newToolResultStep`（真实步骤构建）。为此把步骤构建从批循环提取为 `newToolResultStep`——这是让该场景测到生产代码的最小接缝。
5. **附带修复（有意、已文档化）**：原批循环在 `appendStep` 返回值（副本）上改 `step.Error`，导致 `result.Steps` 中的 tool_result 步骤永远缺 Error 文本（仅事件流副本携带）。`newToolResultStep` 在入列前构建完整步骤（含 Error）。`reflection.go:99,119` 读 `result.Steps` 的 `step.Error`，此修复是 Task 8「提示词含错误文本」的前置条件。既有全包测试保持绿。
6. **`EnrichTranscriptEntries` 导出**：唯一生产调用方是 agent 包内的 `persistTranscriptEntries`；导出是为让 `shouldBeDeterministicAcrossReplays` 在 agentruntime 包内走完整真实链路（FromChatAt → 富化 → 真实 sink → metadata_json 字节比对），否则字节比对无法触达真正产生 `verifyTranscriptPayload` 比对对象的 rowFor。
7. **reasoning/system_echo 短路**：sink 的 default 分支在 metadata 构建之前返回 nil（结构未变），测试断言 0 行。
8. **`llm.ChatMessage` 零改动**：由 `entry_error_fields_test.go` 中 struct 转换式编译期锁锁定（字段名/类型/顺序/tag 任一变化即编译失败；vet 干净）。

## Self-review checklist

- [x] 12 个 RED 场景全部实现并通过（场景名逐字保留为子测试/函数名）
- [x] 生产代码只在对应失败测试存在后才写；每个周期先看到失败（编译失败或断言失败）再转绿
- [x] 键集断言精确：富化失败四键、未富化两键（与旧行一致）、function_call 三键
- [x] 富化仅按精确 ToolCallID 匹配 result.Steps（同名工具、tool_call 同 ID 干扰项均覆盖）
- [x] 未命中查表不添加错误字段（字节兼容旧行；下游按 unknown）
- [x] 回放确定性：同转录+同步骤两次经真实 sink，metadata_json bytes.Equal + TranscriptEntryID/Content/ContentType 逐行相等，且富化行确实携带 is_error（防空洞比对）
- [x] ToChat 不泄漏新字段（与无字段输入逐字段相等）；ChatMessage 字段集编译期锁定
- [x] 每 entry 恰 1 次 writer.Create（行数断言）；reasoning/system_echo 0 次
- [x] `go vet` 干净；改动严格限于 Task 1 文件（垫片在仓库外临时目录，非提交物）
- [x] 未提交任何 commit（主会话拥有提交权）

## REFACTOR notes

除 vet 整改外无：初版泄漏锁用无键复合字面量触发 `go vet` composite 警告，重构为结构体转换式编译期锁（同等锁定强度、vet 干净）。其余命名/结构在 GREEN 阶段已按仓库风格定型，无进一步行为保持型清理。

## Deviations（逐条理由）

1. **测试执行使用 `go test -overlay` 垫片**：派发环境节与代码事实冲突（见 REVERSE SYNC），不垫片则三个包中两个在 Windows 上无法执行任何测试。垫片位于仓库外临时目录、不进提交、不影响装运构建门禁；`acquirePathLock` 不被任何相关测试触达，语义无损。已按 reverse-sync 流程记录并置 `reverse_sync_required: true` 待回写。
2. **`result.Steps` 的 tool_result 步骤开始携带 Error 文本**（原仅在事件流副本）：提取 `newToolResultStep` 时把 Error 移入列前构建；属 Task 1 错误状态持久化语义且为 Task 8 前置，全包测试保持绿（见实现注 §5）。
3. **富化成功行写四键（含 `error_code:""`）**：任务只钉死失败四键与未富化两键；取均匀键集使「富化行恒四键」规则单一、字节确定性更易审计，下游三态解析只看 is_error。
4. **`shouldNotLeakErrorFieldsThroughToChat` 落在 compaction 包**（函数名 `TestMessageSinkRowShouldNotLeakErrorFieldsThroughToChat`）：ToChat 属 compaction，agentruntime 的 `TestMessageSinkRow` 承载其余 5 个 sink 场景；场景名逐字保留。
5. **`EnrichTranscriptEntries` 导出**：理由见实现注 §6（回放确定性场景必须经真实 sink 产生 metadata_json 字节）。
6. **富化查表过滤 `StepTypeToolResult`**：任务文字「按 ToolCallID 查 result.Steps」未提类型过滤，但 tool_call 步骤共享 ToolCallID 且 IsError 恒为零值，不过滤将污染富化结果；测试显式覆盖该干扰项。
