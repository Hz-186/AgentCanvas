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

### HARD-GATE — 通过（用户预授权）

- 用户在原始请求中明确："按照这个计划去跑 vsdd 的全部流程，自己去写 apply，自己 review，就可以了" —— 全流程（propose→design review→apply→review→verify）已获显式预授权，`user_confirm_apply: true`。
- 复杂度 🔴 standard、artifacts 清单（proposal/specs×2/design/tasks/log/state）、待澄清项=无，一并输出给用户报告。
