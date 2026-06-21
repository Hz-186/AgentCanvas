# AgentCanvas

AgentCanvas 是一个用 Go + React 编写的单人版 Agent Flow + RAG 知识库项目。

当前项目已经完成 Phase 7：向量检索、Hybrid Search 与 Rerank。系统已经具备单人应用的平台壳子、模型配置能力、txt/md 文档上传、异步解析切片、ES BM25 / dense_vector 检索、Hybrid Search、可选 LLM Rerank、基于知识库上下文调用 OpenAI-compatible LLM 的普通问答能力、通过 JSON DSL 创建发布并运行 Agent Flow 的后端闭环，以及浏览器中的可视化工作台、React Flow 画布和 SSE 调试台。

## 当前阶段

Phase 7：向量检索、Hybrid Search 与 Rerank。

已经包含：

- Go API 服务入口
- 配置文件加载
- 基础日志
- MySQL 客户端
- Redis 客户端
- MinIO 客户端
- Elasticsearch 客户端
- 健康检查接口
- Docker Compose 本地依赖
- 数据库 migration 目录
- 用户注册和邮箱密码登录
- JWT access token 与 refresh token session
- GitHub OAuth 登录
- 当前用户信息接口
- API Token 创建、列表和撤销
- Provider 配置管理
- Provider API Key 加密存储与脱敏展示
- Provider 连通性测试
- 审计日志记录与查询
- 知识库创建、列表、详情、更新和删除
- txt/md 文档上传到 MinIO
- MySQL ingestion job 异步任务表
- ingestion job 失败重试与最大尝试次数控制
- Worker 轮询处理文档解析、切片和索引
- 基于估算 token 预算的 fixed-token 切片
- document chunks 保存到 MySQL
- chunks 同步写入 Elasticsearch
- 知识库关键词搜索和高亮返回
- 上传失败、重复处理和删除路径的状态一致性处理
- retrieval logs 检索日志记录
- 普通 RAG Chat 同步问答接口
- POST + text/event-stream 流式 RAG Chat 接口
- conversation、message、reference 持久化
- model usage 日志记录
- OpenAI-compatible /chat/completions 客户端
- DeepSeek、Qwen、openai_compatible 复用 OpenAI-compatible 协议
- Agent 创建、列表、详情、更新和删除
- Flow Version 保存、校验和发布
- Flow DSL v1 解析与 DAG 校验
- Agent Runtime 顺序执行 Begin、Retrieval、Prompt、LLM、Message 节点
- Agent Run 创建、状态记录和输出持久化
- run events 与 node logs 记录和查询
- POST + text/event-stream 流式 Agent Run 接口
- React 18 + TypeScript + Vite 前端工程
- 前端浅色 / 深色主题和 macOS 风格工作台布局
- 登录、注册和 GitHub OAuth 前端页面
- 设置页 Provider 管理、API Token 管理和审计日志查看
- 知识库列表、创建、文档上传、chunk 查看和检索测试
- RAG Chat 页面，支持流式问答和引用展示
- Agent 工作区，支持创建、打开和删除 Agent
- React Flow 可视化画布，支持 Begin、Knowledge Retrieval、Prompt、LLM、Message 五类节点
- 画布节点拖拽、连线、配置、DSL 双向序列化、保存、发布和校验
- Agent 调试台，支持 POST + text/event-stream 运行并展示事件时间线
- Go embed 托管 Vite 构建产物，支持 SPA history fallback
- Memory、HTTP Tool、Switch、JSON Output、Guardrail
- Tool 调用日志和 Memory 写入日志
- 知识库级 Embedding Provider / Model / Dimensions 配置
- Elasticsearch dense_vector chunk 索引
- Keyword、Vector、Hybrid 三种检索模式
- 应用层 BM25 + kNN 分数融合
- 可选 Chat Completions Rerank，失败时降级返回原排序
- 知识库重建索引接口与前端入口
- RAG Chat 默认跟随知识库 retrieval_mode
- Agent Retrieval 节点支持 keyword / vector / hybrid 模式

还没有包含：

- PDF / docx / xlsx 解析
- 多人协作和多租户工作区

## 目录说明

```text
cmd/                  程序入口
configs/              配置文件
deployments/          Docker Compose 等部署相关文件
internal/bootstrap/   应用初始化和依赖组装
internal/application/  应用用例层
internal/domain/       领域实体和仓储接口
internal/interface/   HTTP 接口层
internal/infrastructure/ MySQL、Redis、MinIO、Elasticsearch 等外部依赖
internal/pkg/         项目内部通用工具
migrations/           数据库迁移 SQL
scripts/              本地开发脚本
web/                  React + Vite 前端工作台
```

## 配置文件

仓库提供本地配置模板：

```text
configs/config.local.yaml.example
```

首次启动前可以复制一份本地配置：

```bash
cp configs/config.local.yaml.example configs/config.local.yaml
```

`config.local.yaml` 是本地配置文件，不应该提交到 GitHub。它可以放本机数据库、Redis、MinIO、Elasticsearch 等真实连接信息。

服务启动时的配置读取顺序：

```text
1. 如果设置了 AGENTCANVAS_CONFIG_PATH，读取这个路径
2. 否则读取 configs/config.local.yaml
```

## 本地依赖

本地开发依赖通过 Docker Compose 启动：

- MySQL 8.4
- Redis 7.2
- MinIO
- Elasticsearch 8.15.3
- Kibana 8.15.3

启动依赖：

```bash
make docker-up
```

或者直接执行：

```bash
docker compose -f deployments/docker-compose.yml up -d
```

停止依赖：

```bash
make docker-down
```

## 启动完整应用

```bash
make run
```

`make run` 会先执行 `make build-web`，再启动 Go API。启动成功后可以直接访问：

```text
http://localhost:8080
```

如只启动后端 API，可以执行：

```bash
go run ./cmd/api
```

默认监听地址：

```text
http://localhost:8080
```

## 前端开发

前端工程位于：

```text
web/
```

常用命令：

```bash
npm --prefix web run dev
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
```

Vite 开发服务会通过代理访问后端 `/api`。

## 启动 Worker

Phase 2 的文档解析、切片和 ES 索引由独立 worker 执行：

```bash
make worker
```

或者：

```bash
go run ./cmd/worker
```

## 健康检查

基础健康检查：

```bash
curl http://localhost:8080/api/v1/health
```

检查 MySQL：

```bash
curl http://localhost:8080/api/v1/health/mysql
```

检查 Redis：

```bash
curl http://localhost:8080/api/v1/health/redis
```

检查 MinIO：

```bash
curl http://localhost:8080/api/v1/health/minio
```

检查 Elasticsearch：

```bash
curl http://localhost:8080/api/v1/health/es
```

正常情况下会返回类似：

```json
{
  "code": 0,
  "message": "OK",
  "data": {
    "component": "mysql",
    "status": "healthy"
  }
}
```

## Phase 1 API

认证接口：

```text
POST /api/v1/auth/register
POST /api/v1/auth/login
POST /api/v1/auth/refresh
POST /api/v1/auth/logout
GET  /api/v1/auth/me
```

GitHub OAuth：

```text
GET /api/v1/auth/github/redirect
GET /api/v1/auth/github/callback
```

Provider 管理：

```text
GET    /api/v1/model-providers
POST   /api/v1/model-providers
GET    /api/v1/model-providers/:id
PATCH  /api/v1/model-providers/:id
DELETE /api/v1/model-providers/:id
POST   /api/v1/model-providers/:id/test
```

API Token 与审计日志：

```text
GET    /api/v1/api-tokens
POST   /api/v1/api-tokens
DELETE /api/v1/api-tokens/:id
GET    /api/v1/audit-logs
```

## Phase 2 API

Knowledge Base：

```text
POST   /api/v1/knowledge-bases
GET    /api/v1/knowledge-bases
GET    /api/v1/knowledge-bases/:id
PATCH  /api/v1/knowledge-bases/:id
DELETE /api/v1/knowledge-bases/:id
POST   /api/v1/knowledge-bases/:id/search
```

Document：

```text
POST   /api/v1/knowledge-bases/:id/documents
GET    /api/v1/knowledge-bases/:id/documents
GET    /api/v1/documents/:id
DELETE /api/v1/documents/:id
GET    /api/v1/documents/:id/chunks
```

Ingestion Job：

```text
GET    /api/v1/ingestion-jobs/:id
```

## Phase 3 API

RAG Chat：

```text
POST /api/v1/rag/chat
POST /api/v1/rag/chat/stream
```

请求体：

```json
{
  "provider_id": 1,
  "kb_ids": [1],
  "question": "请总结这份文档",
  "conversation_id": 0,
  "model": "",
  "top_k": 8
}
```

流式事件：

```text
conversation
user_message
retrieval
delta
done
error
```

Conversation：

```text
GET    /api/v1/conversations
GET    /api/v1/conversations/:id
GET    /api/v1/conversations/:id/messages
DELETE /api/v1/conversations/:id
```

## Phase 4 API

Agent：

```text
POST   /api/v1/agents
GET    /api/v1/agents
GET    /api/v1/agents/:id
PATCH  /api/v1/agents/:id
DELETE /api/v1/agents/:id
```

Flow Version：

```text
POST /api/v1/agents/:id/flow-versions
GET  /api/v1/agents/:id/flow-versions
GET  /api/v1/flow-versions/:id
POST /api/v1/flow-versions/:id/publish
POST /api/v1/flow-versions/:id/validate
```

Agent Run：

```text
POST /api/v1/agents/:id/runs
POST /api/v1/agents/:id/runs/stream
GET  /api/v1/runs/:id
GET  /api/v1/runs/:id/events
GET  /api/v1/runs/:id/node-logs
POST /api/v1/runs/:id/cancel
```

流式 Agent Run 事件：

```text
workflow_started
node_started
retrieval_started
retrieval_finished
llm_started
llm_delta
llm_finished
message_written
node_finished
workflow_finished
workflow_failed
node_failed
done
error
```

## Phase 5 前端页面

Phase 5 提供一个由 Go embed 托管的单页应用：

```text
/login
/register
/app/chat
/app/knowledge
/app/agents
/app/agents/:id/canvas
/app/settings
```

其中 Agent 画布会把 React Flow 节点序列化为 Flow DSL v1，再调用 Phase 4 API 保存、发布和运行。

## 常用命令

```bash
make docker-up      # 启动本地依赖
make docker-down    # 停止本地依赖
make run            # 启动 API
make worker         # 启动文档处理 Worker
make dev            # 启动依赖并运行 API
make build-web      # 构建 Vite 前端产物
make tidy           # 整理 Go 依赖
make test           # 运行测试/编译检查
make migrate        # 运行 migration 命令入口
```

## MinIO

MinIO API 地址：

```text
http://localhost:9000
```

MinIO 控制台：

```text
http://localhost:9001
```

默认账号和密码：

```text
minioadmin / minioadmin
```

## Kibana

Kibana 地址：

```text
http://localhost:5601
```

## Phase 7 向量检索配置

Phase 7 的向量能力按知识库配置。进入「知识库」页面，选择一个知识库后可以设置：

- 默认检索模式：`keyword`、`vector` 或 `hybrid`
- Embedding Provider、Embedding 模型和 Embedding 维度
- Hybrid 权重，默认 `0.5`
- 可选 Rerank Provider 与 Rerank 模型

启用 `vector` 或 `hybrid` 前，需要先在「设置」中创建支持 OpenAI-compatible `/v1/embeddings` 的 Provider，并填写默认 Embedding 模型或在知识库中单独填写模型。修改 embedding 配置后，点击「重建索引」会为已有文档重新创建 ingestion job，worker 会重新解析、切片并写入 `embedding_vector`。

## 当前状态说明

当前阶段已经完成基础工程骨架、Phase 1 平台能力、Phase 2 的 txt/md 知识库最小闭环、Phase 3 普通 RAG Chat、Phase 4 Agent Flow DSL 与 Runtime、Phase 5 前端画布与 Agent 调试、Phase 6 Memory / Tool / Guardrail 能力，以及 Phase 7 向量检索、Hybrid Search 与 Rerank。用户可以在浏览器中登录、配置 Provider、管理知识库、进行 RAG Chat、创建 Agent、进入画布拖拽节点、保存并发布 Flow Version，再通过 SSE 调试台观察节点运行过程。

下一阶段可继续补充 PDF / docx / xlsx 解析、更多检索质量评估、多人协作和多租户工作区。
