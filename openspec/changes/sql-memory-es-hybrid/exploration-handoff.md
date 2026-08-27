# Exploration Handoff — sql-memory-es-hybrid

本次 Explore 已完成决策树穷举。目标是把 AgentCanvas 记忆系统改造成 SQL 唯一事实源、ES 关键词检索副本、统一异步写入，并吸收 Codex 的两阶段抽取/整合与 usage 驱动生命周期。

### Skills Loaded

- brainstorming
- vsdd-workflow-router
- openspec-explore（通过 `openspec list --json` 核验当前 changes）

## 决策清单 (Decisions)

### 战略与范围

| ID | 决策 | 结论 | 来源 |
|---|---|---|---|
| S1 | change 边界 | 新建独立完整 change `sql-memory-es-hybrid`，不扩展 `memory-usecase-dead-code-cleanup` 或 `unify-context-compaction` | 用户原话「建立一个完整的，然后开始提问，进入 propose」；代码核实两个旧 change 分属 cleanup/compaction |
| S2 | 复杂度 | 🔴 standard；跨 SQL schema、ES、embedding、worker、runtime、Reflection、迁移与前端/接口语义 | 代码核实：`internal/bootstrap/app.go:206-245`、`internal/domain/memory/runtime_service.go:113-333`、`internal/application/reflection_usecase/worker.go:51-174`；router 复杂度规则 |
| S3 | 变更拆分 | 一个统一 change，内部按依赖分 wave 实施；不拆成多个 change | 用户原话「建立一个完整的」 |
| S4 | 事实源 | SQL `memories`/artifact/job 表是唯一事实源；ES 只能作为检索副本，命中后必须回 SQL hydration | 用户原话「SQL 是唯一事实源」；`memory.Memory` @ `internal/domain/memory/memory.go:42-67`；`RuntimeService.Read` @ `internal/domain/memory/runtime_service.go:113-183` |
| S5 | 记忆颗粒度 | 原子记忆分条存储：一条记忆对应 `memories` 一行；汇总手册/摘要单独作为 artifact/projection 记录，不把全部内容拼成一个 `memories` 大字段 | 用户确认「`memories` 分条存储原子记忆 + 独立 artifact/projection 记录汇总工件」 |
| S6 | 文件读路径 | 迁移后 Agent-facing 读路径不再读取 `DurableFileStore`；旧文件只作为一次迁移输入 | 用户确认「迁移校验通过后直接删除旧 durable-memory 文件及目录」 |

### 文件工件归宿

| ID | 工件 | 目标归宿 | 来源 |
|---|---|---|---|
| F1 | `MEMORY.md` | `memory_artifacts` 中 owner 级 `handbook` projection，带版本、来源与变更元数据 | 用户确认六类工件映射；当前写入事实 `durable_memory_pipeline.go:564-572` |
| F2 | `memory_summary.md` | `memory_artifacts` 中 owner 级 `summary` projection，带版本；开局自动注入直接读 SQL | 用户确认六类工件映射；当前读取 `durable_memory_files.go:52-84`、`agentruntime/tools.go:401-422` |
| F3 | `raw_memories.md` | 抽取任务的 SQL `raw_input` 记录或等价规范化明细；不再生成事实文件 | 用户确认六类工件映射；当前 `durable_memory_pipeline.go:542-545` |
| F4 | `rollout_summaries/` | 每次 rollout 的 `rollout_summary` SQL 审计/证据记录，保留 run/job provenance | 用户确认六类工件映射；当前 `durable_memory_pipeline.go:909-928` |
| F5 | `skills/` | **不属于 memory artifacts**；由独立 skill 子系统及其 SQL/context 索引接管，`DurableFileStore` 退役后不得留下无主召回入口 | 用户原话「skills 不属于 memory artifacts，读路径由 skill 子系统接管」；当前 `durable_memory_files.go:133-134` 已把 `skills/` 暴露为 read_memory 搜索面；已有 skill domain/context indexing |
| F6 | `extensions/ad_hoc/notes/` | 作为统一异步写入输入记录，成功后生成普通 `memories` 行，`source=ad_hoc`；不再同步写 markdown | 用户确认六类工件映射；当前同步写入 `durable_memory_files.go:175-240` |
| F7 | phase2 diff/manifest | SQL artifact 版本/差异元数据；整合使用 SQL diff，不保留 filesystem manifest 事实源 | 用户确认六类工件映射；当前 diff/manifest 流程 `durable_memory_pipeline.go:549-578` |

### Reflection 归宿

| ID | 决策 | 结论 | 来源 |
|---|---|---|---|
| R1 | 分类原则 | 不新增 `reflection` memory type；Reflection 作为来源/证据 metadata，普通记忆与普通生命周期统一处理 | 用户确认「Reflection 全量吸收进统一 memories」；需求 D1；当前独立 `agent_reflections` schema @ `migrations/000001_agent_only_baseline.up.sql:164-207` |
| R2 | 内联反思 | 保留为当前 run 的临时反馈；需要持久化的证据进入统一抽取输入，不单独落 Reflection 记忆 | 需求 D2；代码 `internal/runtime/agent/reflection.go:23-77` |
| R3 | 终端 Reflection Worker | 并入统一 Memory Extraction Worker，生成普通 `memories` 行，`source=reflection`，metadata 保存证据/根因/原状态 | 用户确认 Reflection 全量吸收；当前 worker @ `internal/application/reflection_usecase/worker.go:51-174` |
| R4 | 已批准提案 | 改为统一 `memory_write_jobs`，`source=proposal`，不再创建独立 `agent_reflections` 记录 | 用户确认 Reflection 全量吸收；当前 `improvement.go:337-343` |
| R5 | 检索 | Reflection 记忆进入统一 Context ES 关键词索引，与普通 memory 同一检索入口 | 用户选择复用统一 Context 索引 A；当前 Reflection outbox @ `internal/infrastructure/mysql/reflection_repo.go:175-180` |
| R6 | 生命周期 | `validated/disputed` 不再作为独立记忆生命周期，映射为统一 `active/revoked/superseded` 或 metadata/audit | 用户确认 Reflection 全量吸收；需求 D1/D2 |
| R7 | 历史表与 API | 迁移校验通过后直接删除 `agent_reflections` 及独立 Reflection API/索引/worker 链路，不保留只读兼容期 | 用户选择 B：「迁移校验通过后直接删除旧表和独立 API」 |

### 命名与来源枚举

| ID | 决策 | 结论 | 来源 |
|---|---|---|---|
| N1 | artifact 表 | `memory_artifacts` | 用户确认命名方案 |
| N2 | artifact kind | `handbook`、`summary`、`raw_input`、`rollout_summary`、`ad_hoc` | 用户确认命名方案 |
| N3 | 统一写入队列 | `memory_write_jobs` | 用户选择 A |
| N4 | 记忆实体 | 沿用 `memory.Memory` / `memories`，不新增平行 durable entity | 用户确认命名方案；现有模型 @ `internal/domain/memory/memory.go:42-67` |
| N5 | 生命周期字段 | `usage_count`、`last_used_at`；禁止继续使用 `recall_count` 表达采用量，以免和 RecallLog 的返回侧账本混淆 | 用户原话「字段名改成 usage，不要叫 recall」 |
| N6 | source 枚举全集 | `extraction`（抽取产物）、`ad_hoc`（显式便签）、`proposal`（批准提案）、`consolidation`（整合重写）、`reflection`（反思派生）、`manual`（人工/接口编辑） | 用户原话 source 枚举全集 |
| N7 | skills 归属 | `skills` 不属于 memory artifacts，读路径由 skill 子系统接管；如未来确需 memory 搜索 skills，再单独扩展开放枚举 | 用户原话「skills 是类型清单里的缺席者」 |
| N8 | 旧写日志 | 退役 `memory_write_logs` 表、模型/repository/生产调用方；不与 `memory_write_jobs` 并存 | 用户原话「趁这次变更退役 memory_write_logs」；代码核实 `MemoryWriteLogRepository` 无生产调用方 |

### 检索与读入口

| ID | 决策 | 结论 | 来源 |
|---|---|---|---|
| E1 | ES 基础设施 | 复用统一 `ContextBackendIndex`、`ContextKeywordIndex`、`ContextSemanticIndex` 与 SQL context outbox；废弃旧 `agentcanvas_memories_v1` 专用 memory index | 用户选择 A；代码 `internal/infrastructure/retrieval/context_backend.go:50-113`、`memory_retrieval.go:19-174` |
| E2 | 检索模式 | 永久纯关键词，向量权重为 `0`；该决策覆盖需求附件 R2 的 `0.7/0.3` 混合检索要求，Proposal 必须显式记录覆盖关系 | 用户原话「先使用全部关键字查询，向量权重 = 0 的方案」并选择 B「永久改为纯关键词」 |
| E3 | 排序 | 使用 ES `_score` 降序；SQL hydration 不重排事实命中顺序，平分时使用稳定 `memory_id` 次序 | 用户原话「ES 查询利用评分排序」 |
| E4 | 自动摘要 | 直接读取 SQL `summary` projection；只在顶层运行注入，保留 advisory 文案与 token budget；摘要条目带稳定 `memory_id` | 用户确认读入口方案；当前 `agentruntime/tools.go:401-422` |
| E5 | `read_memory` | 统一 ES 关键词查询 + SQL hydration；保留 owner/agent/project/conversation 范围限定、默认 5 条、最多 20 条、单条最多 6000 字符；不再走文件分支 | 用户确认读入口方案；当前 `memory_tools.go:70-170`、`runtime_service.go:113-183` |
| E6 | 注入指引 | 自动摘要块增加简短提示：何时调用 `read_memory`、摘要可能过期、需要详情时不要仅依赖摘要 | 用户原话「考虑让注入块携带简短的“何时调 read_memory / 记忆可能过期”指引」 |

### Citation 与 usage

| ID | 决策 | 结论 | 来源 |
|---|---|---|---|
| C1 | 协议 | 使用 `<oai-mem-citation>` 块，列出被采用记忆的稳定 `memory_id` 和 source/provenance | 用户确认 citation 机制 |
| C2 | 展示处理 | 在解析前从最终展示文本剥离 citation 块；用户看到干净答案，块不泄漏到用户产物 | 用户原话「citation 块要从展示文本里剥掉」；Codex 参照 `stream_events_utils.rs:44-57,168` |
| C3 | usage 记账 | 只有模型实际采用并自报的记忆计入 `usage_count`/`last_used_at`；按 run/thread 去重；返回但未采用不计入 | 用户确认 citation 规则；需求 D3 |
| C4 | owner 校验 | 每个 `memory_id` 先校验存在且属于当前 run 的 owner；悬空/越权 ID 静默丢弃并记录告警 | 用户原话「悬空 id + owner 校验」 |
| C5 | 解析容错 | 行级容错：坏行只丢弃该行，其余合法行继续入账；不因整个块的一行错误拒绝全部 citation | 用户原话「解析器要‘行级容错’而不是‘块级拒绝’」；Codex `citations.rs:14-18` |
| C6 | RecallLog 边界 | `RecallLog` 继续记录“召回时返回了哪些记忆”的返回侧账；usage 字段只记录“模型采用了哪些记忆”，二者语义不合并 | 用户原话对 RecallLog/usage 的区分 |

### 异步写入与一致性

| ID | 决策 | 结论 | 来源 |
|---|---|---|---|
| W1 | 队列模型 | 新增 `memory_write_jobs` 作为统一入口；`memory_extraction_jobs` 保留抽取阶段证据/状态 | 用户选择 A |
| W2 | 写入来源 | ad-hoc、抽取、整合、批准 proposal、reflection 派生均转为统一异步 job；Worker 负责幂等、lease、重试、DLQ | 用户确认 R3 约束；现有 durable/context worker 事实 @ `durable_memory_pipeline.go:214-340`、`contextresource/worker.go:28-158` |
| W3 | 主链路失败语义 | 主运行只投递 job，写入/队列/ES/LLM 失败不得使成功运行失败；失败可观测并重试 | 需求 R3；当前 ad-hoc 失败吞掉并发事件 @ `agentruntime/execution.go:445-456` |
| W4 | SQL→ES | SQL 事务提交成功即事实；context outbox 异步更新 ES，使用 content hash/version 防旧事件覆盖新事件 | 需求 R1/R3；现有 `context_resource_index_outbox` worker stale-check @ `internal/domain/contextresource/worker.go:126-147` |
| W5 | freshness | SQL 提交后立即可作为事实源；ES 可检索目标 p95 ≤ 5 秒，允许短暂陈旧，不做同步 ES refresh | 用户选择 A |
| W6 | 可用性退化 | ES/embedding 不可用时不回退为全表宽扫；读取返回空结果/可观测错误，SQL 事实继续保留，索引 worker 继续重试 | 需求 R1/R3；代码核实 Context outbox lease/retry |

### 生命周期与兼容数值

| ID | 决策 | 结论 | 来源 |
|---|---|---|---|
| L1 | 升降级原则 | 只按 usage 驱动，不引入 LLM 质量评分或静态质量分；引用多者优先整合，冷落者按窗口淘汰 | 用户确认生命周期；需求 D4 |
| L2 | 默认窗口 | 最近 30 天有 `last_used_at` 的内容进入整合选材；从未使用时回退 `source_updated_at` | 用户确认生命周期默认值；Codex 参照 `max_unused_days=30` |
| L3 | 整合上限 | 每次整合最多 256 条 | 用户确认生命周期默认值；Codex 参照 top-N=256 |
| L4 | 保护边界 | 已进入 handbook/summary 的内容设置已整合保护，不因冷落直接删除；未保护旧输入先除名再清理；删除输入触发基于来源的摘要/手册外科式清理 | 用户确认生命周期；需求 D4 |
| L5 | 兼容上限 | 摘要 1200 token；`read_memory` 默认 5、最大 20；单条最多 6000 字符 | 用户确认「保持这些数值不变」 |
| L6 | 旧数据迁移 | 迁移文件与历史 Reflection，校验数量/哈希/owner 归属，回填 Context ES；校验成功后删除文件、`agent_reflections`、旧独立 API/index、`memory_write_logs` | 用户确认迁移删除策略；用户选择 Reflection B；用户要求退役 write logs |

## 客观阻塞 (Real Blockers)

无。当前仓库代码、SQL schema、ES/context outbox、worker 和 Codex 参照均可读取；Proposal 阶段需将上述覆盖 R2 的纯关键词决策显式写入正式合同。

## 下一步建议 (Next Step)

进入 `vsdd-workflow-propose`，初始化 `openspec/changes/sql-memory-es-hybrid/`，生成 `proposal.md`、`specs/`、`design.md`、`tasks.md`、`log.md` 与 `.vsdd-state.yaml`。复杂度为 🔴 standard，须完成两轮独立 design review；在你显式确认 Proposal 前不得进入 apply。
