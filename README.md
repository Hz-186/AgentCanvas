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

Agent 的执行能力不仅仅取决于模型本身，更取决于包裹在模型外层的 **Harness 四层壳**。这一设计将一个字节未改的模型通过更精巧的壳，使执行通过率出现显著提升，验证了 **Agent 的核心竞争力在于 Harness 而非 Model** 的设计哲学。

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
- **Commands**：流程入口，控制 Agent 的执行生命周期（run > plan > execute > judge > resume）
- **Skills**：领域能力封装，流程型 Skill 通过 `disable-model-invocation` 抑制模型推理，零额外推理 token 消耗
- **Rules**：前馈约束，基于 MCTS 剪枝思想的分级注入，确保进入模型的上下文是精选而非全量
- **Hooks**：反馈兜底，`preToolUse` / `postToolUse` 双环拦截，覆盖危险命令物理阻断

### Harness 双环控制：Rules + Hooks

#### Rules 规则分级体系（前馈约束）

将蒙特卡洛树搜索（MCTS）的剪枝思想应用于规则分级管理，实现五级分级注入：

| 级别 | 类型 | 策略 | 说明 |
|------|------|------|------|
| L0 | Safety | 永久硬挂载 | 安全规则，不可被剪枝 |
| L1 | Core | 永久硬挂载 | 核心行为约束，永久剪掉明显错误分支 |
| L2 | Scenario | 按需加载 | 场景规则，激活信号驱动，token 预算内按分数加载 |
| L3 | Tool | 按需加载 | 工具级别规则，工具使用时才激活 |
| L4 | Ephemeral | 低优先按需 | 临时/实验性规则，最低得分，预算不足时首批裁剪 |

每条规则可配置激活信号（`mode_any`/`tool_all`/`keywords_any`/`risk_any`）和排除信号（`exclude_modes`/`exclude_tools`），配合 token 预算控制和分数截止，实现**信息熵压缩**——通过 `level_budgets` 和 `score_cutoff` 精确控制每个级别注入上下文的信息量，避免低质量规则浪费上下文窗口。

在场景信号精准匹配 + 分层预算控制下，32K 窗口模型的可用空间从 8.5K tokens 翻到 17K tokens。

#### Hooks 拦截链（反馈兜底）

`preToolUse` 和 `postToolUse` 双环 Hook 链，覆盖：

- **危险命令物理拦截**：检测 `rm -rf /`、`mkfs.`、`dd if=`、`fork bomb`、`chmod 777 /` 等 40+ 种危险模式，在工具调用前直接拒绝
- **风险审批流**：高/中风险工具（`shell`、`python_execute`、`http_request` 等）触发人类审批，支持暂停/恢复/拒绝三部曲
- **主机白名单**：`allowed_hosts` 策略限制 HTTP Tool 只能访问白名单内的主机
- **敏感字段脱敏**：`api_key`、`authorization`、`password`、`secret` 等字段自动 `[REDACTED]`
- **观测摘要压缩**：工具输出超过 `max_tool_output_bytes` 时自动截断+去冗余

### Skills 领域能力封装体系

将可复用的 Agent 能力封装为标准化的 Skill 模块，屏蔽底层复杂度：

- **流程型 Skill**：通过 `disable-model-invocation` 标记抑制 LLM 推理，纯逻辑 Pipeline 执行，该类型任务实现零额外推理 token 消耗
- **工具型 Skill**：封装 Knowledge Base + Tool + Memory 的组合能力，一次调用即可完成复杂的多步操作
- **Skill 校验**：每个 Skill 支持运行时验证（`POST /skills/:id/validate`），确保 DSL 的正确性

### ReAct Agent Loop 与 DAG Flow 执行引擎

支持两种执行模式：

- **DAG Flow 模式**：基于有向无环图的声明式节点编排，支持 23 种节点类型（LLM、Prompt、Retrieval、Memory、HTTP Tool、MCP Tool、Switch、CodeSandbox、WorkflowCall、TeamCall、AgentLoop 等）
- **ReAct Agent Loop 模式**：完整的 Agent 循环，包含 Planner（规划器）、Supervisor（监督者）、Judge（判断器）、Resumer（恢复器）

执行引擎支持：
- 嵌套 Workflow 调用（`call_depth` + `parent_run_id` 追踪调用链）
- Workflow Profiles 个性化配置（Tool Packs、MCP Servers、Memory Policy、Context Policy、Risk Level）
- 暂停/恢复/取消运行，审批流程集成
- SSE 实时流式事件推送 + JSON 数据流解析

### 上下文压缩引擎

为解决长对话的上下文窗口压力，实现了多层上下文压缩策略：

- **滑动窗口压缩**：基于重要性评分的窗口滑动机制，保留高信息密度片段
- **同质化信息压缩**：通过文本指纹（MD5 simhash 变体）检测重复/近似内容，`DiversityLambda` 控制去重力度
- **熵驱动摘要**：对删除区域按 token 预算生成压缩摘要而非直接丢弃，摘要优先保留高 `salience` + `keySignal` 片段
- **TextFeatures 向量**：为每个 corpus item 构建 token 频率特征，计算 `approximateSimilarity` 实现 O(n) 冗余检测

### LLM 双层缓存

L1（Redis 精确匹配）+ L2（RediSearch/向量语义匹配）双层缓存架构：

- **L1 层**：SHA256 哈希精确匹配，命中率最高，延迟最低（Redis GET，< 1ms）
- **L2 层**：语义向量相似匹配，通过 `EmbeddingModel` 将用户 query 向量化后在 L2 Store 中查 HNSW 索引，命中后回注 L1 提升后续命中
- **L2 Store**：支持 Milvus / Redis Stack（RediSearch 向量索引）两种后端，使用 HNSW 算法实现高效近似最近邻检索
- 支持 `similarity_threshold`（默认 0.96）和 TTL 过期控制

### RAG 知识库与检索

**知识库全生命周期管理**：

- txt / md 文件上传到 MinIO，Worker 异步队列消费
- **DeepDoc 深度文档解析引擎**：PDF 布局分析（段落/标题/表格识别）、表格提取（几何分析 + 直角聚类）、K-Means 聚类（自动识别 word-level / line-level / block-level 三种文本粒度）、乱码检测与修复
- **FixedTokenChunker**：基于 token 计数的固定窗口切片，保证每个 chunk 不超过模型上下文限制
- **RecursiveChunker**：递归切片器，按段落→句子→词组的层级递归切分
- 支持异步解析任务跟踪（`ingestion_jobs` + `GET /ingestion-jobs/:id`）

**RAG 双召回架构**（检索技术核心在于召回链路设计）：

| 召回模式 | 实现方式 | 适用场景 |
|---------|---------|---------|
| **Keyword** | Elasticsearch `multi_match` + BM25 评分 | 精确词匹配、专有名词、短 query |
| **Vector** | Milvus / RediSearch HNSW + Embedding 模型语义检索 | 语义相似、长文理解、同义改写 |
| **Hybrid** | BM25 + kNN 分数融合（加权求和） | 通用场景，兼顾精确匹配和语义覆盖 |

- 支持 BGE-Reranker（`bge-reranker-v2-m3`）做检索结果重排序，提升 Top-K 精度
- Rerank 失败时自动降级返回原始排序，不阻塞检索流程
- 知识库重建索引（`POST /reindex`），支持 Embedding 模型和维度切换后的一键重索引
- 检索日志完整追踪（`retrieval_logs`）

### 记忆系统

三层分级记忆系统：

| 级别 | 存储 | 生命周期 | 说明 |
|------|------|---------|------|
| **Working Memory** | Redis 滑动窗口 | 单次 Agent Loop | 工具调用上下文、临时变量、中间结果 |
| **Short-term Memory** | MySQL + 向量 Embedding | 对话会话级别 | 当前 session 内的关键信息提取 |
| **Long-term Memory** | MySQL + 向量 Embedding | 跨会话持久化 | 用户偏好、历史决策、知识积累 |

- **Dream（记忆梦境）机制**：定时 Job 遍历近期记忆，通过 LLM 自动提取和合并，支持冲突检测（`conflict_flag`）去重
- `memory_level`（importance）分级 + `access_count` + `consolidation_count` 生命周期管理
- 向量化检索：支持通过 Embedding 模型将记忆向量化后按相似度检索
- `memory_merge_logs` + `memory_extraction_jobs` 完整可追踪

### 评估体系（EvalHarness）

- 数据集管理（`workflow_eval_datasets` + `workflow_eval_cases`）
- 批量评估运行（`workflow_eval_runs`），支持自动评分和 Judge 模型评分
- 评估趋势追踪（`GET /eval-datasets/:id/trend`）
- 评估指标：准确率、召回率、F1、Latency、Token 成本

### Guardrail & 工具体系

- **Tool Policy**：基于风险等级的细粒度工具策略（超时、输出截断、主机白名单、风险审批）
- **Tool Packs**：工具包（`tool_packs` + `tool_pack_items`），一键注入多工具组合
- **MCP 协议支持**：`mcp_servers` + `mcp_tool_cache`，支持 MCP Server 注册与工具缓存刷新
- **Code Sandbox**：隔离执行 Python/Shell 代码，危险命令物理拦截（`dangerousToolArgumentReason` 40+ 模式）
- **工具注册表**：`ToolRegistry` 管理所有注册的工具，支持动态注入和懒加载

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
    skill_usecase/           ─ Skill 技能管理
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
    auth/                    ─ 认证
    conversation/            ─ 对话/消息/引用
    dialog/                  ─ Dialog 会话
    tool/                    ─ 工具定义/MCP
    skill/                   ─ Skill 定义
    usage/                   ─ 模型用量
    user/                    ─ 用户
    audit/                   ─ 审计日志
    flow/                    ─ DSL v1 定义
  infrastructure/           基础设施层 (16 个包)
    mysql/                   ─ GORM 仓库实现 (30+ Repository)
    redis/                   ─ Redis 客户端/RediSearch/Memory Cache/WorkingMemory
    elasticsearch/           ─ ES 客户端及索引管理
    minio/                   ─ MinIO 对象存储
    vectorstore/             ─ Milvus + Redis Stack 向量存储
    retrieval/               ─ ES 检索/Milvus 检索/Composite 组合/Memory 检索
    llm/                     ─ Chat/Embeddings/Rerank/LLM Cache
    deepdoc/                 ─ PDF 解析: 布局/表格/K-Means/乱码
    parser/                  ─ 文档解析器注册表
    chunker/                 ─ FixedTokenChunker/RecursiveChunker
    queue/                   ─ MySQL Queue/Redis Stream/NATS JetStream
    catalog/                 ─ 供应商预置目录加载
    crypto/                  ─ AES-GCM/JWT/bcrypt
    oauth/                   ─ GitHub OAuth
    job/                     ─ Memory Dream 定时调度
  interface/http/           HTTP 接口层
    handler/                 ─ 12 个 Handler (Auth/OAuth/Provider/Knowledge/Document/Dialog/Chat/Workflow/Memory/Tool/Skill/Audit)
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
    agent/                   ─ Agent Loop: Runner/Planner/Supervisor/Judge/Resumer/ContextAssembler
    engine/                  ─ Flow DAG 执行器/Node 接口/VariableResolver
    harness/                 ─ 评估框架: Rules (L0-L4 分级)/Hooks (preToolUse/postToolUse)
    node/                    ─ 23 种节点实现
    toolruntime/             ─ 工具运行时: HTTP Tool/Knowledge Tool/Memory Tool/MCP/Sandbox/Skill/Workflow Call
    evalharness/             ─ 评估指标 (自动评分/Judge)
    sandbox/                 ─ 代码沙箱
    contextcompress/         ─ 上下文压缩: 滑动窗口/熵压缩/同质化去重/指纹
    event/                   ─ 运行时事件定义
migrations/                 数据库迁移 SQL (25 组, .up.sql + .down.sql)
scripts/                    本地脚本
  dev.sh                     ─ 完整开发链路启动
  migrate.sh                 ─ 数据库迁移
  lint.sh                    ─ lint + go vet + gofmt + typecheck
  build.sh                   ─ 生产构建 (迁移/前端/Go 编译)
  verify.sh                  ─ 迁移表完整性验证 + go vet + build check
web/                        React + Vite 前端 (SPA)
  src/
    pages/                   ─ 页面组件 (Login/Knowledge/Workbench/Agent Flow...)
    components/              ─ 通用组件
    stores/                  ─ Zustand 状态管理
    api/                     ─ API 调用层
    types/                   ─ TypeScript 类型定义
    utils/                   ─ 工具函数
  embed.go                   ─ 内嵌前端 dist
skill/                       Skill 预设
  explain-knowledge/
  record-knowledge/
```

---

## 数据库表清单（45 张表）

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
| `workflow_profiles` | Profile 配置（Tool Packs/MCP/Memory/Context/Output Schema） |
| `workflow_eval_datasets` | 评估数据集 |
| `workflow_eval_cases` | 评估用例 |
| `workflow_eval_runs` | 评估运行记录 |
| `workflow_eval_results` | 评估结果 |
| `approval_requests` | 审批请求 |
| `workflow_checkpoints` | 运行检查点（暂停恢复） |
| `memories` | 记忆（含 embedding + importance） |
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
| `skills` | Skill 技能定义 |
| `workflow_teams` | 团队（Crew AI） |
| `workflow_team_members` | 团队成员 |

---

## 快速开始

```bash
# 复制本地配置
cp configs/config.local.yaml.example configs/config.local.yaml

# 启动 Docker 依赖（MySQL/Redis/MinIO/ES/Kibana/Milvus/etcd/NATS）
make docker-up

# 运行迁移
make migrate

# 启动完整开发链路（依赖 → 迁移 → 前端构建 → Worker + API）
make dev

# 访问
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
GET    /api/v1/auth/github/redirect       GitHub OAuth 跳转
GET    /api/v1/auth/github/callback       GitHub OAuth 回调

GET    /api/v1/model-providers            供应商列表
POST   /api/v1/model-providers            创建供应商
PATCH  /api/v1/model-providers/:id        更新供应商
DELETE /api/v1/model-providers/:id        删除供应商
POST   /api/v1/model-providers/:id/test   测试供应商

GET    /api/v1/api-tokens                 API Token 列表
POST   /api/v1/api-tokens                 创建 Token
DELETE /api/v1/api-tokens/:id             删除 Token
GET    /api/v1/audit-logs                 审计日志
```

### 知识库与检索

```text
POST   /api/v1/knowledge-bases                        创建知识库
GET    /api/v1/knowledge-bases                        知识库列表
GET    /api/v1/knowledge-bases/:id                    知识库详情
PATCH  /api/v1/knowledge-bases/:id                    更新知识库
DELETE /api/v1/knowledge-bases/:id                    删除知识库
POST   /api/v1/knowledge-bases/:id/search             Keyword/Vector/Hybrid 检索
POST   /api/v1/knowledge-bases/:id/reindex            重建索引
POST   /api/v1/knowledge-bases/:id/documents          上传文档
GET    /api/v1/knowledge-bases/:id/documents          文档列表
GET    /api/v1/documents/:id                          文档详情
PATCH  /api/v1/documents/:id                          设置文档启用/停用
DELETE /api/v1/documents/:id                          删除文档
GET    /api/v1/documents/:id/chunks                   文档切片列表
GET    /api/v1/ingestion-jobs/:id                     解析任务状态
```

### RAG Chat

```text
POST   /api/v1/dialogs                                创建 Dialog
GET    /api/v1/dialogs                                Dialog 列表
GET    /api/v1/dialogs/:id                            Dialog 详情
PATCH  /api/v1/dialogs/:id                            更新 Dialog
DELETE /api/v1/dialogs/:id                            删除 Dialog
POST   /api/v1/dialogs/:dialog_id/rag/chat            RAG 问答
POST   /api/v1/dialogs/:dialog_id/rag/chat/stream     RAG 流式问答 (SSE)
GET    /api/v1/dialogs/:dialog_id/conversations       对话列表
GET    /api/v1/dialogs/:dialog_id/conversations/:id   对话详情
DELETE /api/v1/dialogs/:dialog_id/conversations/:id   删除对话
```

### Workflow / Agent Flow / Eval / Team

```text
POST   /api/v1/workflows                             创建 Workflow
GET    /api/v1/workflows                             列表
GET    /api/v1/workflows/:id                          详情
PATCH  /api/v1/workflows/:id                          更新
DELETE /api/v1/workflows/:id                          删除
GET    /api/v1/workflows/:id/profile                  Profile 配置
PATCH  /api/v1/workflows/:id/profile                  更新 Profile

POST   /api/v1/workflows/:id/flow-versions            创建 Flow Version
GET    /api/v1/workflows/:id/flow-versions            Version 列表
POST   /api/v1/flow-versions/:id/publish              发布 Version
POST   /api/v1/flow-versions/:id/validate             校验 Version

POST   /api/v1/workflows/:id/runs                     启动运行
POST   /api/v1/workflows/:id/runs/stream              SSE 流式运行
GET    /api/v1/runs/:id                               运行状态
POST   /api/v1/runs/:id/cancel                        取消
POST   /api/v1/runs/:id/pause                         暂停
POST   /api/v1/runs/:id/resume                        恢复
GET    /api/v1/runs/:id/events                        SSE 事件列表
GET    /api/v1/runs/:id/node-logs                     节点日志
GET    /api/v1/runs/:id/steps                         运行步骤
GET    /api/v1/runs/:id/trace                         运行追踪
GET    /api/v1/runs/:id/children                      子运行列表

POST   /api/v1/workflows/:id/eval-datasets            创建评估数据集
GET    /api/v1/workflows/:id/eval-datasets            数据集列表
POST   /api/v1/eval-datasets/:id/cases                创建评估用例
POST   /api/v1/eval-datasets/:id/runs                 启动评估运行
GET    /api/v1/eval-datasets/:id/trend                评估趋势

GET    /api/v1/approval-requests                      审批请求列表
POST   /api/v1/approval-requests/:id/approve          通过
POST   /api/v1/approval-requests/:id/reject           拒绝

POST   /api/v1/workflow-teams                         创建 Team
GET    /api/v1/workflow-teams                         列表
POST   /api/v1/workflow-teams/:id/members             添加成员
```

### Memory / Tool / Skill / MCP

```text
GET    /api/v1/memories                              记忆列表
POST   /api/v1/memories                              创建记忆
PATCH  /api/v1/memories/:id                          更新记忆
DELETE /api/v1/memories/:id                          删除记忆

GET    /api/v1/tool-definitions                      工具定义列表
POST   /api/v1/tool-definitions                      创建工具
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

知识库支持 `keyword` / `vector` / `hybrid` 三种检索模式。启用向量检索前，需要：
1. 在 Provider 设置页创建支持 OpenAI-compatible `/v1/embeddings` 的供应商
2. 在知识库设置中配置 embedding model / dimensions / hybrid_weight
3. 可选：配置 Rerank Provider（如 `bge-reranker-v2-m3`）实现重排序

修改 Embedding 配置后需要重建索引：`POST /api/v1/knowledge-bases/:id/reindex`

---

## 前端独立开发

```bash
npm --prefix web run dev        # 启动 Vite dev server（HMR, 默认 :5173）
npm --prefix web run build      # 生产构建到 web/dist/
npm --prefix web run typecheck  # TypeScript 类型检查
npm --prefix web test -- --run  # 运行单元测试
```

---

## Docker 部署

```bash
# 本地开发环境
docker compose -f deployments/docker-compose.yml up -d
docker compose -f deployments/docker-compose.dev.yml up -d

# 生产构建
docker build -f deployments/docker/Dockerfile -t agentcanvas .
docker run -p 8080:8080 -v $(pwd)/configs:/app/configs agentcanvas
```
