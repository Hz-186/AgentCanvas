# Log — memory-evidence-reflection

## 2026-08-28 propose

### Skills Loaded: vsdd-workflow-router, vsdd-workflow-propose, vsdd-workflow-reverse-sync, vsdd-workflow-design-review, openspec-propose, writing-plans

> writing-plans 的产物位置默认（docs/superpowers/plans/）被 VSDD tasks.md 取代：按 propose skill「tasks/RED 拆解以已加载 skill 为唯一权威」，本 change 的实施计划即 `tasks.md`（含 files/RED/GREEN/ASSERT/DoD），评审报告见 `doc/memory-reflection-evidence-plan-v2.md`。

| 事件 | 说明 |
| --- | --- |
| 输入 | 用户附件计划 + 三个只读审计子代理报告（AgentCanvas 记忆管线 / Codex `D:\codex-src` / 反思与持久化）+ 主会话复核；严格评审结论落盘 `doc/memory-reflection-evidence-plan-v2.md` |
| Explore | 未走 explore 阶段：需求已由评审+审计收敛为明确合同（等价于 exploration handoff 的决策输入），用户明示直接进入全流程；按 S2，无"声称来自 explore"情形 |
| OpenSpec | `openspec new change` 因目录预建拒绝（与 sql-memory-es-hybrid 相同）；按 propose fallback 手写 `.openspec.yaml` |
| Templates | 宿主 `~/.zcode/templates/` 缺失（`spec.md/tasks.md/log.md/vsdd-state.example.yaml/exploration-handoff.md` 均不存在）。处理：以本仓库既往 standard change（`sql-memory-es-hybrid`）的工件结构为 schema 基准；记录此环境缺口，不阻塞流程（用户已授权全流程自走） |
| Complexity | 🔴 standard（跨 5 模块、9 任务、新建 renderer/仓储方法/写接线） |
| Reverse Sync | 原计划 4 处事实性错误已回写修订（RawMemory→episodic 路径不存在→改为无模型文本倾倒；压缩证据前提不全→新增归档感知读路径；候选门禁预设的写接线不存在→列为显式新建任务；"AgentStep warning 事件"不存在→复用 StepTypeError 载荷模式）；artifacts 与代码事实一致，`reverse_sync_required=false` |
| Dependency graph | T1,T2 → T3,T4 → T5 → T6 → T7；T8/T9 文件独立可与 Wave2/3 并行；execution_mode=serial 下逐个执行 |
| Mode | `complexity_mode=standard`，`active_review_stage=design_review` |

### Design review round 1 — FAIL（3 Must Fix，全部已回写）

| Must Fix | 事实核验 | 回写处理 |
| --- | --- | --- |
| Task 1 接缝不存在：真实链路是 `[]llm.ChatMessage → FromChatAt → sink`（runner.go:847-870），无 RunStep→Entry 转换；且 `verifyTranscriptPayload`（message_repo.go:201-221）要求回放字节一致 | 主会话复核 runner.go/message_repo.go 属实 | design Decision 1 重写为"runner 侧 entry 富化 + 查表未命中不加键"；Task 1 重写（+4 RED：ErrorCode 载体×3、富化确定性×1）；spec 增"replay 字节一致"场景 |
| RunStep 无 ErrorCode；error_code 仅在 `ToolResult.Metadata`（tool_batch_executor.go:45,59），runner 构建 step 时丢弃 | 复核 types.go:162-180 与批执行器属实 | Task 1 文件清单纳入 types.go/runner.go 载体改动；design Decision 11 增载体段；reflection spec 声明载体来源；Task 8 声明消费 Task 1 交付 |
| 归档读接线无 RED 锁定：`messagesThrough`（durable_memory_pipeline.go:470-485）可能仍走活跃读 | 复核属实（现走 `dreamMessageRangeReader`/`ListActiveThrough`） | Task 5 增 2 条 `DurableWindowWiringTest` RED + 文件清单含 :470-485；spec 场景强化为 worker 级断言 |

Should Improve 处理：Decision 10 扩至全部倾倒点（:426-428/:599-601/:617-618）并增 `NoModelConsolidateTest`；Task 9 改为包级 `slog.SetDefault` 捕获（零依赖注入改动）；Task 6 增 `DedupPolicyScopeTest` 与阈值边界 `shouldAcceptExactThresholdValues`；Decision 3 澄清竞态=锁读观察到 running，0 行分支为无锁实现的防御兜底（spec 两场景分列）；Decision 5 声明不复用 `LatestCompletedThrough`、"最近"=MAX(id)、保留乱序影子语义。

### Design review round 2 — FAIL（1 Must Fix，已回写）

- Must Fix：round 1 修复自身引入新事实错误——`summarizeDurableText` 有第 4 个调用方 `consolidation_projection.go:157`，Task 6 的"grep 为空"DoD 不可满足。主会话核实 5 处全集（4 调用 + 定义）：`consolidation_projection.go:157` 为归并产物兜底（输入非原始对话），**保留**；`pipeline:427/:600/:617` 删除。Decision 10 改写为站点全集表 + 两退避通道澄清；Task 6 DoD grep 期望改为"恰剩 2 行"；GREEN 移除无测试文件的 `./cmd/worker`。
- Should Improve 一并处理：Task 1 增 `shouldWriteEmptyErrorCodeWhenStepLacksCode`（字节比对列的确定性空串）；proposal Impact 的触发白名单归属更正为 `agentruntime/assembly.go`。
- 轮次说明：propose skill 上限为 2 轮修订；round 2 的唯一 Must Fix 为窄域可机械验证修复，已回写完毕，追加一次**收敛验证审查**（非开放式新一轮修订）以取得 PASS 门禁证据。

### Design review 收敛验证 — PASS

- 5 项定点核验全部通过：Decision 10 站点全集表与代码逐一吻合（保留 `:157` 有依据：输入为归并产物且无模型场景不可达）；Task 6 grep DoD 可满足（恰剩 2 行）；两退避通道分别锁定；round 2 四项 Should Improve 全部落实；无新回归。
- 剩余 Should Improve 1 条（引用润色）已当场修复：线性退避延迟位置补引 `durable_memory_pipeline.go:290`。
- `design_review_passed: true`，`design_review_rounds: 3`（2 轮修订 + 1 次收敛验证）。

## 2026-08-28 apply

### Skills Loaded: vsdd-workflow-router, vsdd-workflow-apply, vsdd-workflow-reverse-sync, vsdd-workflow-git-discipline, vsdd-workflow-implement-task, vsdd-workflow-review-implementation, test-driven-development, subagent-driven-development

- 分支门控：`refactor/memory-usecase-cleanup`（非 main/master，直接放行）。
- Commit 策略：`vsdd-workflow.local.yaml` → `auto_commit: true`，guidance="Use English conventional-commit style messages (e.g. refactor(memory): ...). Never use Chinese in commit messages."；已写入本 change `.vsdd-state.yaml.runtime`（不入库，遵循既往惯例）。
- Propose 工件提交：`72bd98b docs(memory): propose memory evidence and reflection hardening change`（8 files）。`doc/` 目录被 .gitignore 忽略（`doc/*`），评审报告 `doc/memory-reflection-evidence-plan-v2.md` 保留本地。
- 工作区校验：tracked 工作区干净。
- `base_commit`（apply 起点）= 72bd98b。
- 编排说明：subagent-driven-development 的 ledger 职责由本 change 的 `log.md` + `.vsdd-state.yaml`（入库可恢复）承担，不另建 `.superpowers` scratch workspace（与既往 change 一致）。

### HARD-GATE — 通过（用户预授权）

- 用户在原始请求中明确："按照这个计划去跑 vsdd 的全部流程，自己去写 apply，自己 review，就可以了" —— 全流程（propose→design review→apply→review→verify）已获显式预授权，`user_confirm_apply: true`。
- 复杂度 🔴 standard、artifacts 清单（proposal/specs×2/design/tasks/log/state）、待澄清项=无，一并输出给用户报告。

### Reverse Sync — Task 1 实现者发现环境事实冲突（2026-08-28）

- 冲突点：Task 1 派发说明的环境节声称仅 `internal/infrastructure/mysql` 在 Windows 上不可编译（syscall.Flock），要求 `go test ./internal/runtime/agent ./internal/runtime/compaction ./internal/runtime/agentruntime` 原生执行。代码事实：`internal/runtime/toolruntime/filesystem_path.go:100,106` 直接使用 `syscall.Flock/LOCK_EX/LOCK_UN`（commit 7e624eb "refactor(go): split runtime and tool modules" 引入，无构建标签），导致 toolruntime 及其上游 `agent`/`agentruntime` 包在 Windows 上同样无法编译，原生 `go test` 无法执行（已实测；本机无 WSL 分发、无 Docker）。
- 处理：实现者未修改任何装运代码。测试执行改用 `go test -overlay`，将 `filesystem_path.go` 映射为等价的 Windows 可编译副本（仅两处 Flock 调用替换为进程内等价实现；`acquirePathLock` 路径不被这三个包的任何测试触达）。装运构建门禁仍为无 overlay 的 `GOOS=linux go build ./...`。
- 待回写：主会话需在 tasks.md 环境说明（或后续任务派发模板）更正"仅 mysql 包不可在 Windows 编译"，并决定是否单独立项为 toolruntime 增加 Windows flock 兼容层。`reverse_sync_required: true` 直至回写完成。

### Reverse Sync — Task 1 环境事实回写完成（主会话，2026-08-28）

- 回写点：`design.md` Risks 表 Windows 行 + Test Strategy 段；`tasks.md` 新增「环境与验证门禁（全局）」节（后续任务派发模板以此为准）。
- 裁定：测试期 `go test -overlay` 垫片方案**批准**（仓库外、不提交、不影响装运门禁；`acquirePathLock` 不被触达）；Windows flock 兼容层**不在本 change 范围**，如需要另立 change。
- 实现者 concern 2 裁定（**接受**）：tool_result 步骤在 `result.Steps` 中携带 Error 文本属 Task 1 错误状态持久化语义，且是 Task 8 提示词含错误文本的前置条件；全包测试保持绿。
- 实现者 concern 3 裁定（**接受**）：富化成功行写四键（`is_error:false, error_code:""`）——任务 RED 只钉死失败四键与未富化两键；均匀键集使「富化行恒四键」规则单一、字节确定性可审计，下游三态解析只看 is_error。
- `reverse_sync_required: false`；known_issues 标记 RESOLVED。

### Task 1 — 实现者返回：DONE_WITH_CONCERNS → 三关切已裁定，进入双审

- 证据核验：报告 `task-1-report.md` 含 3 周期 RED（编译失败→断言失败逐层转绿）、12 场景全绿（-count=1）、`GOOS=linux go build ./...` exit 0、DoD grep 命中 `message_sink.go:108`。

### Review Evidence Task 1 — spec-compliance reviewer

- Verdict: **PASS**（Must Fix 0）。12/12 RED 场景逐一核对：名称逐字保留、mock 输入与断言与 tasks.md 措辞匹配（含干扰项：同 ToolCallID 的 tool_call 步骤、同名工具多批）。
- 规格符合性：未富化行走改动前字面 2 键 marshal（`IsError != nil` 守卫，字节兼容）；富化仅精确 ToolCallID + `StepTypeToolResult`；回放确定性经真实 sink `bytes.Equal`；`ToChat` 不泄漏；`llm.ChatMessage` 零改动（编译期锁）。范围 = 4 个清单内生产文件；go.mod/go.sum 0 行；无迁移。
- 构建门禁：`GOOS=linux go build ./...` exit 0（评审独立复跑）；compaction 泄漏锁测试原生复跑通过。
- Should Improve 4 条（非阻塞）：① 未富化行 2 键形状缺独立行级断言（当前为构造正确+守卫覆盖）；② 场景 12 落在 compaction 包顶层函数（已裁定偏差 §4）；③ 回放测试行数断言偏松；④ 内联 issue 短路站点无 ErrorCode（白名单外停机原因，影响有界，供 Task 8 评审参考）。

### Review Evidence Task 1 — code-quality reviewer

- Verdict: **PASS**（Must Fix 0）。独立复跑全套：agent/agentruntime（overlay）+ compaction（原生）全绿；`GOOS=linux go vet` 三包 exit 0（原生 vet 因已知 flock 环境受阻，已用 GOOS=linux vet 补偿）。
- 正确性核验：三态 `*bool` 每 entry 独立指针；`toolResultErrorCode` nil/非字符串安全；步骤类型过滤存在且正确（干扰项测试覆盖）；json.Marshal map 键排序 → 字节确定；checkpoint JSON 往返保留 Error/ErrorCode；`error_code` 元数据仅批执行器两个生产者写入，无工具泄漏面。
- 副作用爆炸半径审计：事件流副本无双重上报；`reflectionSignal` denied 启发式开始纳入 Error 文本（判定更准，属已裁定偏差半径）；checkpoint steps 携带 Error/ErrorCode 为回放确定性所必需。
- Should Improve 4 条（非阻塞）：① **混合版本恢复边界**（值得跟踪）：旧二进制持久化的 2 键行，新二进制恢复重放再富化为 4 键 → `verifyTranscriptPayload` 字节冲突 → 该批 `PersistEntries` 中止，runner 优雅降级（error step + cursor 前进，无崩溃）——记入 known_issues，verify 阶段裁定是否需修复；② 成功行四键断言可收紧（偏差 §3 不变式未全锁）；③ 缺 `item.err`（无元数据）端到端步骤用例；④ 防空洞守卫用 `metadataMap` 解析更贴合文件风格。
