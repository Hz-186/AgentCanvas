## Why

AgentCanvas 当前的 Agent-facing durable memory 仍以 owner 目录中的 Markdown 文件为主，而 SQL `memories`、ES context index、Reflection 子系统和异步管线各自拥有部分状态，导致事实源、召回语义、写入失败边界和生命周期规则不一致。现在需要建立一个完整的 SQL-first memory contract：原子记忆分条存储，ES 只做关键词检索副本，所有写入统一异步化，并将 Reflection 吸收为普通记忆来源。

## What Changes

- **BREAKING** 将 SQL `memories` 与 `memory_artifacts` 定义为唯一事实源；`DurableFileStore` 不再是 Agent-facing 读写路径。
- **BREAKING** 原子记忆按条写入 `memories`，一条记忆一行；`MEMORY.md`、`memory_summary.md`、raw input、rollout summary 使用 `memory_artifacts` 及抽取记录承载。
- **BREAKING** `read_memory` 改为统一 ES 关键词检索（永久 `vector_weight=0`）+ SQL hydration；ES `_score` 降序，保留 owner/agent/project/conversation 范围、默认 5/最大 20 条和单条 6000 字符上限。
- **BREAKING** 开局摘要直接读取 SQL `summary` projection，保留顶层运行限定、advisory 文案、1200 token budget，并提示何时调用 `read_memory` 及摘要可能过期。
- 新增 `memory_write_jobs`，统一承载 `ad_hoc`、`extraction`、`consolidation`、`proposal`、`reflection` 和 `manual` 六类写入；主运行不等待写入，不传导队列/SQL/LLM/ES 失败。
- 保留 `memory_extraction_jobs` 作为抽取阶段证据和状态；SQL 提交后继续使用 context outbox 异步更新 ES，目标 p95 可检索延迟不超过 5 秒。
- 新增 Codex 兼容的 `<oai-mem-citation>` 解析和 usage 记账；使用 `usage_count` / `last_used_at`，解析前剥离展示文本、逐行容错并校验 owner。
- 将 Reflection 的内联反馈、终端 worker、批准 proposal、向量索引和 validated/disputed 状态吸收到普通 `memories`、统一抽取/写入 job 和统一 Context 索引；迁移校验通过后删除 `agent_reflections`、独立 API/index/worker。
- 采用 usage 驱动生命周期：默认 30 天窗口、整合 top-256、已整合内容受保护，不使用 LLM 质量评分。
- 固定 `source` 枚举：`extraction`、`ad_hoc`、`proposal`、`consolidation`、`reflection`、`manual`。
- `skills` 不属于 memory artifacts，读路径由独立 skill 子系统接管；退役 `memory_write_logs` 表、模型、repository 和生产调用方。
- 提供一次性旧文件、旧 Reflection 迁移及哈希/owner 校验；成功后删除旧文件及目录，不保留文件回退读取。

## Capabilities

### New Capabilities

- `sql-memory-source`: SQL 原子记忆、artifact projection、统一 source/provenance 与迁移归宿。
- `keyword-memory-retrieval`: 统一 ES 关键词检索、SQL hydration、两个读入口等价语义和失败退化。
- `async-memory-writes`: 统一异步写入 job、SQL 事实提交、outbox 索引和 freshness/failure contract。
- `memory-citations-lifecycle`: citation 自报、usage 记账、Codex 风格使用驱动生命周期和淘汰保护。
- `reflection-memory-unification`: Reflection 吸收、历史迁移和独立 Reflection 面退役。

### Modified Capabilities

无。当前仓库没有可复用的 `openspec/specs/` capability 文件；本 change 新增上述行为契约。

## Impact

- SQL migrations：`memories` 字段重命名、`memory_artifacts`、`memory_write_jobs`、usage/lifecycle/provenance、旧 Reflection/write-log 清理。
- Go domain/application：`internal/domain/memory`、`internal/application/memory_usecase`、`internal/application/reflection_usecase`、runtime read/write/citation hooks。
- Go infrastructure：MySQL repositories、Context ES backend、outbox worker、bootstrap/config wiring。
- API/runtime：自动摘要块、`read_memory` 响应、citation strip/usage events、Reflection endpoint removal。
- Data operations：一次性 Markdown/Reflection import、checksum/tenant validation、ES reindex、post-check cleanup。
- Compatibility: pure-keyword retrieval intentionally supersedes the attachment's 0.7/0.3 vector requirement; this is a user-approved permanent contract.
