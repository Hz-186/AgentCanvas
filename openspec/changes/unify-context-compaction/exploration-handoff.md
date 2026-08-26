# Exploration Handoff — unify-context-compaction

> 目标：AgentCanvas 上下文压缩彻底向 Codex 对齐——单一 compact 算法、多处调用（轮间/轮内/手动）、统一历史纳入 tool 条目；保留快照持久化；前端按条目类型渲染、reasoning 不渲染。
>
> 参照基准：OpenAI Codex CLI @ d52478c5（codex-rs，Rust）。已核实事实：单一历史含 function_call/function_call_output/reasoning；一个算法三触发点（PreTurn `turn.rs:1013` / MidTurn `turn.rs:458` / 手动 `tasks/compact.rs`）；压缩后替换历史 = ≤20,000 tokens user 消息（`COMPACT_USER_MESSAGE_MAX_TOKENS`, `compact.rs:62`）+ 一条 user 角色摘要（SUMMARY_PREFIX 包装）；滚动摘要；总结超窗丢最早条目重试（`compact.rs:314-323`）；原始条目留盘、resume 取最新替换历史+增量。
>
> ### Skills Loaded: brainstorming, vsdd-workflow-router, openspec-explore

## 一、决策清单

> 来源标注：A=用户原话（verbatim）；B=代码证据（文件:行号）。

### 战略

| # | 决策点 | 结论 | 来源 |
|---|---|---|---|
| D1 | change 划分 | 单个 change（`unify-context-compaction`），内部任务可分阶段（先算法合并、后历史统一、再前端） | A："请你给出一个计划"、"在当前项目文件夹中新创建一个 openspec，不要和之前的混用" |
| D5 | tool 条目写入时机 | **实时逐条写入**（对齐 Codex rollout 实时追加，`session/mod.rs:3181-3183`）。衍生设计输入（propose 必须解决）：① 暂停/恢复时 checkpoint 重放不得重复入库（需幂等机制，如 run 内写入游标）；② 失败/取消的运行保留半截 tool 流（真实发生的事件，run_id 关联）；③ 写入钩子挂在消息产生点（`runner.go:369,731`） | A：用户选择"实时逐条写入（最像 Codex）" |
| D6 | 子代理写入 | 只写两条：派发=一条 function_call、子代理最终输出=一条 function_call_output；完整子 transcript 不入库 | A：用户选择"只写调用+结果两条（推荐）" |
| D7 | 压缩对象 | 全量送入总结器（含 function_call/output），对齐 Codex（`compact.rs:257-286`、`history.rs:589-609`）；单条超长 tool output 送总结前截断（复用现有截断机制） | A："总之都学习 codex 的做法" + B：`runner.go` 已有截断机制 |
| D8 | 持久化外壳 | 核心算法统一，持久化外壳各自保留：跨轮写 `conversation_compactions`（claim 锁 `compaction_repo.go:60` + 指纹去重 `:37` + snapshot_version 链），运行内写 runtime 快照行（`auto_compaction.go:245-293`） | A："持久化的部分和快照保留下来" |
| D9 | reasoning 条目 | 仅在 content_type 枚举预留 `reasoning`，本期不写入（无数据来源）；前端明确排除渲染 | A："把大模型推理的部分不渲染就可以了" + B：`chat_client.go:22-27` ChatMessage 无 reasoning 字段 |
| D10 | 旧数据兼容 | 不回填，新旧自然共存；旧会话照常压缩（少了 tool 上下文，与现状一致） | A：用户选择"不回填，自然共存（推荐）" |
| D11 | API/前端范围 | Message DTO 增加 `content_type`/`tool_call_id`/`tool_name`；前端按类型渲染（tool 条目折叠卡、reasoning 不渲染） | A："前端区分一下，把大模型推理的部分不渲染" + B：`web/src/types/api.ts:558-569`、`ChatPage.tsx:117-119,709` 已有按 role 过滤雏形 |
| D15 | 快照锚点语义 | FirstMessageID/LastMessageID 维持锚定"保留的 user 消息"（`filterFrozen`/`composeWindow` 语义不变，`coordinator.go:152-167,455-473`）；tool 条目只进总结器输入，不改变锚点 | A："持久化的部分和快照保留下来"（最小化快照语义改动） |
| D19 | 手动压缩回声 | `/compact` user 消息与 `Context compacted.` assistant 消息保留现有流转，但打 content_type 可忽略标记：前端不渲染、总结输入排除、索引排除 | A：用户选择"标记为可忽略（推荐）" |
| D21 | 搜索/检索索引 | tool 条目全部排除出 ES 会话搜索与 context_resource 索引（调用点 `service.go:813`、`turn_worker.go:671`、`message_repo.go:31` 加 content_type 判断） | A：用户选择"全部排除（推荐）" |
| D22 | 测试策略 | Go 侧产出/修改测试代码但不在本机运行（本机无 Go 工具链），交付清单显式列出"需在有工具链环境执行 `go test ./...`"；前端本地跑 typecheck + vitest 真实验证 | B：环境事实（本机无 `go` 命令，2026-08-25 验证）；前端有独立工具链 `web/package.json` |

### 选型

| # | 决策点 | 结论 | 来源 |
|---|---|---|---|
| D2 | 统一算法落点 | 新建独立包 `internal/runtime/compaction`，`conversationcontext` 与 `agent` 都依赖它，避免载体偏袒 | B：两套实现 12 处逐行重复（总结函数 `coordinator.go:346-407` vs `auto_compaction.go:119-175`；retain `coordinator.go:409-439` vs `auto_compaction.go:177-207`），仅载体类型（`conversation.Message` vs `llm.ChatMessage`）与客户端方法（`Chat` vs `ChatWithTools`）不同 |
| D3 | 输入/输出契约 | 定义消息无关的 `Entry`（Role/Content/ToolCallID/ToolCalls/ContentType），提供 `FromMessages([]conversation.Message)` 与 `FromChat([]llm.ChatMessage)` 两个适配器，核心 `Compact([]Entry) → ([]Entry, summary, usage, error)` | A："使用同一套 compact 算法，但是在多处调用，并且压缩策略一致" + B：三触发点输入异构（`coordinator.go:282` / `runner.go:210-243` / 手动=`Force=true` 的 PreTurn，`execution.go:129`） |
| D4 | tool 条目存储方案 | 新迁移恢复 `messages` 表的 `content_type varchar(32) NOT NULL DEFAULT 'text'` + `metadata_json json DEFAULT NULL`（复用曾被删除的列名） | B：`000001_agent_only_baseline.up.sql:885-902` 原建表含这两列，`000008_model_schema_cleanup.up.sql:56-57` 删除；`message.go:17-27` 现无结构化列 |
| D17 | token_budget 模式 | 保留为可配置旁路开关，不并入统一核心算法（Codex 同样存在该变体 `compact_token_budget.rs`） | B：`execution.go:299` 配置、`coordinator.go:288-291`、`auto_compaction.go:83-91` 独立分支与测试 |
| D20 | LLM 客户端 | 统一用 `Chat`（总结任务无需工具） | B：`coordinator.go:375` 已用 `Chat`；`auto_compaction.go:146` 用 `ChatWithTools` 属历史巧合 |

### 命名

| # | 决策点 | 结论 | 来源 |
|---|---|---|---|
| D12 | content_type 枚举 | `text` / `function_call` / `function_call_output` / `reasoning`（对齐 Codex/OpenAI 命名）；可忽略回声另加标记值（如 `system_echo`，propose 定） | A："都学习 codex 的做法" |
| D13 | role 枚举 | 不新增。function_call=role `assistant` + content_type `function_call`；output=role `tool` + content_type `function_call_output` | B：`message.go:9-15` 已有 user/assistant/system/tool/developer 五角色；11 处读侧按 role 过滤（improvement.go:250、session_search、context_resource 等），role 膨胀冲击面大 |
| D14 | metadata_json 字段 | `{"tool_call_id","tool_name","arguments"}`，snake_case | B：对齐 `llm.ToolCall` 语义（`chat_client.go:87-92`）+ 现有索引字段风格（`session_search.go:81-82`） |

### 数值

| # | 决策点 | 结论 | 来源 |
|---|---|---|---|
| D18 | 常量 | 全部维持现值：保留预算 20,000、阈值 0.90、超时 20s、前缀 `SUMMARY:\n`；合并时抽成单一常量组防再次漂移 | B：两侧已一致（`coordinator.go:25-26,226` / `auto_compaction.go:22-25`）且与 Codex `compact.rs:62` 吻合 |
| D16 | developer 消息 | goal continuation 的 developer 角色与现有保留逻辑维持不变 | B：`service.go:765-767` 写入、`auto_compaction.go:86` token_budget 保留依赖该角色 |

## 二、客观阻塞

无。全部 22 个决策点已 resolved（A=11，B=11）。

## 三、下一步建议

进入 **propose 阶段**（`vsdd-workflow-propose`），在 `openspec/changes/unify-context-compaction/` 产出 proposal / specs / design / tasks。

- **复杂度预判：🔴 standard**——跨模块（runtime/agent、runtime/conversationcontext、runtime/agentruntime、application/agent_usecase、infrastructure/mysql、migrations、interface/http、web/），含表结构迁移与两条压缩路径合并，必须走 design review。
- **建议任务阶段划分**（供 propose 细化）：
  1. 统一压缩核心包 `internal/runtime/compaction`（Entry 契约 + 单一算法 + 测试），两处调用方切换；
  2. 历史统一：messages 表迁移（content_type + metadata_json）→ runner 实时写入钩子（含暂停/恢复幂等）→ 子代理两条写入 → 跨轮压缩输入纳入 tool 条目（含超长 output 截断）→ 回声/索引排除；
  3. API DTO 扩展 + 前端类型渲染（本地可验证）。
- **最高风险点**：快照锚点语义与实时写入的交互（D5+D15）——propose 的 design 必须给出"压缩进行中/恢复后消息不重不漏"的明确机制。
- **验证门槛**：本机无 Go 工具链，`go test ./...` 需在有工具链的环境执行；前端 typecheck/vitest 可本地执行。

## 附：关键代码事实速览（propose 直接引用）

- 两套压缩算法 12 处重复点：总结函数、总结 prompt 文案、缺省 system、丢最早重试、退避重试、retain、阈值 0.90、预算 20,000、前缀、超时 20s、空摘要兜底、二分截断（`coordinator.go` vs `auto_compaction.go`，行号见 D2）。
- messages 表读侧消费者 11 处：API 展示（`agent_handler.go:276-287`）、fork（`service.go:596-613`）、userMessageForTurn（`service.go:705-719`）、遗留构建器（`assembly.go:25`）、协调器（`coordinator.go:282`）、查询改写（`assembly.go:196`）、改进审查（`improvement.go:239,250`）、dream（`dream_worker.go:174,204,302`）、抽取（`extraction.go:92`）、ES 索引（`session_search.go:77-96`）、context_resource（`message_repo.go:31`）。
- 手动压缩链路：`router.go:93` → `agent_handler.go:351-362` → `StartTurn{Content:"/compact", ManualCompaction:true}` → `execution.go:169-172` 直接返回不进模型循环。
- 子代理完成路径不写消息：`turn_worker.go:612-633`（`:632 return`）。
- checkpoint 以 JSON 整体存 `agent_run_checkpoints` 表（`turn_worker.go:747-791`），恢复经 `resumer.go:21-122`。
- Codex 侧证据：见文件头部参照基准段。
