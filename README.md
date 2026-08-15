# AgentCanvas

AgentCanvas 是一个以 Agent 为唯一执行单元的开源运行平台，面向类似 Codex CLI、OpenCode、Hermes 与 OpenClaw 的产品形态。后端使用 Go，前端使用 React；核心能力包括持久化 Agent Loop、会话与事件流、动态子 Agent、RAG、Memory、Reflection、Tools、MCP、Skills、审批和检查点恢复。

项目当前只维护 Agent Runtime。发布后的 Agent Release 会直接转换为强类型运行请求，由 Agent Loop 统一调度工具、Memory、RAG、Skills 和动态 Subagent。

## 核心能力

- Agent：草稿配置、不可变 Release、会话、Turn、Run 和运行步骤。
- Agent Loop：支持 `react` 与 `plan_execute`，统一处理上下文、工具调用、停止条件和结构化输出。
- Dynamic Subagents：模型通过 `run_subagent` 动态委派任务，使用 `parent_run_id`、`run_type` 和 `delegation_depth` 记录父子关系。
- RAG：知识库、文档解析、Keyword/Vector/Hybrid 检索、Rerank 与引用。
- Context Index：将 Reflection、Memory、Skill、Tool 和 Conversation Message 统一写入语义索引。
- Memory：User、Agent、Conversation 三种作用域，支持候选审核、召回反馈、衰减和异步提取。
- Reflection：Agent/Global 两种作用域，支持召回、异步轨迹分析、证据、状态管理与 helpful/harmful 反馈。
- Tools：HTTP Tool、MCP、Memory Tool、Skill Tool、Sandbox 和动态子 Agent 工具。
- Skills：支持内联 Markdown 与本地 Bundle，包含校验、版本、校验和与按需加载。
- Human in the Loop：高风险工具触发审批，完整快照持久化后可批准、拒绝或恢复。
- Self Improvement：可选的 Turn Review、Change Proposal 与人工批准应用。
- Git Workspace：Project 绑定受控仓库；Run 可使用 shared checkout 或独立 worktree，worktree 子 Agent 自动使用 sibling checkout，并以 fail-safe 策略保留 dirty、unpushed 或锁状态不明的工作区。

## 运行架构

```text
HTTP / SPA / future Gateway
            │
            ▼
      Agent Usecase
  Conversation / Turn / Run
            │
            ▼
       Agent Runtime
  Context → Model → Tool → Result
     │          │          │
     │          │          ├─ Approval / Checkpoint
     │          │          └─ Run Event / Run Step
     │          └─ run_subagent → Agent Runtime
     ├─ RAG / Context Index
     ├─ Memory / Reflection
     └─ Skills / MCP / Sandbox
```

`agent_usecase.Service` 负责运行创建、Worker、事件、步骤、取消、审批、恢复和父子运行。`agentruntime.AgentRuntime` 是唯一执行端口；Turn 与 Subagent 使用同一套资源解析、规则、工具、Memory、RAG、Reflection 和安全策略。

### Run 模型

每次执行写入以下核心表：

| 表 | 用途 |
| --- | --- |
| `agent_runs` | 运行状态、Agent、Release、会话、父运行、类型与委派深度 |
| `agent_run_events` | 可重连 SSE 事件 |
| `agent_run_steps` | LLM、Tool、Approval、Reflection 与 Final Answer 步骤 |
| `agent_run_checkpoints` | 完整运行快照、消息、交互、工具和规则哈希 |
| `agent_approval_requests` | 人工审批请求与决定 |

`run_type` 只有 `turn` 与 `subagent`。动态委派由 `max_subagent_depth` 和并行限制保护；子运行直接递归进入 Agent Runtime，不生成临时 DSL。

### Agent Loop

```text
组装上下文
  ├─ System / Conversation
  ├─ RAG / Context Index
  ├─ Memory / Reflection
  └─ Rules / Skills
        │
        ▼
      LLM Call
        ├─ Final Answer ────────────────┐
        └─ Tool Calls                   │
              ├─ Pre Hook              │
              │    ├─ Deny             │
              │    └─ Approval → Save Checkpoint
              ├─ Runtime Tool           │
              └─ Post Hook              │
                    │                   │
                    └──── Next Turn ────┘
```

规则随 Agent Release 固化为不可变快照，并通过 `rule_hash` 校验。平台强制规则始终加载；Agent 自定义规则按激活条件、优先级和 token 预算选择。工具 Hook 在模型之外执行危险参数拦截、风险审批、主机限制、超时、输出截断和敏感字段脱敏。

### 审批与恢复

运行需要人工决定时：

1. Runtime 生成 Approval 与完整 Checkpoint。
2. Agent Usecase 持久化消息、步骤、交互 ID、规则哈希、工具注册表哈希和工具策略哈希。
3. Run 进入 `waiting_human` 或 `paused`。
4. 批准或拒绝操作与恢复抢占在同一个 MySQL 事务中完成。
5. Resume 校验规则和工具快照，然后继续未完成的 Tool Call。

### RAG 与统一上下文

知识库支持 `keyword`、`vector`、`hybrid` 三种检索模式。Elasticsearch 负责全文检索，Milvus 可用于向量检索。统一上下文索引按 `owner_id`、`agent_id` 与 `conversation_id` 隔离，并使用 Outbox、lease、重试和 dead letter 保证最终一致性。

Embedding provider、model、dimensions 与 profile hash 会被持久化；不同向量空间不会混用。

### Memory 与 Reflection

Memory 只允许以下作用域：

- `user`：用户长期偏好与事实。
- `agent`：单个 Agent 的经验与知识。
- `conversation`：单个会话的上下文记忆。

成功 Agent Turn 可触发异步 Memory 提取；Redis 对同一会话的短时突发进行合并。候选内容在批准前不会直接修改活跃记忆。

Reflection 与事实记忆分离，只使用 `agent` 和 `global` 作用域。运行前召回相关经验，运行后由异步 Worker 分析轨迹；证据、置信度、状态与用户反馈独立演化。

## 技术栈

### 后端

| 类别 | 技术 |
| --- | --- |
| Language | Go 1.22 |
| HTTP | Gin |
| ORM / DB | GORM / MySQL 8.4 |
| Cache | Redis 7.2 |
| Search | Elasticsearch 8.15 |
| Vector | Milvus 2.5（可选） |
| Object Storage | MinIO |
| Queue | MySQL / Redis Stream / NATS JetStream |
| Security | AES-GCM、bcrypt、JWT |

### 前端

| 类别 | 技术 |
| --- | --- |
| UI | React 18 + TypeScript |
| Build | Vite 5 |
| Router | React Router 6 |
| State | Zustand 5 |
| Test | Vitest + Testing Library |

默认前端入口为 `/app/agents`。未知 SPA 地址统一回到 Agent 首页；未知 `/api/` 地址返回 JSON 404。

## 目录结构

```text
cmd/
  api/                    HTTP Server 与内嵌 SPA
  worker/                 文档、上下文、Memory、Reflection Worker
  migrate/                数据库迁移入口
  backfill-context-index/ 统一上下文索引登记工具
internal/
  application/            Agent、RAG、Memory、Reflection、Tool、Skill 用例
  bootstrap/              依赖装配与 Worker 启动
  domain/                 Agent、Conversation、Memory、Reflection 等领域模型
  infrastructure/         MySQL、Redis、ES、Milvus、MinIO、Queue、LLM
    pythonbridge/          Python Bridge gRPC 客户端与 Chunker/Tool 适配
  interface/http/         Handler、Middleware、SSE 与 Router
  runtime/
    agent/                Runner、Planner、Resumer、Context Assembler
    agentruntime/         唯一 Agent 执行入口与资源装配
    harness/              Rules 与 Tool Hooks
    toolruntime/          RuntimeTool、MCP、Skills、Memory、Subagent
    sandbox/              Docker 代码沙箱
    conversationcontext/  会话压缩与滚动快照
migrations/               单一 Agent-only 数据库基线
proto/                    Python Bridge Protobuf v1 合约
python/                   Python 常驻侧车、工具与切片实现
web/                      React SPA
```

## 快速开始

要求：Go 1.22+、Node.js、npm、Docker Desktop。

```bash
cp configs/config.local.yaml.example configs/config.local.yaml
make docker-up
make migrate
make dev
```

打开 `http://localhost:8080/app/agents`。

如需用 Compose 同时运行 API、Worker 与 Workspace Pruner，必须显式选择一个宿主机仓库根目录，并让三个服务以相同路径挂载：

```bash
export AGENTCANVAS_WORKSPACE_ROOT="/Users/your-name/Projects"
docker compose -f deployments/docker-compose.yml -f deployments/docker-compose.dev.yml up -d
```

Docker 内的 Project 路径应填写为 `/workspaces/<repo>`；不要把用户仓库放入临时 Sandbox。AgentCanvas 不会自动 commit、push 或 merge，worktree branch 默认保留供人工审查。

Python Bridge 默认关闭。需要试用 Python 工具或切片时，在启动 Compose 前设置同一进程级令牌，并将 `configs/config.local.yaml` 的 `python_bridge.enabled` 改为 `true`。Python 切片还必须在 shadow 基准达标并人工审阅后显式打开 `python_bridge.allow_experimental_chunking`：

```bash
export AGENTCANVAS_PYTHON_BRIDGE_TOKEN="$(openssl rand -hex 32)"
make docker-up
```

本地 Go 进程使用 `127.0.0.1:50051`；Compose 内 API/Worker 使用 `python-bridge:50051`。知识库可显式选择 `python:recursive` 或 `python:fixed_token`，Agent 设置通过 `python_tool_names` 选择全局 `allowed_tools` 白名单内的 Python 工具。

本地运行 Python 测试前可创建独立虚拟环境：

```bash
python3 -m venv .venv
source .venv/bin/activate
python -m pip install -r python/requirements-dev.txt
make test-python
```

侧车运行后可执行 `make benchmark-python`，它读取固定 fixture 并输出跨语言边界、p50/p95、分配量及 Recall@K/Precision@K；设置 `AGENTCANVAS_ELASTICSEARCH_URL` 时会额外对真实 Elasticsearch 索引测量，否则使用确定性的本地检索代理。

评估新切片策略时可同时设置 `python_bridge.shadow_enabled: true`。Worker 会保留 Go 的 `fixed_token`/`recursive` 结果，只限时调用 Python 并输出边界、token、元数据和延迟对比日志；shadow 失败不会污染入库结果。

数据库基线不提供旧数据升级能力。部署和本地切换必须使用空 MySQL 数据库，并重新初始化本项目的 Redis key、Elasticsearch index 与 Milvus collection。不要对无法确认归属的外部实例执行清理。

## 常用命令

```bash
make dev                    # 本地开发
make run                    # 迁移、构建前端并启动 API
make worker                 # 启动异步 Worker
make migrate                # 执行数据库基线
make backfill-context-index # 登记统一上下文索引 Outbox
make test                   # Go 测试
make test-python            # Python Bridge 测试
make benchmark-python       # 真实侧车跨语言基准（需设置 Bridge 环境变量）
make test-web               # 前端测试
make typecheck-web          # TypeScript 检查
make lint                   # 静态检查
make verify                 # 表结构、vet 与构建检查
make build                  # 生产构建
make docker-up              # 启动本地依赖
make docker-down            # 停止本地依赖
```

前端独立命令：

```bash
npm --prefix web run dev
npm --prefix web run typecheck
npm --prefix web test -- --run
npm --prefix web run build
```

## 主要 API

### Agent 与会话

```text
POST   /api/v1/agents
GET    /api/v1/agents
GET    /api/v1/agents/:id
PATCH  /api/v1/agents/:id
PATCH  /api/v1/agents/:id/settings
DELETE /api/v1/agents/:id

POST   /api/v1/agents/:id/conversations
GET    /api/v1/agents/:id/conversations
PATCH  /api/v1/agents/:id/conversations/:conversation_id/mode
GET    /api/v1/agents/:id/conversations/:conversation_id/messages
POST   /api/v1/agents/:id/conversations/:conversation_id/turns
GET    /api/v1/agents/:id/conversations/:conversation_id/turns/latest
POST   /api/v1/agents/:id/conversations/:conversation_id/fork
POST   /api/v1/agents/:id/conversations/:conversation_id/upgrade
DELETE /api/v1/agents/:id/conversations/:conversation_id
GET    /api/v1/agent-turns/:id
```

### Run、审批与 Reflection

```text
GET    /api/v1/runs/:id
GET    /api/v1/runs/:id/events
GET    /api/v1/runs/:id/events/stream
GET    /api/v1/runs/:id/children
GET    /api/v1/runs/:id/steps
GET    /api/v1/runs/:id/trace
POST   /api/v1/runs/:id/cancel
POST   /api/v1/runs/:id/resume

GET    /api/v1/approval-requests
POST   /api/v1/approval-requests/:id/approve
POST   /api/v1/approval-requests/:id/reject

GET    /api/v1/agents/:id/reflections
PATCH  /api/v1/agents/:id/reflections/:reflection_id
POST   /api/v1/runs/:id/reflections/:reflection_id/feedback
```

### Project、Workspace 与 Git

```text
POST   /api/v1/projects
GET    /api/v1/projects
GET    /api/v1/projects/:id
PATCH  /api/v1/projects/:id
DELETE /api/v1/projects/:id
POST   /api/v1/projects/:id/folders
GET    /api/v1/projects/:id/folders
DELETE /api/v1/projects/:id/folders/:folder_id
GET    /api/v1/projects/:id/git/status
GET    /api/v1/projects/:id/git/branches
GET    /api/v1/projects/:id/git/worktrees

GET    /api/v1/runs/:id/workspace
GET    /api/v1/runs/:id/git/status
GET    /api/v1/runs/:id/git/diff
GET    /api/v1/runs/:id/git/log
POST   /api/v1/runs/:id/git/commit
POST   /api/v1/workspaces/:id/cleanup
POST   /api/v1/workspaces/:id/refresh
```

### RAG、Memory、Tools 与 Skills

```text
POST   /api/v1/knowledge-bases/:id/documents
POST   /api/v1/knowledge-bases/:id/search
POST   /api/v1/knowledge-bases/:id/reindex
GET    /api/v1/documents/:id/chunks

GET    /api/v1/memories
GET    /api/v1/memory-candidates
POST   /api/v1/memory-candidates/:id/approve
POST   /api/v1/memory-candidates/:id/reject
GET    /api/v1/memory-recall-logs

GET    /api/v1/tool-definitions
GET    /api/v1/tool-policies
GET    /api/v1/tool-packs
GET    /api/v1/mcp-servers
POST   /api/v1/mcp-servers/:id/refresh
GET    /api/v1/skills
POST   /api/v1/skills/:id/validate
```

## 配置

默认读取 `configs/config.local.yaml`，不存在时回退到 `configs/config.yaml`。也可通过环境变量指定：

```bash
export AGENTCANVAS_CONFIG_PATH="/absolute/path/to/config.yaml"
```

主要配置段：

- `agent_runtime`：Turn Worker、lease、自改进与 Review Model。
- `mysql`、`redis`、`queue`、`nats`：持久化、缓存与异步任务。
- `elasticsearch`、`milvus`、`context_index`：RAG 与统一上下文索引。
- `memory_dream`、`working_memory`：记忆提取与运行缓存。
- `reflection_queue`：Reflection Outbox、JetStream、lease 与 DLQ。
- `minio`、`ocr`：文档存储与解析。
- `python_bridge`：侧车开关、gRPC target、令牌环境变量、超时、消息/并发限制以及切片和工具白名单。
- `security`、`oauth`：密钥、Token 与 GitHub OAuth。
- `git_workspace`：允许的绝对仓库根目录、worktree 目录、Git/fetch 超时、输出与文件读取上限、prune TTL、自动初始化和 Git identity。API、Worker 与 Pruner 必须使用完全相同的 `allowed_roots` 与挂载。

## 多语言扩展边界

当前由 Go `agentruntime.AgentRuntime` 保留运行、审批、事件、Memory、Reflection 与 Checkpoint 的权威状态；Python Bridge 只提供窄领域 Chunker 和低风险 Tool。Python 不直接访问 AgentCanvas 数据库、对象存储、宿主机文件或用户密钥。

未来若引入 Python Agent Loop 或 LangGraph，应作为可选 Workflow Runtime 或远程子 Agent，继续复用 Go 的 Run/Event/Approval/Checkpoint 契约，不建立第二套持久化模型。详细计划见 `doc/python-bridge-plan.md`。
