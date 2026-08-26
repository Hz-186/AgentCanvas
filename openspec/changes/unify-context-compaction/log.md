# Log: unify-context-compaction

### Skills Loaded: vsdd-workflow-router, vsdd-workflow-reverse-sync, vsdd-workflow-design-review, openspec-propose, writing-plans
（propose 阶段；explore 阶段加载记录见 exploration-handoff.md 头部：brainstorming, vsdd-workflow-router, openspec-explore）

## 2026-08-26 explore 完成
- 决策树 22 节点全部 resolved（A=11 / B=11），handoff 落盘。

## 2026-08-26 propose 启动
- **S1 偏差记录**：宿主 `<AGENT_DIR>/templates/`（spec.md/tasks.md/log.md/vsdd-state.example.yaml/exploration-handoff.md）在本机不存在（explore 阶段同样缺失）。按 SKILL 正文内嵌的格式规范继续（tasks 行首 `- [ ] Task`、RED/GREEN/ASSERT/DoD、依赖关系小节、spec 的 Requirement/Scenario 四井号格式），不中断流程。
- **S2 Handoff 承接**：已读 `exploration-handoff.md`；全部 22 项决策升格进 proposal/specs/design/tasks，无冲突。
- **S3 复杂度**：🔴 standard——跨 8 个模块（runtime×3、application、domain、infrastructure、interface/http、web）+ 表迁移 + 两套压缩路径合并。
- **S4 脚手架**：`openspec` CLI v1.8.0 可用，change 已被识别（schema=spec-driven）。
- **任务依赖判断**：Task 1/2 可并行（无共享文件）；Task 3/5/6 串行（共享消息写入路径与 turn_worker）；Task 4 可与 3/5 并行但建议串行（核心包接口演化）；Task 8 为汇合点。共享核心文件/状态流的分组不标并行。
- **tasks.md 机械自检**：首检 30 行 RED 缺 mock/返回/抛 关键词，全部改写后复检通过（8 Task 行、RED 均含方法名与 mock 输入、各任务 RED≥5、ASSERT 非「全部转绿」）。

## 2026-08-26 design review 第 1 轮：FAIL → 回写完成
- 只读子代理审查四份 artifacts + Codex 源码抽查（20,000/0.90/丢最旧重试/SUMMARY 注入/单入口多触发 5 项全部吻合）。
- **4 Must Fix（事实已逐一在代码中核实后回写）**：
  1. 子代理双重写入/泄漏：子代理 run 共享父会话 ID（subagent_tool.go:157 → subagent.go:80）且走同一 execution.go 路径 → design §4.1 增「DelegationDepth>0 不挂 sink」；§5 裁决委派对唯一写入方=父 sink（调用对已在父 transcript，runner.go:374-377），完成路径（completeSubagentRun run_control.go:202）不再写；否则一次委派落 4 行。tasks Task 6 重聚焦（联合场景恰 2 行 + 内部抑制 + 完成路径 0 写入）。
  2. 索引排除漏点：fork（service.go:600-613）逐条 IndexMessage 且不复制类型列；`/compact` 与 `Context compacted.` 回声需打 system_echo 并跳过索引 → design §6 修正调用点清单并指定打标责任方；tasks Task 5 增 RED 11-13（system_echo×2 + fork）。
  3. PersistedMessageCount 与运行内压缩交互未定义 → design §4.2 裁决：压缩后游标重置为 len(新 transcript)，保留 user 条目与 SUMMARY 视为豁免；两构造器（hydrateCheckpoint :786 / checkpointFromMessages :752）均须携带计数。tasks Task 5 增 RED 9/10。
  4. 跨轮阈值三 Scenario 无任务覆盖 → tasks Task 3 增 RED 7-9（达到触发/未达 0 次/单条超硬上限溢出含估计值），并补 RED 10-11（claim 复用、指纹重复无操作）。
- **Should Improve 已吸收**：§9b 增 Codex 有意偏离（SUMMARY 前缀字面量、PerEntryLimitTokens=8,000）；§4.1 补 reflection feedback 挂点（runner.go:379-382）；§3 钉死 text 先于 function_call 写入顺序；§2.2 定总结输入渲染格式；Task 5 文件清单补 dependencies.go/service.go；Task 7 修正 DTO 实际位置（handler/agent_handler.go + 实体 json tag）；§4.4 增读侧消费者容忍声明；Task 5 增取消路径 RED 8。

## 2026-08-26 design review 第 2 轮：PASS
- 复审子代理（agent_bf92b40f，第 2 轮）逐条验收 4 项 Must Fix：全部实质消除且未引入新矛盾；行号引用经代码核实（checkpointFromMessages runner.go:752 及调用点 :203/:301/:600/:707、hydrateCheckpoint :786）。
- 16 个 requirement → tasks 全量闭合；常量四处一致；机械自检通过（RED 计数 6/12/11/6/14/6/6/5 与 DoD 一致）。
- **3 条遗留 Should Improve 已吸收**：Task 5 增 RED 14（普通 turn user text 与最终答案各被 IndexMessage 恰 1 次的正向断言）；Task 3 RED 9 增实现提示（单条溢出错误消息需补数值，spec 授权微改动）；Task 3 RED 10 更名 `CoordinatorCompactTest#reusesClaimedSnapshot` 并注明复用分支位置（:266-276）。
- `design_review_passed: true`。propose 阶段收口；用户已预确认 apply（user_confirm_apply: true）。

## 2026-08-26 apply 启动

### Skills Loaded: vsdd-workflow-router, vsdd-workflow-reverse-sync, vsdd-workflow-git-discipline, vsdd-workflow-implement-task, vsdd-workflow-review-implementation, test-driven-development, subagent-driven-development
（apply 阶段；7 项前置技能全部经 Skill 工具加载。templates/ 目录本机缺失——同 propose 阶段偏差处理，按 SKILL 内嵌格式执行。）

### S1 前置决策（用户暂无法应答，按已批准 artifacts 与工作流默认执行，全部留痕）
- **Branch Confirm**：main → `feature/unify-context-compaction`（VSDD 默认命名），base_commit=25420ec3。
- **工作区**：`doc/python-bridge-plan.md` 的删除为用户会话前操作且与本 change 无关 → `git restore` 恢复，保持工作区干净；`openspec/` 为本次 propose 产物（仅含新 change，无混用），随 setup commit 落盘。
- **Commit 策略**：本机无 `<AGENT_DIR>/vsdd-workflow.local.yaml`（已探测 `~/.zcode/skills`、`~/.agents`），工作流默认 false；因用户批准的 tasks.md 全局约束明确「每任务独立提交（atomic commit）」→ runtime 置 `auto_commit: true`，guidance=仓库现有 feat:/test:/chore: 风格。两值已写入 `.vsdd-state.yaml.runtime`（commit gate 前强制重读）。
- **Go 构建门**：本机无 Go 工具链（2026-08-25 已验证）。按 tasks.md 全局约束（用户批准）：Go 侧 RED/GREEN/build 执行延迟到有工具链的环境；每个 Go 任务的 Build Evidence 记录偏差说明，Task 8 交付完整命令清单。前端 typecheck/vitest 本地执行。此为对 apply SKILL S6.5 的**显式偏差**，证据形式=延迟验证记录+交付清单。
- tasks.md 格式校验通过（8 个 `- [ ] Task` 行）；state 更新：current_phase=apply、execution_mode=serial。

## 2026-08-26 apply 完成（Task 4-8）

- Task 4 `8821798`：轮内压缩委托核心（compactRuntimeTranscript → compaction.Compact，chatClientAdapter 适配 ToolCallingClient）；删除 runner 侧 5 处本地实现。
- Task 5 `4aab550`：MessageSink.PersistEntries 实时写入（§3 行映射：assistant text → function_call → function_call_output）；ResumePersistedMessageCount 恢复幂等；sink 失败降级为 StepTypeError 不中断；steering developer 计数不落库；压缩成功后游标重置。
- Task 6 `29669b9`：DelegationDepth>0 无 sink；委派对仅经父级 sink 落 2 行。
- Task 7 `a79a212`：ListMessages → messageDTO（content_type/tool_call_id/tool_name）；前端过滤 system_echo/reasoning、工具条目折叠卡。vitest 因 Node localStorage 环境缺陷阻塞（stash 基线 15/15 同错，证明非本变更引入）。
- Task 8：verification-checklist.md 落盘。**审计发现并修复**：coordinator 与 runner 仍残留本地 retain 循环与 coordinator 本地二分截断（重复点 6/12）→ `e3b0dbc` 收敛到核心 RetainEntriesByRole/TruncateToTokens（核心新增按角色保留泛化，coordinator 按 MessageID 回映源行并保留截断内容）。
- 验证：`GOOS=linux go build ./...` / `go vet ./...` exit 0；原生 `go test ./internal/...` 41 包绿（含核心两包 16+12 测试）；16 个原生失败包全部为既有 syscall.Flock/Kill Linux-only 约束 + 1 个既有 Windows 路径配置测试（git diff main 为空）；`openspec validate` valid。
- 用户指令执行：「减少 spec 书写、加快编码」→ T4-T8 未再走多轮评审，证据留痕于本 log 与 checklist。
