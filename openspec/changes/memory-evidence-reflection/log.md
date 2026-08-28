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

### Task 2 — 实现者返回：DONE（无偏差阻断）

- 交付：`ListThroughIncludingArchived`（domain 接口 + mysql 实现，`owner_id AND conversation_id AND id > ? AND id <= ?` + `ORDER BY id ASC`，无 `archived_at` 条件）；纯增量 +473 行；活跃读零 diff。
- 偏差裁定（接受）：方法名以 design.md Decision 2 的 `ListThroughIncludingArchived` 为准（派发示例名非规范）。
- 环境补充（已回写 tasks.md 全局节）：mysql 测试二进制另触碰 `workspace_usecase/git.go`（Flock）与 `cleanup.go:144`（`syscall.Kill`）两个 Windows 阻断点；验证用 `GOOS=linux go test -c` 交叉编译。

### Review Evidence Task 2 — spec-compliance reviewer

- Verdict: **PASS**（Must Fix 0）。6/6 场景逐字存在且断言等于或强于 tasks.md 措辞（精确 id 集合 + 精确 SQL 谓词串 + 参数序）；`shouldLeaveActiveReadUnchanged` 钉死两活跃方法完整条件串；SQL 侧无 `archived_at`（大小写不敏感负断言锁定）；纯增量（474+ / 0-）；范围 = 4 个清单文件，无迁移、go.mod 零动。
- 门禁复跑：`GOOS=linux go build` exit 0；domain 测试原生绿；mysql 测试 `GOOS=linux go test -c` exit 0（原生编译受阻于已知 flock，符合预期）。
- Should Improve 3 条（非阻塞）：① 升序场景注释措辞（ordering 由 SQL 断言证明）；② 集成子测试本机 skip（DSN 缺省，有证据）；③ tasks.md 环境补充行属 apply 期回写槽位（仅备注）。

### Review Evidence Task 2 — code-quality reviewer

- Verdict: **PASS**（Must Fix 0）。gorm 占位符/参数序一致、裸错误风格与兄弟方法同；`domain.ImmutableModel` 无 `gorm.DeletedAt` → 无隐式软删过滤（新法真正返回归档行）；假驱动忽略 WHERE/ORDER BY 但测试诚实（记录 SQL + 罐头行镜像，既有包模式）；`archived_at` 意外出现会被大小写不敏感断言捕获（无假信心）；集成子测试 skip 逻辑与既有集成测试字节一致，且真在 id 范围内播种异 owner/异会话行验证过滤。
- 门禁复跑：`GOOS=linux` build/vet 全 0；原生 vet 受阻于已知环境事实（GOOS=linux vet 为接受替代）。
- Should Improve 3 条（非阻塞）：① `repository_test.go:74` 契约桩未转发 `conversationID`（编译钉为主，次要）；② 升序场景注释同上；③ 退化窗口（afterID==throughID / throughID==0）未测——SQL 语义下平凡安全，可选。

### Task 3 — 实现者返回：DONE（无实质偏差）

- 交付：`evidence_renderer.go`（301 行，纯渲染器：`Render([]conversation.Message) []EvidenceUnit`，text/exchange/orphan_output 三类单元，三态 `EvidenceErrorState`，复用未改动的 `redactDurableSecrets`@pipeline:712，精确 tool_call_id 配对，键序无关的同参失败连击+恢复检测，防御式 metadata 解析）+ 测试（353 行，7 场景全绿）。
- 附加细节裁定（接受）：排除场景加播 `role=system` 行——spec 原文要求排除 "developer/system injected content"，实现逐字落实。

### Review Evidence Task 3 — spec-compliance reviewer

- Verdict: **PASS**（Must Fix 0）。7/7 场景逐字存在且断言等于或强于措辞；三条 ASSERT 均有专用锁（`assertNoRawSecret` 扫 Content/Arguments/Output；跨 run 同名工具双向不污染；`assertTriState` + unknown≠success≠failure 显式断言）。
- 集成契约：`durable_memory_pipeline.go` 零 diff（redact 复用）；解析缺键/坏 JSON→unknown 无 panic；渲染器纯函数（仅 json/sort/strings/conversation 四导入）；签名与 Task 2 读路径返回类型吻合。范围 = 2 新文件 654 行，go.mod 零动。
- 门禁复跑：`GOOS=linux build` exit 0；`-run EvidenceRenderer -count=1` 7/7 绿；全包回归绿。
- Should Improve 3 条（非阻塞）：① 坏 JSON metadata 路径无直接测试（可于 Task 5 补）；② 场景 6 四名三种（注释已自证，外观项）；③ `.runtime` 文件提交时勿扫入（既有惯例）。

### Review Evidence Task 3 — code-quality reviewer

- Verdict: **PASS**（Must Fix 0）。逐行核验：坏 JSON/错型值→unknown 安全降级；指纹 `toolName+"\x00"+canonical(args)`（UseNumber+Marshal，键序确定，坏 JSON 回落原串不 panic）；连击按指纹独立成 map（异参成功不重置他人连击）；Recovered 仅在同指纹 ≥1 失败后出现成功时置位并回填；三遍配对（索引调用→绑定输出→发射）不依赖 id 序，输出早于调用仍可配对；重复 tool_call_id 首调用胜出、重复输出落 orphan 不丢弃；脱敏先于入单元（4 站点全覆盖，Arguments 脱敏在指纹化之前故连击键稳定）；`sort.SliceStable` + 单遍发射 → 严格升序。
- 变异探针三项全捕获（默认 success、按 tool_name 配对、漏脱敏 arguments）。
- 门禁复跑：build/全包回归/vet/gofmt 全绿（gofmt 标记的 5 个既有文件非本任务触碰）。
- Should Improve 2 条（非阻塞）：① 4 个边界子测试可补（输出 id<调用 id、重复 tool_call_id、orphan 含密、异参成功穿插连击中）——代码已正确处理，仅测试未锁；② orphan 按工具名归组连击的语义建议一行注释。

### Task 4 — 实现者返回：DONE（事故已恢复）

- 交付：会话级去抖调度替换按边界建任务——`ExtractionJobRepository` 增 `LatestDurableJob`/`RefreshPendingBoundary`/`LatestCompletedDurableThrough`；事务内 `FOR UPDATE`（conversation+source+durable、`ORDER BY id DESC LIMIT 1`）→ 条件刷新 / 单 successor / 新建，唯一键冲突重读；键格式 `durable:<o>:<c>:initial` 与 `after-job:<id>`；Redis `durable:pending:` 突发键移除（grep 空）；`previousBoundary` 200 行扫描改 MAX(id) 定向查询（影子规则保留）；白名单 4 常量显式化。
- 事故披露（已恢复，主会话复验范围干净）：实现者误跑 `go fmt` 波及 59 个范围外文件（仅行尾），`git checkout` 恢复，最终工作区 = 8 个 Task 4 文件。
- 披露偏差（接受）：场景 9 断言 `boundary==500` 而非字面 ">500"——窗口读 `(500, 600]` 严格在 500 之后，语义等价不弱化。

### Review Evidence Task 4 — spec-compliance reviewer

- Verdict: **PASS**（Must Fix 0）。9/9 场景逐字存在且断言匹配（含伴生测试：legacy 行识别、影子规则、idle 忽略）；ASSERT 全锁定（创建恰 1 次发布 `AvailableAt=DueAt`、刷新 0 次；`durable:pending` 全仓空；`ListByStatus` 生产调用归零；FOR UPDATE 精确 SQL 被 sqlmock 钉死）；DoD 额外项：旧格式行经会话查询可识别（fake+集成双测）；键格式仅 4 个生产站点；既有唯一索引 `uq_memory_extraction_idempotency` 复用，无新迁移。
- 设计符合：Decision 3 事务形状与"竞态=锁读见 running→successor、0 行为防御"逐字落实；Decision 5 MAX(id)+影子、`LatestCompletedThrough` 零 diff（文件纯增量）。
- 门禁复跑：build exit 0；memory_usecase 10/10 子测试绿；domain/memory 绿；agentruntime overlay 重建后白名单 2/2 + 全包绿；mysql overlay 扩展后 sqlmock 7/7 + 集成 DSN-skip；四包 vet 干净。
- Should Improve 4 条（非阻塞）：① `ListByStatus` 零调用残留（后续清理候选）；② `NewDurableMemoryTrigger` 保留未用 `redisClient` 形参（签名稳定，后续清理）；③ 队列发布错误被吞（2s 轮询兜底，建议加 warn 日志）；④ `allStopReasons()` 手工维护清单受语言限制（注释约定）。

### Review Evidence Task 4 — code-quality reviewer

- Verdict: **PASS**（Must Fix 0），并发专项无缺陷。单事务覆盖锁读→分支→刷新/插入→冲突重读；发布在提交之后（回滚不泄漏唤醒）；FOR UPDATE 锁集被 `idx_conversation_id` 有界化；0 行回退为设计钦定防御且双路径有测；唯一键冲突仅吞重复错误、其余原样返回（1062 有真例测试）；`roundNumber` 忽略语义未变；3 处 SQL 修正为终态非残留（`through_message_id NOT NULL DEFAULT 0` 佐证去 COALESCE 正确）；sqlmock 7/7 `ExpectationsWereMet`，UPDATE 参数序 `-count=5` 稳定。
- Should Improve 5 条（非阻塞）：① 刷新非前向单调——design Decision 3 钦定形状，非偏差；可选 `GREATEST` 变体强化（注意朴素 `< ?` 守卫会误入 successor 路径，不可用）；② 发布错误无日志；③/④ 同上的清理候选；⑤ 注释"uses idx_conversation_id"表述为优化器选择非保证。
- 记录待办（verify 阶段裁定）：发布错误 warn 日志与 `GREATEST` 强化均列为可选改进，不阻塞当前任务。

### Task 5 — 实现者返回：DONE（4 项披露偏差）

- 交付：`evidence_chunker.go`（120k 上限、单元边界切块、超大输出 `part_index/part_count` 无损切片、相邻块恰 2 单元重叠）；extract 段重写为 渲染→分块→逐块提取→增量 `result_json`；`messagesThrough` 优先 `dreamMessageArchiveRangeReader`（管线内声明，domain 接口零改动）、未实现回退活跃读；提取侧倾倒点移除（无模型即报错，线性退避）；死代码 `DurableStage1Result`/旧 `extract`/`safeDurableSlug` 删除（全仓零引用）。
- 偏差裁定：a) 重叠单元不计入新单元上限预算（Decision 6 分列两参数，避免前沿停摆）——接受；b) `outcome` 增 `extracted` 标记——接受（归档前工件同步，已回写 design Decision 8）；c) 空/影子窗口写新 schema——接受（旧非空负载不可变有回归测试锁定）；d) 归并侧 summarize 站点 :766/:783 保留给 Task 6（grep 4 行=定义+投影+2 站点，与 Decision 10 站点表及 Task 6 DoD「恰剩 2 行」算术一致）——接受。

### Review Evidence Task 5 — spec-compliance reviewer

- Verdict: **PASS**（Must Fix 0）。10/10 场景逐字存在、断言匹配（线性退避通道显式区分、调用次数精确钉死、泛型 map 解码钉 `chunks/merge/outcome` 键、缺字段逐个拒绝、分块只操作 EvidenceUnit 不触碰 Message）；Design 8 崩溃安全逐点核验（至多丢在途块；续跑新鲜读取；空窗/影子窗 0 调用 `no_output`）；Design 10 提取侧无倾倒残留、归并侧站点完整；范围 = 5 文件。
- Should Improve 4 条：① 无模型分支测试归 Task 6 落地（`shouldFailWithoutModelInsteadOfDumpingText`，须断言线性通道）；② **窗口起点移动时的续跑索引不健全**（见下裁定）；③ outcome 枚举归档前同步工件（已回写）；④ part_index 单调断言可选强化。

### Review Evidence Task 5 — code-quality reviewer

- Verdict: **PASS**（Must Fix 0）。增量持久化原子单行写 + 写入路径校验；续跑每轮 `ClaimByID` 新鲜读取；退避/封顶/双通道隔离复验；可选读接口恰一次调用与回退路径字节级保留旧形状；脱敏先于分块（切片只作用于已脱敏文本，提示词组装二次脱敏）；分块器确定性无 map 序依赖。
- Should Improve 3 条（两项升级处理）：① **SI-1 续跑索引**：与规格审 ② 同源——successor 先跑、旧任务后完成 → 重试窗口收缩 → 新计划 index 0 映射不同证据，陈旧块候选被保留且新窗口首部单元静默漏提取；② **SI-2 碎片爆炸**：单元外壳本身 ≥120k 时 `budget` 夹到 1 → `partCount=字节数` → 每字节 1 次模型调用；③ 字节切片可能切断多字节 rune（仅限模型输入，可接受，记录在案）。

### Task 5 修复轮（主会话裁定：两项 SI 升级为即改）

- 理由：两项均为生产可达的正确性缺口（静默证据丢失 + 无界模型调用），修复面窄且实现者上下文尚热，推迟到 verify 后成本更高。
- 派发：原 Task 5 实现者续跑，TDD 增补——① result schema 记录 `window_after/window_through`，resume 窗口不符则作废部分块重提取（新场景 `shouldDiscardPartialChunksWhenWindowShrinksBetweenAttempts` + 防过度丢弃守卫）；② 外壳超限单元按整上限切片，碎片数 O(payload/cap)（新场景 `shouldNotExplodeWhenUnitShellExceedsCap`）。design Decision 8 已同步工件。

### Task 5 修复轮 — 双增量复审均 PASS（2026-08-28）

- 修复 1（续跑索引健全）：`window_after/window_through` 标记在首次逐块持久化之前写入；resume 在**任何模型调用之前**校验（`durable_memory_pipeline.go:366`，双字段相等判定，双向窗口移动均覆盖）；`(0,0)` 永不通过（`ThroughMessageID>0` 前置保证）；陈旧部分块作废即从头重提取，AttemptCount/退避不受影响。新场景 `shouldDiscardPartialChunksWhenWindowShrinksBetweenAttempts` 复现原失效模式（3 次调用、陈旧候选消失、窗口内证据正确）；防过度丢弃由 `shouldPersistChunkCandidatesIncrementallyAndSkipOnRetry`（已增 (0,3] 标记断言）守卫。
- 修复 2（碎片爆炸守卫）：`evidence_chunker.go:165-171` 外壳超限时 `budget=maxBytes`，碎片数 O(payload/cap)（300000→3 片）；空负载外壳超限早返回整单元；`part_count` 均匀、拼接无损；过度声明的 doc 注释已更正为含外壳溢出例外。新场景 `shouldNotExplodeWhenUnitShellExceedsCap` 通过。
- 工件同步：design Decision 8 已回写 `outcome` 枚举与窗口标记作废规则；报告增 Fix round 段 + 偏差 6。
- 门禁复跑：`GOOS=linux build`/`vet` exit 0；全包 `-count=1` ok；8 个候选提取子测试全绿。
- 剩余 Should Improve（非阻塞，记录）：① 缺窗口"扩张"方向专项测试（由相等语义+收缩场景覆盖）；② 作废后旧部分块在下次成功持久化前暂存 `result_json`（无害，每次重试重校验）；③ 刀锋带宽 `budget=1`（外壳恰低于 `cap-markerMax` 时）理论仍退化——需元数据调至距上限数字节内，极低概率，记录备查。
- `reverse_sync_required: false`，无阻断。进入关闭。

### Task 6 — 实现者返回：DONE（4 项披露偏差，3 项已裁定接受）

- 交付：候选门禁（`gateExtractionCandidates` 纯函数，`>=` 阈值、NaN/Inf 显式拒、脱敏后非空、拒因入 `result_json.rejections`）；写接线（`extraction_write_adapter.go`，键 `extraction:<job>:<idx>`，source=extraction）；`SQLMemoryWriter` 仅 extraction 分支算内容哈希（`sha256(type+"\n"+normalize)`，normalize=trim+`strings.Fields` 折叠）；归并侧两个 `summarizeDurableText` 兜底改错（grep 恰剩 2 行达成）；`cmd/worker/main.go` 注入 `WithExtractionWrites`。
- 偏差裁定：a) `bootstrap/app.go` 零改动——核实属实（app.go 只建 trigger 与 enqueue-only 管线；`NewDurableMemoryWorker` 唯建于 cmd/worker/main.go:233）；b) 场景 10/12 为既有行为的接线/回归锁——核实有牙（断言具体可观测量，回归即失败）；c) extraction 分支无条件覆盖请求侧去重键——spec 钦定语义；d) 提取行 archival+会话域、候选溯源入 metadata_json——披露且无 spec 冲突。

### Review Evidence Task 6 — spec-compliance reviewer

- Verdict: **PASS**（Must Fix 0）。12/12 场景逐字存在且断言匹配（`>=` 边界、64-hex 正则、键逐值、双通道 DueAt 容错不重叠）；ASSERT 全锁定（哈希仅 extraction 分支@:278、唯一索引 `uq_memories_owner_deduplication_key` varchar(191) 容 64 字符、grep 2 行、门禁为可复用纯函数供 Task 7）；范围 = 9 文件，bootstrap/app.go 零 diff 与偏差 a 一致。
- Should Improve 2 条（非阻塞）：① `result_json.rejections` 持久化内容无端到端断言；② 报告行号漂移（外观）。

### Review Evidence Task 6 — code-quality reviewer（首轮）

- Verdict: **FAIL（1 Must Fix）**。MF-1：入队失败静默丢候选——`finalizeExtractionOutcome` 先置终端 outcome（:398）→ result_json 带终端态落盘（:400/:335-344）→ 重试被 :368 条件挡在门禁+入队块外 → 任务以 0 个 write job 完成，候选永久丢失（窗口起点保证后续任务不会重提取）；部分入队失败更糟（k..n-1 丢失）。与 :404-407 注释及报告 ASSERT #5 自相矛盾；`fakeWriteJobRepo.failCreate` 存在但无测试使用。
- 核验确认：主会话复读 :330-429 控制流，缺陷成立。
- Should Improve 3 条：① 缺失败重试测试（随 MF-1 补）；② phase2 通道无尝试上限（既有机制，本改动使"无模型"持续失败转入该通道后无限重试——记录，不属本任务范围）；③ 拒因记录未脱敏标题（理论面，模型只见脱敏证据）。

### Task 6 修复轮（主会话裁定：MF-1 即改）

- 派发原实现者：终端 outcome 改为入队成功后才落盘（拆分 `finalizeExtractionOutcome`）；影子窗 no_output 直置分支保留；入队失败后 result_json 保留块而 outcome 为空 → 重试重新门禁（纯函数幂等）+ 重入队（唯一键保恰一次）；终端 updateJob 失败路径仍需安全（重试见 outcome 直接完成、不重入队）。新场景 `shouldReenqueueCandidatesAfterTransientEnqueueFailure` + 影子窗守卫。
- phase2 无上限重试记入 known_issues（既有机制，另议）。

### Task 6 修复轮 — MF-1 增量复审 PASS（2026-08-28）

- 修复核验：`finalizeExtractionOutcome` 拆为 `gateExtractionResult`（仅门禁+拒因，不碰 outcome）；`extractChunks` 不再置 `extracted`（空窗 `no_output` 保留）；Handle 顺序 = 空 outcome 门禁 → 空 outcome 落盘 → 入队 → 成功后才置终端态并重新落盘；延迟失败处理器在入队失败时持久化的是"块在、outcome 空"的可续跑状态。
- 重试恰一次机制实证：键确定性（`extraction:<job>:<idx>` + 排序展平纯门禁）+ 生产 `Create` 的 `OnConflict(owner_id, idempotency_key) DO NOTHING`+重读（memory_schema_repos.go:60-66，唯一索引 `uq_memory_write_jobs_owner_idempotency`）。
- 不变式 4 机制更正（评审指出）：终端 updateJob 失败时 outcome 并未落盘（延迟处理器因内存态 completed 早退），重试见"块在+空 outcome"重入 → 0 模型调用重门禁重入队 → 唯一键去重保恰一次。安全性来自唯一键而非块跳过，净效果一致。
- 影子窗分支完好（门禁跳过、入队 no-op、:418 守卫保住 outcome），新场景 `shouldKeepNoOutputOutcomeForShadowWindow` 锁定。
- 回归：15/15 场景绿（12 指令场景逐字在列）；`GOOS=linux` build/vet 0；grep 恰 2 行；新测试确能捕获原缺陷（旧代码在失败入队后持久化 "extracted"，新断言直接 Fatalf）。
- 剩余 Should Improve（非阻塞，记录）：① 部分入队后重试的直接测试缺口（去重已由生产代码+假件双验证）；② 低概率合流：入队失败 + 兄弟任务推进边界 → 窗口作废重提取时旧键可能保留旧窗口行（双低概率事件叠加，影响受内容哈希去重封顶；如需绝对严密可将窗口代际折入写键）；③ 影子遗留写任务语义为既有行为。
- `reverse_sync_required: false`。进入关闭。

### Task 7 — 实现者返回：DONE（5 项披露偏差）

- 交付：多块归并（`mergeExtractionChunks`/`mergeChunkCandidates`/`buildMergePrompt`/`mergeChunkSummary`，N>1 时恰 1 次归并调用、与提取同一 chat seam 与模型配置、归并槽入队前落盘、归并输出过同一门禁）；`gateCandidates` 判据 `len(r.Merge)>0` → `r.Merge!=nil`（区分"归并未跑"与"归并产出空裁决"）；Handle 对 `no_output` 终态跳过归并（outcome 门禁，:447 区）。
- 偏差披露：a) 命名沿用 Tasks 1–6 惯例（`TestMergePass`/`TestConsolidationInput` 子测试承载指令场景名）；b) 2 个指令场景（单块跳过归并、归并输入含 extraction 源）RED 即过——为既有行为的回归锁；c) 2 个伴随场景（空归并裁决判 no_output、入队失败重试不重跑归并）；d) 影子窗 no_output 亦跳过归并（任务指令场景 6 语义覆盖）；e) 2 个既有测试随行为更新（`candidate_extraction_test` 多块重试 3→4 次调用 + 归并提示词断言；`no_model_consolidate_test` 空摘要测试改种 1 条过门禁候选、write jobs 0→1——因 no_output 不再进归并）。
- 报告：task-7-report.md；评审包 /tmp/task7-review.diff（841 行，5 文件：+731/-17）。

### Review Evidence Task 7 — spec-compliance reviewer

- Verdict: **PASS**（Must Fix 0）。6/6 指令场景逐字存在且断言匹配（`merge_pass_test.go:95/197/233/339`、`consolidation_input_test.go:32/73`）：恰 1 次归并调用（3 提取+1 归并=4）、单块 0 归并、归并失败保留全部块候选且重试 0 提取仅重发归并（无 `phase2:` 前缀）、低置信归并输出被门禁拒、4 条输入来源标记正确（extraction→rollout / ad_hoc→ad_hoc）、no_output 不入队且归并锁 0 次获取。
- ASSERT 双锁定：提取（:664）与归并（:709）调用点字节级同源（`w.cfg.Provider`/`w.cfg.Model`），无新配置项（`DurableMemoryConfig`/`NewDurableMemoryConfig`/`DurableMemoryWorker` 零 diff，逐调用 provider/model 断言锁定）；归并失败重试走线性 Phase-1 退避（`DueAt=now+(AttemptCount+1)min`、`Phase2AttemptCount` 不动、无 `phase2:` 前缀泄漏），与 phase2 指数通道（:451/:1160/:1141）语义区分。
- Task 6 顺序不变式在归并槽下保持：槽入队前落盘（:687-701）、门禁不置 outcome（:583-587）、入队（:418）成功后才置终端态并重落盘（:421-429）；伴随场景 `shouldNotReRunMergeAfterEnqueueFailure` 锁定入队失败重试 0 模型调用。
- 范围核验：恰 5 文件 + openspec 工件；`consolidation_projection.go` 零 diff；`go.mod` 零 diff；`grep -rn summarizeDurableText internal/` 恰 2 行。
- 偏差裁定：a) 命名接受；b) 接受——评审以 `-overlay` 变异测试证实两锁非空洞（守卫改 `<1` → 单块测试失败；`ListBySources` 删 extraction 源 → 4 条变 2 条失败）；c) 接受（两伴随场景均在 RED 失败，驱动了 `!=nil` 修复与槽持久化锁定）；d) 接受（归并输入为 owner 域全量行 :1009，跳过 no_output 触发不丢证据；phase2 重试环 :289-308 不受门禁；legacy 空 outcome 仍进归并——`TestNoModelConsolidate` 等既有测试绿证明）；e) 接受（变异证实更新后断言仍锁原不变式：删块跳过 → 5 次调用失败；空摘要兜底回归 → `:115` "summary" 断言失败）。
- 门禁复跑：`GOOS=linux go build ./...` exit 0；`go test ./internal/application/memory_usecase -count=1` ok；`-run 'TestMergePass|TestConsolidationInput' -v` 6+2 场景全绿；vet 0。
- Should Improve（非阻塞）：① 两处 `.vsdd-state.yaml.runtime` 未跟踪工件建议 gitignore（含无关的 sql-memory-es-hybrid 目录）；② 归并提示词候选逐值无上限（摘要限 1200 rune），随候选数线性增长——在 120k 窗口上限与去抖约束下可接受，观察项。

### Review Evidence Task 7 — code-quality reviewer

- Verdict: **PASS**（Must Fix 0）。
- 归并正确性：恰一次守卫三分句（:688 `Outcome!="" || len(chunks)<=1 || Merge!=nil`）；N=0 不可达（`extractChunks` 空窗先置 no_output :621）；空归并裁决被遵守且不回退块候选（`parseExtractionCandidates` 对 `{"candidates":[]}` 返回非 nil 空切片 :778、JSON 往返保形、`Merge!=nil` 守卫幂等）——由伴随场景 `shouldGateEmptyMergeOutputAsNoOutput` 锁定；提示词全脱敏链路（渲染器 :194/:211/:225 + 构建时再脱敏 :748，测试锁定无原秘密且 `[REDACTED]` 在场）；摘要 1200 rune 截断带 `"..."` 标记（:749-752）；解析复用 Task 5/6 严格路径（缺字段报错 :789-816）。
- 失败/重试：归并错误先于触槽返回（:691-693）、块候选完整、线性退避、无 `phase2:` 前缀；重试 0 提取仅重发归并；陈旧归并五路排查无缺陷（失败留 nil / 窗口移动整体替换清槽 :382 / 影子窗全新 result :371 / 入队失败重门禁存储槽 0 调用 / 归并后 updateJob 失败的槽为当前块合法归并、跳过即恰一次语义）；`AttemptCount` 每次 claim +1（:477），场景 3 锁定首次后 =1。
- 归并门禁：位于终态 updateJob（:436）与租约释放（:441）之后；`extracted`+0 accepted 不可能（:421-426 要求 accepted 非空）；legacy/终态存储结果 outcome 空仍进归并（两条既有测试绿）。饥荒边界（owner 全部任务 no_output 时 ad_hoc 行延迟归并）裁定 Should Improve——门禁为任务指令钦定（`shouldNotEnqueueConsolidationOnNoOutput`）、输入永不丢失（行持久、下次 extracted 全量重归并）、改动前 no_output 触发归并本系意外副产品；已记入 known_issues 待 archive。
- 既有测试更新强度不降：`candidate_extraction_test.go:215` 原断言全保留（部分块检查 :252-267、重试提示词 :278-281、终态 :287-303），3→4 + 归并提示词断言为增强（块 0 不重发现在双锁定：重发则 5 次调用）；`no_model_consolidate_test.go:90` 种子变更系必要（旧种子在新门禁下到不了归并、场景不可测），原意图断言逐字保留（"summary" :115、phase2 计数 :119、0 artifact :122），write jobs 0→1 同时锁定入队先于归并的顺序。
- 门禁复跑：`GOOS=linux go build ./...` exit 0；全包 `-count=1` ok；vet 0；8/8 场景名逐字在场全绿；5 个触碰文件 `gofmt -l` 干净。
- 透明披露：评审期间误跑 `go fmt` 波及 5 个未触碰文件（仅 CRLF），已 `git checkout` 复原并复核工作树与评审包字节一致。
- 主会话复核：工作树 5 文件 +731/-17 与评审包一致；报告偏差 #4 引用误述（"spec sentence" 实为 tasks.md 场景）已按评审意见修正。
- `reverse_sync_required: false`。双 PASS，Task 7 关闭。

### Task 7 关闭（2026-08-28）

- 门禁复跑（主会话）：`GOOS=linux go build ./...` exit 0；`go test ./internal/application/memory_usecase -count=1` ok 2.000s。
- 提交 `20cc5b4` feat(memory): merge multi-chunk extraction candidates before gating（9 文件 +926/-20；`.vsdd-state.yaml.runtime` 已先行 reset）。

### Task 8 — 实现者返回：DONE（4 项披露偏差）

- 交付：`reflection.go` 全轨迹信号扫描（分类：结构化 ErrorCode 优先→子串兜底，不覆盖优先级 schema>not-found>denied>generic；指纹 `tool_name+规范化参数+error_code`（键序无关重序列化，畸形/空→稳定 `{}`）；2+ 同指纹失败且其间无同指纹成功 → `repeated_no_progress`；选择规则：重复信号（最新）→ 类别优先级 → 最新 index）；12 步居中提示词窗口（参数经配对 tool_call 步按 ToolCallID 解析、与 content 共享 1200 尾截断上限、含 error_code/错误文本/后续同工具恢复）；防注入系统消息、`validInlineReflection`、`extractJSONContent`、`fixedReflectionFeedback`、触发指纹去重、用量/指标全部字节不变。
- 偏差披露：a) error_code→类别映射（schema: `invalid_json`/`invalid_arguments`；not-found: `missing_name`/`unknown_name`/`ambiguous_name`/`invalid_alias`，均溯源自 Task 1 交付的 normalizer/executor/runner 代码）；b) 选择规则；c) 「生产 tool_result 步从不携带 ArgumentsJSON，参数从配对 tool_call 步取」；d) 偶数窗口居中偏移取 size/2。
- 报告：task-8-report.md；评审包 /tmp/task8-review.diff（678 行，2 文件：+612/-18）。

### Review Evidence Task 8 — spec-compliance reviewer

- Verdict: **PASS**（Must Fix 0）。8/8 指令场景逐字在场全绿（`reflection_evidence_test.go:54/87/104/131/153/215/253/300`）+ 4 个额外锁定（并列选择、畸形/空参数同指纹、尾截断、防注入文案）；`SignalSchemaFailure` 首次赋值确认（常量原在 `domain/reflection/signal.go:10`，HEAD 前 domain 外零引用）。
- 偏差裁定：a) 接受、无杜撰——6 个码值全部有真实生产者（`tool_normalizer.go:121/125/106/138/159/157`、`runner.go:561` 防御回退）；b) 接受——确定性选择、严格 `>` 位置决胜、规范"最高优先级指纹而非首个错误"满足；c) 接受、硬核验属实——3 个生产 tool_result 构造点（`runner.go:597/:661/:780`）均不置 ArgumentsJSON，唯 `StepTypeToolCall` 步携带；按 ToolCallID 精确配对（非工具名）→ 同名多次调用正确区分；孤儿 tool_result → `"{}"` 无 panic；d) 接受——居中数学锁定（30 步信号@20 → 14..25，两端钳制）。
- MUST-NOT-CHANGE 全核验：策略门禁、触发指纹去重、防注入消息逐字节、验证/抽取/反馈函数、用量指标——仅以上下文行出现。
- RED 真实性：报告失败行号与新测试文件逐一对应，失效形态均匹配旧代码（首错即返、denied 被 notfound 覆盖、无指纹、6 步尾窗、无 arguments 键）。
- 门禁复跑（评审自建独立 overlay）：build/vet/定向 12 子测试/全包/agentruntime 全绿。范围：恰 2 文件 + 过程工件；go.mod 零触。
- Should Improve：① **上游接线缝**（非 Task 8 缺陷）：`runner.go:589-606` 拦截分支（既有、Task 1 未触）构造的 tool_result 步不带 ErrorCode → 被拦截的 `invalid_json` 等消息文本不匹配 schema 子串，生产降级为 generic tool_error；Task 8 规范正确且范围锁定禁改 runner，记为 Task 1 后续（一行修复候选：拦截点置 `ErrorCode=normalized.Issue.Code`，或扩 schema 子串）；② 测试 :163 字面量可换常量（外观）。
- 透明披露：评审误跑 `go fmt` 触及 2 文件（CRLF→LF），已字节级复原（md5 复核一致）。

### Review Evidence Task 8 — code-quality reviewer

- Verdict: **PASS**（Must Fix 0）。
- 优先目标裁定：① 成功重置语义健全——重置用 `tool+参数` 前缀匹配（:143-149）而非全指纹精确匹配 → 任意 error_code 的同参成功均可重置；NUL 分隔安全（json.Marshal 转义控制字符）；② 配对按 ToolCallID、全轨迹建表一次复用（窗口外亦可解析参数），测试用双 ID 区分同工具两次调用；③ 截断单一常量 `reflectionEvidenceCap=1200`（:22）经 `compactString`→`strutil.TruncateWithSuffixFlag` 尾截断、字节制、与既有 content 上限同单位同向，错误文本 500→1200 系规范钦定；中段 rune 切断由 json.Marshal 净化（合法 JSON 保持）；④ 触发去重交互可接受——去重键含绝对 StepIndex、选择对固定轨迹确定；增长连击选出不同信号再触发系特性且有 `MaxInlinePerRun` 上限。
- 扫描确定性：maps 仅作计数/查找，选择全部遍历有序 `failures` 切片；`chosen` 在 `repeatedAt` 非空时不可能零值。分类无覆盖：switch-on-ErrorCode 先返回 + 有序 if 链，双向测试覆盖。规范化：parse+重序列化、stdlib 键排序保证、`UseNumber` 保数值字面；乱序键测试真实（仅当规范化抹平键序才可能触发 repeated）。
- 窗口数学核验：偏移/钳制/恰好 size 条、无 off-by-one；提示词条目固定 8 键、json.Marshal 排序 → 稳定契约。
- 行为保持：diff 恰 4 个 hunk；既有函数与消息逐字节；全轨迹扫描用 `result.Steps`、空时回退 `recent`（直测路径），生产调用点（`runner.go:189/412`）传全轨迹切片。
- 门禁复跑：build/vet 0；定向 `-count=2` 12/12 确定；全包与 agentruntime 绿；两文件 gofmt 一致（CRLF 为全仓 22/24 文件共同惯例）。
- Should Improve（非阻塞）：① 与 spec 评审同一发现的上游接线缝（runner 拦截步无 ErrorCode → schema 分类降级；一行修复候选同上）；② `normalizeArgumentsJSON` 用 Decoder.Decode 忽略尾随杂散字符（稳定确定、外观）；③ 报告引用 `runner.go:561` 表述不精确（:561 为 InvalidAlias 回退、:596 无码步靠 "not available" 子串——分类结果仍正确）。
- 主会话复核：工作树 2 文件 +612/-18 与评审包一致；上游接线缝两审独立发现、裁定一致——Task 8 规范正确且范围锁定，缺口属 Task 1 载体接线，记入 known_issues（后续候选），不阻塞本任务。
- `reverse_sync_required: false`。双 PASS，Task 8 关闭。

### Task 8 关闭（2026-08-28）

- 门禁复跑（主会话）：`GOOS=linux go build ./...` exit 0；工作树与评审包一致。
- 提交 `c0662d6` feat(runtime): scan full tool trajectories for reflection signals（6 文件 +774/-21）。

### Task 9 — 实现者返回：DONE（无范围外偏差）

- 交付：`terminalReflectionContent`（reflection.go:95-117）四段结构（Root cause / Corrective action / Lesson / Applicability，空段跳过、全空条目丢弃、条目间空行）；`finalizeReflection`（:66-86）入队失败改为先 `slog.Warn`（owner_id/agent_id/run_id/error 结构化属性）再发 1 个尽力而为 AgentStep 事件（沿用 `execution.go:153/:433/:484` 的 StepTypeError 载荷模式，无新事件类型）；`emitRuntimeEvent`（support.go:15-20）本身上不抛错；运行不失败（函数 void、调用点 bare statement）；成功路径静默；waiting_human/paused 仍跳过；`runtimeCore`/Deps 零字段新增。
- 偏差披露：3 个指令场景（保持运行成功、成功静默、跳过两种 stop reason）RED 即过——为既有语义回归锁；其余为授权内的设计裁量（段标签措辞、1 个空段跳过额外子测试）。
- 报告：task-9-report.md；评审包 /tmp/task9-review.diff（344 行，2 文件：+280/-12）。

### Review Evidence Task 9 — spec-compliance reviewer

- Verdict: **PASS**（Must Fix 0）。diff 与工作树逐字节一致（反向应用校验通过）。6/6 场景逐字在场全绿（`reflection_test.go:87/135/159/184/205/226`）+ 1 个披露额外子测试（空段跳过 :113）。
- 回归锁裁定（3 个 RED 即过场景）全部有牙：去守卫 → 场景 6 失败（错误写手使"仅错误跳过"变异同被捕获）；成功路径加日志/事件 → 场景 5 失败；错误路径引入 panic/重试/短路 → 场景 4 失败（recover 守卫 + 恰 1 次调用断言）。
- 需求 3 溯源：四段稳定序（:99-110）、TrimSpace 守卫、全空丢弃（:111-113）、`"\n\n"` 连接（:116）、doc 注释准确（:89-94）。
- 需求 4 溯源：warn 先于事件（:78-83 → :84-85）、`emitRuntimeEvent` 不上抛（support.go:15-20）、恰 1 事件（消息串非测试代码仅现于 :78/:85）、载荷与 execution.go 模式同形、事件类型目录零 diff、Deps/runtimeCore 零字段（dependencies.go/runtime.go/context.go 均空）、结构化属性非格式化消息。
- MUST-NOT-CHANGE 全核验：幂等键 `reflection:run:%d`（write_adapters.go:38 零 diff）、请求字段集、五重守卫与顺序、证据 JSON 键、策略函数均未触。
- 门禁复跑（自建独立 overlay）：build/vet exit 0；定向 6+1 场景全绿；全包 4.819s 绿；agent 包回归 8.205s 绿；gofmt 干净。
- Should Improve：① 报告测试行号区间小误（外观）；② `.vsdd-state.yaml.runtime` 建议 gitignore（非本任务产物）；③ `"error"` 属性可用 `slog.Any(err)` 更惯用（非必需）。
- 透明披露：评审误跑 `go fmt` 波及 16 个无关文件（CRLF→LF），已 `git checkout` 全数复原并复核字节一致。

### Review Evidence Task 9 — code-quality reviewer

- Verdict: **PASS**（Must Fix 0）。RED 状态独立复现（以 HEAD 版 reflection.go + flock shim 组 overlay 重跑，输出与报告一致：4 真失败 + 3 锁）。
- 内容组装：四段独立条件句稳定序、标签风格与既有 `Corrective action:` 及 `reflection_usecase/conversion.go:108` 一致；全字段 TrimSpace、纯 `strings.Join` 构造无尾空白；部分条目发射其有段、全空条目丢弃（测试用仅含 Action 的空条目，Action 非四段之一——意图精确）。
- 可观测路径：先日志后事件；属性 int64 直传、蛇形键、错误不拼入消息、消息文本稳定可 grep；单分支恰一次；守卫全部早于日志代码（跳过的运行不可能产生警告）；调用点（:326/:388/:440）未变。
- 测试卫生：`capturePackageLogs` 经 `t.Cleanup` 还原 `slog.Default()`（包内唯一 SetDefault、无 t.Parallel → 全局替换安全）；事件捕获复用包标准件；无 flaky 构造；gofmt 干净。
- 门禁复跑：build/vet 0；定向绿；全包 5.071s 绿；agent 回归 7.557s 绿（自建独立 overlay）。
- Should Improve（非阻塞，记录）：① 双非空条目间 `"\n\n"` 连接无直接断言（两条目测试丢了一条）；② 仅 RootCause+Applicability 条目组合（规范场景 GIVEN）无专项子测试；③ `TerminalAsync=false` 守卫无测试锁（守卫在场且顺序正确，删除不会使任何测试失败）。
- 主会话裁决：3 条均为纯增量测试覆盖缺口、非缺陷；任务 DoD（6 场景绿 + 构建门禁）满足，按流程记录备查，不做修复轮（若后续需要可作独立补测试提交）。
- `reverse_sync_required: false`。双 PASS，Task 9 关闭。全部 9 个任务完成，进入 verify 阶段。
