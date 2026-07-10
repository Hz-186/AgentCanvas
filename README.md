# AgentCanvas

AgentCanvas 是一个基于 **Go 1.22 + React 18 (TypeScript)** 构建的 **单人版 Agent Flow + RAG 知识库**工作台。项目采用 DDD 四层架构，以 **Agent = Model + Harness** 为核心理念，将 Agent 的壳分为 Commands（流程入口）、Skills（领域能力封装）、Rules（前馈约束）、Hooks（反馈兜底）四层，实现高可控、低 token 浪费的 Agent 执行运行时。

> **当前前端的制作还没有完成，后端仍需要打磨。** 部分高级特性（MCP Server、Team/Crew AI、Eval 评估体系）的后端实现已完成，前端页面与交互流程仍在开发中。

---

## 核心技术栈

### 后端

| 层级 | 技术 |
|------|------|
| 语言 | Go 1.22 |
| Web 框架 | Gin v1.10 |
| ORM | GORM v1.25 + MySQL Driver |
| 数据库 | MySQL 8.4 |
| 缓存 | Redis 7.2 + RediSearch |
| 对象存储 | MinIO |
| 全文检索 | Elasticsearch 8.15 |
| 向量数据库 | Milvus 2.5.4（可选） |
| 消息队列 | MySQL Queue / Redis Stream / NATS JetStream 三种后端 |
| 加密 | AES-GCM（API Key 加密）、bcrypt（密码哈希）、JWT（HS256） |
| 日志 | Go 标准库 `log/slog` 结构化日志 |

### 前端

| 层级 | 技术 |
|------|------|
| 框架 | React 18 + TypeScript |
| 构建 | Vite 5 |
| 画布 | React Flow (@xyflow/react v12) |
| 路由 | React Router DOM v6 |
| 状态管理 | Zustand v5 |
| 图标 | Lucide React |
| 测试 | Vitest + @testing-library/react |

---

## 架构设计

### Agent = Model + Harness 四层壳

Agent 的执行能力不仅仅取决于模型本身，更取决于包裹在模型外层的 **Harness 四层壳**。这一设计验证了 **Agent 的核心竞争力在于 Harness 而非 Model** 的设计哲学——同一个模型，换一套更精巧的 Harness，执行通过率可产生显著提升，而模型本身一个字节未改。

```
┌─────────────────────────────────────────┐
│                  Agent                  │
│  ┌───────────────────────────────────┐  │
│  │          Harness (四层壳)          │  │
│  │  ┌─────────────────────────────┐  │  │
│  │  │  Commands ─ 流程入口        │  │  │
│  │  │  Skills   ─ 领域能力封装     │  │  │
│  │  │  Rules    ─ 前馈约束        │  │  │
│  │  │  Hooks    ─ 反馈兜底        │  │  │
│  │  └─────────────────────────────┘  │  │
│  └───────────────────────────────────┘  │
│         Model (LLM Provider)            │
└─────────────────────────────────────────┘
```

四层职责边界被严格划分，避免 token 消耗失控：
- **Commands**：流程入口，控制 Agent 的执行生命周期（run → plan → execute → judge → resume），四种模式（ReAct / Plan-Execute / Reflect / Supervisor）由 Commands 层统一调度
- **Skills**：领域能力封装，将可复用能力抽象为标准化模块，通过 `load_skill` / `skill_search` 工具按需加载
- **Rules**：前馈约束，基于 MCTS 剪枝思想的 L0-L4 五级分级注入，确保进入模型的上下文是精选而非全量
- **Hooks**：反馈兜底，`preToolUse` / `postToolUse` 双环拦截，覆盖危险命令物理阻断 + 敏感字段脱敏 + 输出压缩

---

### Agent 四种运行模式

系统支持四种 Agent 模式，通过 `mode` 字段（`react` / `plan_execute` / `reflect` / `supervisor`）选择，默认 `react`。模式配置遵循三层优先级：Node Config → Profile Defaults → Fallback（react）。

#### 1. ReAct 模式（默认）

完整的 Thought → Action → Observation 循环，核心实现在 `runner.go:35-451`：

```
[Context 组装] → [LLM Call] → [Check Response]
                               ├─ 无 tool_call → FinalAnswer → 结束
                               └─ 有 tool_call → [Execute Each Tool]
                                                    ├─ PreHook 检查
                                                    │   ├─ NeedApproval → Pause(Checkpoint)
                                                    │   ├─ Denied → 注入错误 → Continue
                                                    │   └─ Allowed → 执行 Tool
                                                    ├─ PostHook 处理 (压缩/脱敏)
                                                    └─ 注入 Tool Result → Next Iteration
```

**关键参数**：默认 `MaxIterations=8`，`MaxToolCalls=16`。单次 LLM 调用可返回多个 tool_call，每个 tool 串行执行。

**八种停止原因**：`FinalAnswer` / `MaxIterations` / `MaxToolCalls` / `Timeout` / `Cancelled` / `WaitingHuman` / `LLMError` / `ToolNameNotFound`

#### 2. Plan-Execute 模式

先规划后执行，分两阶段运行：

**阶段一 —— 生成计划**（`planner.go`）：
- `Planner.GeneratePlan()` 向 LLM 请求生成 3-8 步的 JSON 执行计划
- 返回 `{steps: [{number, description, tool_name}], ...}`，所有步骤初始为 `pending`

**阶段二 —— 执行计划**：
- `Plan.PlanContext()` 将计划作为 `pinned: true` 的系统提示注入上下文
- 当 LLM 返回无 tool_call 的 final answer 时，`Plan.Finish()` 标记所有步骤完成并停止

**注意**：Planner 生成的是一次性静态计划，运行期间不会被 LLM 动态修改，但 LLM 可根据 "if a step fails, revise the plan and continue" 指令自行调整行为。

#### 3. Reflect 模式（反思模式）

Reflect 不是独立的执行循环，而是 **三层反思体系**嵌入在 ReAct 循环之上：

**第一层 — 指令级反思**（System Prompt 注入）：
```
"Use reflection: after each tool result or draft answer, verify correctness,
schema compliance, and missing evidence before finalizing."
```
当 `mode=react` 且 `ReflectionEnabled=true` 时也会注入类似指令，实现 ReAct + Reflection 混合模式。

**第二层 — Schema 反思修复**（`agent_loop_node.go:561-598`）：
Runner 返回 final answer 后，如果配置了 `output_schema_json`，自动验证 JSON schema 合规性；不合规则调用 LLM 修复，修复结果以 `StepTypeReflection` 追加到 Steps 中。

**第三层 — Judge 质量评分**（`judge.go`）：
离线/事后评估组件，三层评分机制：
- **工具覆盖率**：`evalharness.Coverage` 比较预期工具 vs 实际使用工具
- **内容匹配**：子串匹配 + 词重合度计算（忽略 ≤2 字短词）
- **LLM 二次评分**：可选的另一个 LLM 对输出打分
- 综合得分 ≥ 0.6 视为通过

#### 4. Supervisor 模式（多 Agent 委派）

Supervisor Agent 通过 `call_agent` 工具（`toolruntime.WorkflowCallTool`）将任务委派给子 Agent，自身负责审查结果和合成最终答案。

**委派机制**：

```
Supervisor Agent
    │
    │ call_agent(workflow_id=X, input=...)
    ▼
┌──────────────────────────────────────────────┐
│  安全检查 (supervisor.go:46-56)               │
│  ├─ CheckCallChain() → 防循环委派              │
│  │   └─ 目标 workflow 已在 callChain 中 → 拒绝  │
│  └─ depth < maxDepth → 防深度过大             │
│      └─ currentDepth >= maxDepth → 拒绝       │
├──────────────────────────────────────────────┤
│  启动子 Workflow Run (嵌套执行)                │
│  ├─ parent_run_id 关联父 Run                  │
│  ├─ caller_node_id 标记调用节点                │
│  └─ call_depth = parent.call_depth + 1        │
├──────────────────────────────────────────────┤
│  子 Run 完成后返回结果给 Supervisor           │
│  └─ Supervisor 审查结果 → 合成最终答案          │
└──────────────────────────────────────────────┘
```

**白名单机制**：Supervisor 只能委派给 `call_workflow_ids` 中明确列出的子 Workflow。`max_workflow_call_depth`（默认范围 0-5）限制嵌套深度。

#### 暂停/恢复与 Checkpoint 状态机

```
Agent 执行中
    ├─ tool 需审批 → Checkpoint(含 PendingToolCall) → 暂停
    │   ├─ 外部 Approve → 恢复执行 pending tool → 继续循环
    │   └─ 外部 Reject → 注入 "Human rejected: ..." 消息 → LLM 适应反馈
    └─ ctx.Cancelled → Checkpoint(无 PendingToolCall) → 暂停
        └─ 外部 Resume → 从消息历史恢复 → 执行未完成 tool → 继续循环
```

恢复时执行 **工具注册表哈希校验**：如果 Resume 时的工具集或策略与 Checkpoint 时刻不一致，暂停恢复并报告哈希不匹配。

---

### Harness 双环控制：Rules + Hooks

#### Rules 规则分级体系（前馈约束）

将蒙特卡洛树搜索（MCTS）的剪枝思想应用于规则分级管理，实现五级分级注入：

| 级别 | 类型 | 策略 | 说明 |
|------|------|------|------|
| L0 | Safety | 永久硬挂载 | 安全规则，不可被剪枝（baseScore=1000） |
| L1 | Core | 永久硬挂载 | 核心行为约束，永久剪掉明显错误分支（baseScore=800） |
| L2 | Scenario | 按需加载 | 场景规则，激活信号驱动，token 预算内按分数加载（baseScore=300） |
| L3 | Tool | 按需加载 | 工具级别规则，工具使用时才激活（baseScore=180） |
| L4 | Ephemeral | 低优先按需 | 临时/实验性规则，最低得分，预算不足时首批裁剪（baseScore=80） |

每条规则可配置激活信号（`mode_any`/`tool_any`/`tool_all`/`risk_any`/`tag_any`/`tag_all`/`keywords_any`/`keywords_all`/`always`）和排除信号（`exclude_modes`/`exclude_tools`/`exclude_tags`/`exclude_risk`），配合 `level_budgets`（Default: L2=45%/L3=35%/L4=20%）和 `score_cutoff` 实现**信息熵压缩**——通过分层预算精确控制每个级别注入上下文的信息量。

规则加载采用 `mcts_pruning:tiered_budgeted_scoring` 策略：
1. L0/L1 永久硬挂载，不可被任何条件剪枝
2. L2-L4 先按 legacy triggers + activation signals 匹配，再按 score 排序
3. 在 score 排序基础上，按 level_budget → global_budget → max_candidates 三级预算逐层裁剪
4. 对 `MaxCandidates` 和 `score_cutoff` 超限的候选项直接丢弃

在场景信号精准匹配 + 分层预算控制下，32K 窗口模型的可用空间从 8.5K tokens 翻到 17K tokens。

#### Hooks 拦截链（反馈兜底）

`preToolUse` 和 `postToolUse` 双环 Hook 链，`ToolHookChain` 串联多个 Hook 实现责任链模式：

**PreToolUse 链**（PolicyPreToolUseHook）：
- **危险命令物理拦截**：检测 `rm -rf /`、`mkfs.`、`dd if=`、`fork bomb`（`:(){` 模式）、`chmod -r 777 /`、`chown -r`、`> /etc/`、`launchctl unload`、`systemctl disable` 等 40+ 种危险模式，在工具调用前直接 `Denied` 返回
- **风险审批流**：`RequireApprovalForRisk` 检查工具风险等级，高/中风险触发 `Approval` → 暂停执行 → 等待人类审批
- **主机白名单**：`validateAllowedHosts` 校验 HTTP Tool 目标主机必须在 `allowed_hosts` 范围内
- **超时控制**：`effectiveTimeoutMS` 取 tool metadata 和 policy 中较小的超时值

**PostToolUse 链**（ObservationPostToolUseHook）：
- **敏感字段脱敏**：`api_key`、`authorization`、`access_token`、`refresh_token`、`password`、`secret` 等字段自动 `[REDACTED]`
- **输出压缩截断**：超过 `max_tool_output_bytes` 时字符级截断（`strutil.TruncateWithSuffix`），JSON 消息级别截断保护二进制数据安全

---

### Skills 领域能力封装体系

#### Skill 分类体系

| 维度 | 类型 | 说明 |
|------|------|------|
| **SkillType** | `instruction`（默认） | 指令型 Skill，Markdown 文本形式的提示词/工作流指令 |
| | `bundle` | Bundle 型 Skill，多文件组成的完整能力包 |
| **SourceType** | `inline`（默认） | 内容存储在 DB 的 `content_md` 字段中 |
| | `local_path` | 内容存放在文件系统上，通过 `bundle_path` + `entry_file` 定位 |

#### Skill 生命周期

```
CREATE → PREPARE(12+条校验规则) → VALIDATE(SHA256 checksum) → RUNTIME USE → SOFT DELETE
```

**创建/更新时自动执行 12+ 条校验规则**（`skill_usecase/service.go:175-218`）：
- Name / Description 非空
- SkillType 仅限 `instruction` / `bundle`
- EntryFile 路径安全校验（禁止 `..` 穿越，禁止绝对路径）
- `local_path` 模式：bundle_path 必须在 workspace 内，entry_file 必须在 bundle_path 内
- `inline` 模式：content_md 不能为空
- SHA256 checksum 自动计算和追踪（`LastValidatedAt` + `LastValidationError`）

#### 运行时 Skill 加载机制

Skill 通过两种工具注入 Agent 的工具集：

| 工具 | 触发方式 | 功能 |
|------|---------|------|
| **load_skill** | LLM 主动调用 `load_skill(skill_id=N)` | 从 DB 加载完整 SKILL.md 内容，按 `MaxContentBytes` 截断 |
| **skill_search** | LLM 调用 `skill_search(goal=...)` | 对 name/description/tags 打分排序，返回 Top-K 匹配 |

**两种加载模式**（`SkillLoadingMode`）：

| 模式 | 行为 | 适用场景 |
|------|------|---------|
| `metadata_only`（默认） | 仅注入 skill name/id/description 到上下文，LLM 按需调用 `load_skill` | skill 数量 ≤10 |
| `search` | 注入 search 指令 + 注册 `skill_search` 工具，LLM 先搜索再加载 | skill 数量 >10 |

上下文注入格式：
```
Available skills:
- name: record-knowledge, id: 1
  description: 将代码知识整理为结构化文档
  load: use load_skill with skill_id=1 when the task matches this skill.
```

最多展示 20 个 skill。`search` 模式下描述截断到 160 字符，提示使用 `skill_search` 先搜索。

#### 内置 Skills

| Skill | 触发条件 | 核心设计 |
|-------|---------|---------|
| **explain-knowledge** | 用户说"讲解知识" | 6原则5步流程：代码驱动→逐层拆解→全程中文注释→真实数据演示→串联贯通→渐进确认 |
| **record-knowledge** | 用户说"记录知识"或"写成文档" | 9章标准文档模板：全景概览→数据结构→核心逻辑→指标→接口→数据流转→API路由→调用链图→源码索引 |

---

### Multi-Agent / Crew AI 团队协作

#### 架构设计

通过 Supervisor + Worker 角色分离，实现类 Crew AI 的多 Agent 协作：

```
┌─────────────────────────────────────────────────┐
│                  Crew Team                       │
│  ┌───────────────────────────────────────────┐  │
│  │           Supervisor Agent                │  │
│  │  (审查结果 + 合成最终答案 + 质量把关)        │  │
│  └──────────┬──────────┬──────────┬──────────┘  │
│             │ call     │ call     │ call         │
│             ▼          ▼          ▼             │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐        │
│  │ Agent 1  │ │ Agent 2  │ │ Agent 3  │        │
│  │(Researcher│ │(Writer)  │ │(Reviewer)│        │
│  └──────────┘ └──────────┘ └──────────┘        │
└─────────────────────────────────────────────────┘
```

**数据模型**（`workflow_teams` + `workflow_team_members`）：
- Team 定义：名称、描述、Supervisor Workflow ID、全员 Workflow ID 列表
- 每个 Team Member 有自己的独立 Profile（模式/Risk Level/Tool Packs/Skills/Memory Policy 等）
- `AllowDelegation` 开关控制是否允许 Supervisor 将任务委派给子 Agent

**安全机制**：
- **循环委派检测**：`CheckCallChain(callChain, targetWorkflowID)` 检查目标是否已在调用链中
- **深度限制**：`call_depth` 追踪嵌套深度，受 `max_workflow_call_depth`（0-5）限制
- **白名单制**：Supervisor 只能委派给 `call_workflow_ids` 中明确列出的子 Workflow

---

### DAG Flow 执行引擎

基于有向无环图的声明式节点编排，`engine/executor.go` 实现完整的 DAG 执行器。

**23 种节点类型**：

| 类别 | 节点 |
|------|------|
| 流程 | BeginNode、MessageNode |
| AI 核心 | LLMNode、PromptNode、AgentLoopNode |
| 检索 | RetrievalNode |
| 记忆 | MemoryWriteNode、MemoryQueryNode |
| 工具 | HttpToolNode、MCPToolNode、WorkflowCallNode、TeamCallNode |
| 逻辑 | SwitchNode |
| 沙箱 | CodeSandboxNode |
| 输出 | StructuredOutputNode、OutputControlNode |

**执行器特性**：
- DAG 拓扑排序 + 并发节点并行执行
- 变量解析器（`VariableResolver`）：支持 `{{node_id.output_field}}` 跨节点引用
- 嵌套 Workflow 调用：`call_depth` + `parent_run_id` + `call_chain_json` 完整追踪调用链
- SSE 实时流式事件推送：`run_events` 实时推送 + `node_logs` 每节点独立日志
- 暂停/恢复/取消：审批集成 + Checkpoint 安全哈希校验
- Flow Version 管理：保存/校验（DSL v1 DAG 拓扑验证）/发布，`workflow_versions` 表追踪

---

### 上下文压缩引擎

为解决长对话的上下文窗口压力，实现了 **纯算法驱动的上下文压缩引擎**（`contextcompress/`，7 个核心文件），完全不依赖 LLM 调用。

#### 三步压缩管线

```
Compress(items)
  ├─ Select(items) → {Selected, Omitted, Scores}
  │   └─ LazyGreedy 选择器 (子模优化)
  ├─ rankFragments(Omitted) → 按 salience + keySignal 排序的片段
  │   └─ 短片段惩罚 (≤2 tokens → 0.45x)
  └─ selectFragments(fragments, budget) → 多样性贪心选择
      └─ renderSummary → "Earlier conversation summary:" + 项目符号列表
```

#### 三重指纹相似度融合

```go
approximateSimilarity = 0.55*cosine + 0.30*jaccard + 0.15*hamming_sim
```

| 指纹 | 实现 | 权重 | 说明 |
|------|------|------|------|
| **TF-IDF Cosine** | IDF 加权词频向量的余弦相似度 | 55% | 语义内容相似度 |
| **MinHash Jaccard** | 64 签名 MinHash（FNV64a + 旋转哈希），Jaccard = intersect / max(a,b) | 30% | 集合级别的相似度 |
| **SimHash Hamming** | 64 位 IDF 加权 SimHash，`sim = 1 - hamming_distance/64` | 15% | 快速去重指纹 |

#### 新颖度评分模型

```go
weight = (novelty + keySignal) * decay * importance
```

- **Novelty**：基于累积词频，`1 - (已见词权重/总权重)`，重复消息被压到 `0.08`
- **KeySignal**：检测 `must/never/always/error/failed/必须/不要/错误/修复/约束/重要` 等关键信号词（每个 0.18，上限 0.9）
- **TimeDecay**：`exp(-0.08 * age)`，Alpha=0 时关闭
- **Pinned 保底**：系统指令等 pinned 条目 novelty 不低于 0.45

#### 后缀数组最长公共子串

- 倍增法 O(n log n) 后缀数组 + Kasai 算法 O(n) LCP
- `longestCommonSubstringSimilarity`：最大 LCP / 较短文本长度，阈值 0.72
- `longestPreviousPrefix`：双向扫描 LCP 找最大重复前缀

---

### LLM 双层缓存（L1 Redis + L2 语义向量）

L1（SHA256 精确匹配）+ L2（向量语义模糊匹配）+ L2→L1 回写策略：

```
CachedChatClient.Chat()
  ├─ L1 精确命中？→ 直接返回 (< 1ms)
  ├─ L2 语义命中？→ 返回 + 回写 L1 (提升后续命中)
  └─ 无命中 → 调用 chatInner → 同时写入 L1 + L2

CachedChatClient.StreamChat()
  ├─ L1 命中 → replayCachedStream() 模拟流式输出
  ├─ L2 命中 → replayCachedStream() + 回写 L1
  └─ 无命中 → 真实流式调用 → 写入 L1 + L2
```

**L2 语义缓存细节**：
- 取最后一条 user message 通过 EmbeddingModel 向量化
- 在 `llm_semantic_cache` 集合中做 HNSW 搜索（TopK=1），过滤 `owner_id` 实现租户隔离
- 相似度阈值：`score > (1 - threshold)`，默认 threshold=0.96

---

### RAG 知识库与检索

**知识库全生命周期管理**：

- txt / md 文件上传到 MinIO，Worker 异步队列消费
- **DeepDoc 深度文档解析引擎**：
  - **PDF 双路径解析**：字面文本提取（BT/TJ 操作数解码、十六进制字符串 UTF-16BE 自动检测）→ 乱码检测（≥30% 乱码率触发）→ OCR 回退路径
  - **K-Means 版面分析**：一维 K-Means 对 X 中心点聚类实现自动列检测（`AssignColumn`），通过 Silhouette Score 自动选择最优 K 值
  - **7 种块分类**：heading / caption / scrap / table / list / faq / text，智能排版合并（`TextMerge` 水平合并 + `NaiveVerticalMerge` 垂直合并 + `isMultiColumn` 多栏检测）
  - **表格解析**：`|` 分隔符检测 → `SimpleRowsToHTML` 转 HTML，HTML 转义确保安全
  - **乱码修复**：`\ufffd` / Control / PrivateUse / HighSurrogate 字符检测，乱码率 ≥30% 自动触发 OCR
- **FixedTokenChunker**：基于 token 计数的固定窗口切片
- **RecursiveChunker**：段落→句子→词组递归切片

**RAG 双召回架构**（检索技术核心在于召回链路设计）：

| 召回模式 | 实现方式 | 适用场景 |
|---------|---------|---------|
| **Keyword** | Elasticsearch `multi_match` + BM25 评分 | 精确词匹配、专有名词、短 query |
| **Vector** | Milvus / RediSearch HNSW（M=16, EFConstruction=200, Cos-sim） | 语义相似、长文理解、同义改写 |
| **Hybrid** | BM25 + kNN 分数融合（加权求和） | 通用场景，兼顾精确匹配和语义覆盖 |

**Rerank 重排序双策略**：
- **ChatReranker**：利用 LLM 自身能力，序列化候选人（最多 20 个，每个 ≤800 字符）为 JSON，零温调用 LLM 按相关性排序
- **BGEReranker**：调用标准 `/rerank` 端点（如 `bge-reranker-v2-m3`），30 秒超时，合并 `relevance_score` / `score` 字段到 `FinalScore`

---

### 记忆系统

三层分级记忆模型：

| 级别 | 存储 | 生命周期 | 说明 |
|------|------|---------|------|
| **Working Memory** | Redis | 单次 Agent Loop | 四维模型：当前任务(ActiveTask) + 近期事实(RecentFacts) + 注意力焦点(AttentionFocus) + 上下文摘要(ContextSummary) |
| **Short-term** | MySQL + Vector | 对话会话级别 | 当前 session 关键信息提取，置信度 ≥0.7 才输出到上下文 |
| **Long-term** | MySQL + Vector | 跨会话持久化 | 用户偏好、历史决策，含 `embedding` + `memory_level` + `access_count` + `consolidation_count` |

- **Dream（记忆梦境）**：定时 Job 遍历记忆，LLM 自动提取合并，`conflict_flag` 冲突检测去重
- **Working Memory** 事件驱动更新，`ToContextBlock()` 自动序列化为 LLM 可读格式

---

### 评估体系（EvalHarness）

- `workflow_eval_datasets` + `workflow_eval_cases` 数据集/用例管理
- 批量评估运行（`workflow_eval_runs`），自动评分（Coverage + Content Match + LLM Judge）
- 评估趋势追踪（`GET /eval-datasets/:id/trend`）
- 指标：准确率、召回率、F1、Latency、Token Cost

---

### Code Sandbox 代码沙箱

Docker 容器六重隔离（`sandbox.go`）：

| 隔离层 | 实现 |
|--------|------|
| 进程 | `docker run --rm python:3.12-alpine` |
| CPU | `--cpus 1` |
| 内存 | `--memory 128m`（上限 512m） |
| 进程数 | `--pids-limit 64`（防 fork 炸弹） |
| 网络 | `--network none`（可选启用） |
| 文件系统 | `-v /tmp/sandbox:/workspace:ro` 只读挂载 |

超时保护（默认 5s，上限 30s）+ `limitedBuffer` 输出截断（默认 64KB，上限 1MB）。

---

## 目录结构

```text
cmd/                        API、Worker、Migration 三个入口
  api/main.go               ─ Gin HTTP Server (SSE 流式 + SPA 嵌入)
  worker/main.go            ─ 异步文档解析/索引 Worker
  migrate/main.go           ─ 数据库迁移工具
configs/                    运行配置 (YAML)
  config.yaml               ─ Docker 环境默认配置
  config.local.yaml          ─ 本地开发配置（不提交 Git）
conf/                       预置配置内嵌
  embed.go                  ─ go:embed 内嵌 providers/*.yaml
  providers/                 ─ 模型供应商预置目录
deployments/                容器化部署
  docker/Dockerfile          ─ 多阶段构建 (golang → debian-slim)
  docker-compose.yml         ─ 本地依赖 (MySQL/Redis/MinIO/ES/Kibana/Milvus/etcd/NATS)
  docker-compose.dev.yml     ─ 开发环境编排
internal/                   核心后端代码
  application/              应用用例层 (12 个包)
    auth_usecase/            ─ 认证 (注册/登录/JWT/OAuth/API Token)
    chat_usecase/            ─ RAG Chat (流式SSE/上下文打包/提示词构建)
    dialog_usecase/          ─ Dialog 会话管理
    ingestion_usecase/       ─ 文档解析/切片/索引
    knowledge_usecase/       ─ 知识库管理/文档上传/检索/重建索引
    memory_usecase/          ─ 记忆提取/合并/Dream/缓存
    provider_usecase/        ─ 模型供应商管理/API Key 加密
    retrieval_usecase/       ─ Keyword/Vector/Hybrid 检索 + Rerank
    skill_usecase/           ─ Skill 技能管理 (12+条校验规则)
    tool_usecase/            ─ Tool 定义/Tool Policy/Tool Pack/MCP Server
    workflow_usecase/        ─ Workflow CRUD/Flow Version/Run/Eval/Approval/Team
    audit_usecase/           ─ 审计日志查询
  bootstrap/                 ─ 启动引导 (App 装配器)
  domain/                   领域层 (15 个包, DDD)
    workflow/                ─ DSL/Workflow/FlowVersion/Run/Profile/Eval/Approval/Team
    knowledge/               ─ 知识库/文档/Chunk/Ingestion
    memory/                  ─ 记忆/Working Memory/Cache
    retrieval/               ─ 检索接口定义
    provider/                ─ 模型供应商
    auth/                    ─ 认证 (APIToken)
    conversation/            ─ 对话/消息/引用
    dialog/                  ─ Dialog 会话
    tool/                    ─ 工具定义/MCP
    skill/                   ─ Skill 定义 (instruction/bundle + inline/local_path)
    usage/                   ─ 模型用量
    user/                    ─ 用户
    audit/                   ─ 审计日志
    flow/                    ─ DSL v1 定义
  infrastructure/           基础设施层 (16 个包)
    mysql/                   ─ GORM 仓库实现 (30+ Repository)
    redis/                   ─ Redis 客户端/RediSearch/Memory Cache/WorkingMemory
    elasticsearch/           ─ ES 客户端及索引管理
    minio/                   ─ MinIO 对象存储
    vectorstore/             ─ Milvus + Redis Stack 向量存储 (HNSW 索引)
    retrieval/               ─ ES 检索/Milvus 检索/Composite 组合/Memory 检索
    llm/                     ─ Chat/Embeddings/Rerank (ChatReranker + BGEReranker)/LLM Cache (L1+L2)
    deepdoc/                 ─ PDF 解析: 字面提取+OCR/K-Means版面分析/7种块分类/表格/乱码修复
    parser/                  ─ 文档解析器注册表
    chunker/                 ─ FixedTokenChunker/RecursiveChunker
    queue/                   ─ MySQL Queue/Redis Stream/NATS JetStream
    catalog/                 ─ 供应商预置目录加载
    crypto/                  ─ AES-GCM/JWT/bcrypt
    oauth/                   ─ GitHub OAuth
    job/                     ─ Memory Dream 定时调度
  interface/http/           HTTP 接口层
    handler/                 ─ 12 个 Handler
    middleware/              ─ Auth (JWT+API Token)/CORS/RequestID/Recovery
    sse/                     ─ SSE Writer
  pkg/                      通用工具包
    config/                  ─ YAML 配置加载/验证
    errors/                  ─ 自定义错误
    idgen/                   ─ ID 生成
    logger/                  ─ slog 封装
    response/                ─ 统一 HTTP 响应格式
    strutil/                 ─ 字符串工具 (截断/脱敏)
  runtime/                  Agent 运行时引擎
    agent/                   ─ 4种模式: Runner/Planner/Supervisor/Judge/Resumer/ContextAssembler/Metrics
    engine/                  ─ DAG 执行器/VariableResolver
    harness/                 ─ Harness框架: Rules (L0-L4 MCTS剪枝) + Hooks (preToolUse/postToolUse责任链)
    node/                    ─ 23 种节点实现
    toolruntime/             ─ 工具运行时 (7种Tool + ToolRegistry + Skill集成)
    evalharness/             ─ 评估指标 (Coverage + Judge)
    sandbox/                 ─ Docker六重隔离代码沙箱
    contextcompress/         ─ 三重指纹压缩: SimHash+MinHash+Cosine/后缀数组LCS/LazyGreedy/CJK Shingle
migrations/                 数据库迁移 SQL (25 组, .up.sql + .down.sql)
scripts/                    本地脚本 (dev/migrate/lint/build/verify)
web/                        React + Vite 前端 (SPA, 内嵌 embed)
skill/                      内置 Skill 预设 (explain-knowledge / record-knowledge)
```

---

## 数据库表清单（44 张表）

| 表名 | 用途 |
|------|------|
| `users` | 用户账号（bcrypt 密码哈希） |
| `oauth_accounts` | GitHub OAuth 绑定 |
| `auth_sessions` | Refresh Token 会话 |
| `api_tokens` | API Token 管理 |
| `model_providers` | 模型供应商（API Key AES-GCM 加密） |
| `audit_logs` | 审计日志 |
| `knowledge_bases` | 知识库（含 embedding/rerank 配置） |
| `documents` | 上传文档 |
| `document_chunks` | 文档切片 |
| `ingestion_jobs` | 异步解析任务 |
| `retrieval_logs` | 检索日志 |
| `conversations` | 对话列表 |
| `messages` | 消息（含归档标记） |
| `message_references` | 消息引用 |
| `model_usage_logs` | 模型用量日志 |
| `dialogs` | Dialog 对话配置 |
| `workflows` | Workflow 定义 |
| `workflow_versions` | Flow Version (DSL JSON) |
| `workflow_runs` | 运行记录（含调用链追踪） |
| `workflow_run_events` | SSE 运行时事件 |
| `workflow_node_logs` | 节点执行日志 |
| `workflow_run_steps` | 运行步骤 |
| `workflow_profiles` | Profile 配置（Mode/Tool Packs/MCP/Memory/Context/Output Schema/Risk Level） |
| `workflow_eval_datasets` | 评估数据集 |
| `workflow_eval_cases` | 评估用例 |
| `workflow_eval_runs` | 评估运行记录 |
| `workflow_eval_results` | 评估结果 |
| `approval_requests` | 审批请求 |
| `workflow_checkpoints` | 运行检查点（暂停/恢复 + 哈希校验） |
| `memories` | 记忆（含 embedding + importance + dream 合并） |
| `memory_write_logs` | 记忆写入日志 |
| `memory_merge_logs` | 记忆合并日志 |
| `memory_extraction_jobs` | 记忆提取任务 |
| `tool_definitions` | 工具定义 |
| `tool_invocations` | 工具调用记录 |
| `tool_policies` | 工具策略（超时/截断/白名单/风险级别） |
| `tool_packs` | 工具包 |
| `tool_pack_items` | 工具包内条目 |
| `mcp_servers` | MCP 服务器注册 |
| `mcp_tool_cache` | MCP 工具缓存 |
| `skills` | Skill 技能定义（SHA256 checksum 追踪） |
| `workflow_teams` | 团队（Crew AI） |
| `workflow_team_members` | 团队成员 |

---

## 快速开始

```bash
cp configs/config.local.yaml.example configs/config.local.yaml
make docker-up
make migrate
make dev
open http://localhost:8080
```

## 常用命令

```bash
make dev              # 启动完整本地开发链路
make run              # 迁移 + 前端构建 + API 启动
make build            # 生产构建（含迁移表完整性校验）
make worker           # 启动文档解析 Worker
make migrate          # 运行数据库迁移
make lint             # go vet + gofmt + typecheck + build dry-run
make verify           # 迁移表完整性验证 + go vet + build check
make test             # 运行 Go 测试
make test-web         # 运行前端测试
make typecheck-web    # 前端 TypeScript 类型检查
make fmt              # 自动格式化 Go 代码
make clean            # 清理构建产物
make docker-up/down   # 管理本地 Docker 依赖
```

## 配置说明

默认读取 `configs/config.local.yaml`，不存在时回退 `configs/config.yaml`。可通过环境变量指定：

```bash
export AGENTCANVAS_CONFIG_PATH=/path/to/config.yaml
```

## 主要 API

### 认证与平台

```text
POST   /api/v1/auth/register              注册
POST   /api/v1/auth/login                 登录
POST   /api/v1/auth/refresh               刷新 Token
POST   /api/v1/auth/logout                登出
GET    /api/v1/auth/me                    当前用户
POST   /api/v1/auth/oauth/exchange        OAuth Code 交换

GET    /api/v1/model-providers            供应商列表
POST   /api/v1/model-providers            创建供应商
POST   /api/v1/model-providers/:id/test   测试供应商

GET    /api/v1/api-tokens                 API Token
GET    /api/v1/audit-logs                 审计日志
```

### 知识库与检索

```text
POST   /api/v1/knowledge-bases                        创建知识库
POST   /api/v1/knowledge-bases/:id/search             Keyword/Vector/Hybrid 检索
POST   /api/v1/knowledge-bases/:id/reindex            重建索引
POST   /api/v1/knowledge-bases/:id/documents          上传文档
GET    /api/v1/documents/:id/chunks                   文档切片
GET    /api/v1/ingestion-jobs/:id                     解析任务状态
```

### RAG Chat

```text
POST   /api/v1/dialogs                                创建 Dialog
POST   /api/v1/dialogs/:dialog_id/rag/chat            RAG 问答
POST   /api/v1/dialogs/:dialog_id/rag/chat/stream     RAG 流式问答 (SSE)
```

### Workflow / Agent Flow / Eval / Team

```text
POST   /api/v1/workflows                             创建 Workflow
GET    /api/v1/workflows/:id/profile                  Profile 配置
POST   /api/v1/workflows/:id/flow-versions            创建 Flow Version
POST   /api/v1/flow-versions/:id/publish              发布
POST   /api/v1/flow-versions/:id/validate             校验
POST   /api/v1/workflows/:id/runs                     启动运行
POST   /api/v1/workflows/:id/runs/stream              SSE 流式运行
POST   /api/v1/runs/:id/pause                         暂停 (→ Checkpoint)
POST   /api/v1/runs/:id/resume                        恢复 (Checkpoint → Resume)
GET    /api/v1/runs/:id/trace                         运行追踪
POST   /api/v1/workflows/:id/eval-datasets            创建评估数据集
GET    /api/v1/eval-datasets/:id/trend                评估趋势
GET    /api/v1/approval-requests                      审批请求
POST   /api/v1/workflow-teams                         创建 Team
```

### Memory / Tool / Skill / MCP

```text
GET    /api/v1/memories                              记忆列表
GET    /api/v1/tool-definitions                      工具定义
POST   /api/v1/tool-definitions/:id/test             测试工具
GET    /api/v1/skills                                Skill 列表
POST   /api/v1/skills                               创建 Skill
POST   /api/v1/skills/:id/validate                   校验 Skill
GET    /api/v1/tool-policies                         工具策略
GET    /api/v1/tool-packs                            工具包
GET    /api/v1/mcp-servers                           MCP 服务器
POST   /api/v1/mcp-servers/:id/refresh               刷新 MCP 工具缓存
```

---

## 向量检索配置

知识库支持 `keyword` / `vector` / `hybrid` 三种检索模式。HNSW 索引参数：M=16、EFConstruction=200、Cos-sim 度量。启用向量检索前需配置 Provider 和 embedding model。修改 Embedding 配置后需重建索引。

---

## 前端独立开发

```bash
npm --prefix web run dev        # Vite dev server (HMR, :5173)
npm --prefix web run build      # 生产构建
npm --prefix web run typecheck  # TypeScript 类型检查
npm --prefix web test -- --run  # 单元测试
```

---

## Docker 部署

```bash
docker compose -f deployments/docker-compose.yml up -d
docker compose -f deployments/docker-compose.dev.yml up -d
docker build -f deployments/docker/Dockerfile -t agentcanvas .
docker run -p 8080:8080 -v $(pwd)/configs:/app/configs agentcanvas
```
