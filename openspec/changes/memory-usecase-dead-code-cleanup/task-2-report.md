# Task 2 Report — Remove candidate-proposal write path dead chain

### Skills Loaded: test-driven-development, vsdd-workflow-reverse-sync

- Branch: `refactor/memory-usecase-cleanup` (HEAD = BASE = `81be279`, Task 1 already committed)
- Status: **DONE**
- Scope discipline: pure deletion, zero behavior change, nothing committed.

## TDD evidence (deletion-only adaptation)

### RED baseline (before edits)

```
$ grep -rn --include="*.go" "CandidateWriter\|MemoryWriteTool\|MemoryCandidates\|ConfigureMemoryCandidates\|CandidateRequest" internal/ | wc -l
28
```

Nonzero, as required. Hits spanned `internal/domain/memory/repository.go`, `internal/runtime/agentruntime/{agent_runtime,dependencies,runtime}.go`, `internal/runtime/toolruntime/memory_tools.go`, `internal/runtime/toolruntime/memory_tools_test.go`.

### GREEN (after edits)

```
$ grep -rn --include="*.go" "CandidateWriter\|MemoryWriteTool\|MemoryCandidates\|ConfigureMemoryCandidates\|CandidateRequest" internal/ cmd/ | wc -l
0
```

Zero hits repo-wide (internal/ and cmd/).

## Changed files (git diff HEAD -- internal/: 6 files, 1 insertion, 150 deletions)

1. `internal/runtime/toolruntime/memory_tools.go` (-62): deleted `MemoryWriteTool` type, its 5 methods (Name/Description/Parameters/Metadata/Execute), `memoryWriteInput` type, and the `Candidates memory.CandidateWriter` field. `MemoryReadTool`, `SessionSearchTool`, and `projectIDFromToolRunContext` untouched. Repo-wide grep found **no registration/construction site** for `MemoryWriteTool` — the type was already fully orphaned (wiring removed before this task); no `"write_memory"` string remains outside the deleted `Name()` method.
2. `internal/runtime/toolruntime/memory_tools_test.go` (-55): deleted `fakeMemoryCandidateWriter` and the 3 `MemoryWriteTool` tests (`TestMemoryWriteToolCreatesReviewCandidate`, `TestMemoryWriteToolNeverSelfApprovesConflictingFact`, `TestMemoryWriteToolCarriesExplicitProjectScopeWithoutWorkspace`). Required for compilation and for the zero-hit GREEN criterion — these tests exclusively exercised the deleted dead chain. 7 tests remain (session search + read tool). Note: `fakeMemoryLogRepo` was already unused before this task and was left as-is (out of scope).
3. `internal/runtime/agentruntime/dependencies.go` (-3/+1): removed `MemoryCandidates memory.CandidateWriter` from `Repositories` struct; removed `MemoryCandidates: deps.MemoryCandidates,` from the `buildRuntimeCore` pass-through literal (the single +1 line is the re-wrapped literal, content otherwise identical).
4. `internal/runtime/agentruntime/runtime.go` (-1): removed `MemoryCandidates memory.CandidateWriter` from `coreRepositories`.
5. `internal/runtime/agentruntime/agent_runtime.go` (-5): removed `ConfigureMemoryCandidates` and the now-unused `agentcanvas/internal/domain/memory` import (its only user was that method; `RuntimeMemoryPolicy`/`memory.Policy` live in runtime.go).
6. `internal/domain/memory/repository.go` (-25): removed `CandidateRequest` struct and `CandidateWriter` interface. Pre-deletion grep confirmed the retired `MemoryWriteTool.Execute` (memory_tools.go:223) was the last `Suggest`/`CandidateRequest` user; no other implementer exists (candidate service was removed by Task 1 / BASE commit). `context` import still used by `Commander`/`Repository` etc.

## Verification (all exit 0, go = ~/sdk/go1.26.6/bin/go.exe)

```
$ GOOS=linux go build ./...
BUILD_EXIT=0

$ GOOS=linux go vet ./internal/runtime/... ./internal/domain/memory/...
VET_EXIT=0

$ go test -count=1 ./internal/application/memory_usecase/...
ok  	agentcanvas/internal/application/memory_usecase	1.796s
TEST_EXIT=0
```

`GOOS=linux go vet` on `./internal/runtime/...` also type-checks the edited test files, confirming they compile cleanly for the target platform.

Known pre-existing issue (NOT caused by this task): Windows-native `go test ./internal/runtime/toolruntime/...` fails at baseline with `undefined: syscall.Flock` in `internal/runtime/toolruntime/filesystem_path.go` (0 diff lines in that file; documented in task instructions as the reason GOOS=linux is used for build/vet).

## Self-review checklist

- [x] Skills loaded before any edit (hard gate)
- [x] RED baseline captured (28 hits) before edits
- [x] GREEN grep zero repo-wide (internal/ and cmd/)
- [x] GOOS=linux build ./... exit 0
- [x] GOOS=linux vet (runtime + domain/memory) exit 0
- [x] memory_usecase tests green, fresh run (-count=1)
- [x] No references outside the planned chain — no BLOCKED condition hit
- [x] `projectIDFromToolRunContext` kept (still used by `MemoryReadTool.Execute` memory_tools.go:161 and `subagent_tool.go:157`)
- [x] `MemoryReadTool`, `SessionSearchTool`, `Commander`, all other memory interfaces untouched
- [x] Diff is 150 deletions + 1 re-wrapped literal line — pure deletion, zero behavior change
- [x] Nothing committed (working tree only)

## Concerns

None blocking. Minor observations for follow-up tasks (not touched, out of scope):
- `fakeMemoryLogRepo` in `memory_tools_test.go` is unused (pre-existing; likely orphaned by Task 1).
- `memory.WriteLogRepository` plumbing (`MemoryWriteLogs`/`MemoryLogs` fields in agentruntime) survives this task per scope; only the candidate chain was in Task 2's remit.
