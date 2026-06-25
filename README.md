# AgentCanvas

AgentCanvas 是一个用 Go + React 构建的单人版 Agent Flow + RAG 知识库项目。当前已完成到 Phase 7，具备登录认证、模型 Provider 管理、txt/md 知识库、异步切片索引、RAG Chat、Agent Flow 画布、SSE 调试、Memory / HTTP Tool / Guardrail，以及 Keyword / Vector / Hybrid 检索与可选 Rerank。

## 当前状态

已完成目标：

- 平台基础：配置加载、日志、健康检查、MySQL、Redis、MinIO、Elasticsearch、数据库迁移和本地 Docker Compose。
- 账号与安全：注册登录、JWT access token、refresh session、GitHub OAuth、API Token、审计日志、Provider API Key 加密存储。
- 知识库：知识库 CRUD，txt/md 上传，MinIO 文件存储，worker 异步解析、fixed-token 切片、MySQL chunk 持久化和 Elasticsearch 索引。
- RAG Chat：普通问答、流式问答、conversation / message / reference 持久化、model usage 记录。
- Agent Flow：Agent CRUD、Flow Version 保存 / 校验 / 发布、DSL v1 DAG 校验、运行记录、事件、节点日志和 SSE 调试。
- 前端工作台：登录注册、设置、知识库、RAG Chat、Agent 列表、React Flow 画布、节点配置、保存发布和运行调试。
- Phase 6/7：Memory、HTTP Tool、Switch、JSON Output、Guardrail、tool 调用日志、memory 写入日志、向量索引、Keyword / Vector / Hybrid 检索、BM25 + kNN 分数融合、Chat Completions Rerank、知识库重建索引。

暂未包含：

- PDF / docx / xlsx 解析
- 多人协作和多租户工作区

## 技术栈

- 后端：Go 1.22、Gin、GORM
- 前端：React 18、TypeScript、Vite、React Flow
- 存储与中间件：MySQL、Redis、MinIO、Elasticsearch
- LLM 协议：OpenAI-compatible Chat Completions / Embeddings

## 目录结构

```text
cmd/                    API、worker、migration 入口
configs/                本地配置模板
deployments/            Docker Compose 配置
internal/application/   应用用例层
internal/domain/        领域实体和仓储接口
internal/infrastructure/MySQL、Redis、MinIO、Elasticsearch、LLM 客户端
internal/interface/     HTTP 路由、handler、middleware、SSE
internal/runtime/       Agent Flow 执行引擎和节点
migrations/             数据库迁移 SQL
scripts/                本地脚本
web/                    React + Vite 前端
```

## 本地启动

复制配置模板：

```bash
cp configs/config.local.yaml.example configs/config.local.yaml
```

启动本地依赖：

```bash
make docker-up
```

运行数据库迁移：

```bash
make migrate
```

启动完整本地开发链路（Docker 依赖、迁移、前端构建、API、文档处理 worker）：

```bash
make dev
```

仅启动 API 与内嵌前端：

```bash
make run
```

启动文档处理 worker：

```bash
make worker
```

访问地址：

```text
http://localhost:8080
```

前端独立开发：

```bash
npm --prefix web run dev
```

## 常用命令

```bash
make docker-up          # 启动 MySQL / Redis / MinIO / Elasticsearch / Kibana
make docker-down        # 停止本地依赖
make dev                # 启动完整本地链路：依赖 / 迁移 / API / worker
make run                # 运行迁移、构建前端并启动 Go API
make worker             # 启动文档解析与索引 worker
make test               # 运行 Go 测试
make typecheck-web      # 运行前端类型检查
make test-web           # 运行前端测试
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
```

## 配置说明

默认读取：

```text
configs/config.local.yaml
```

也可以通过环境变量指定：

```bash
export AGENTCANVAS_CONFIG_PATH=/path/to/config.yaml
```

`configs/config.local.yaml` 用于存放本机数据库、Redis、MinIO、Elasticsearch、OAuth、加密密钥等真实配置，不应提交到版本库。

## 主要 API

健康检查：

```text
GET /api/v1/health
GET /api/v1/health/mysql
GET /api/v1/health/redis
GET /api/v1/health/minio
GET /api/v1/health/es
```

认证与平台：

```text
POST /api/v1/auth/register
POST /api/v1/auth/login
POST /api/v1/auth/refresh
POST /api/v1/auth/logout
GET  /api/v1/auth/me
GET  /api/v1/auth/github/redirect
GET  /api/v1/auth/github/callback

GET    /api/v1/model-providers
POST   /api/v1/model-providers
PATCH  /api/v1/model-providers/:id
DELETE /api/v1/model-providers/:id
POST   /api/v1/model-providers/:id/test

GET    /api/v1/api-tokens
POST   /api/v1/api-tokens
DELETE /api/v1/api-tokens/:id
GET    /api/v1/audit-logs
```

知识库与检索：

```text
POST   /api/v1/knowledge-bases
GET    /api/v1/knowledge-bases
GET    /api/v1/knowledge-bases/:id
PATCH  /api/v1/knowledge-bases/:id
DELETE /api/v1/knowledge-bases/:id
POST   /api/v1/knowledge-bases/:id/search
POST   /api/v1/knowledge-bases/:id/reindex

POST   /api/v1/knowledge-bases/:id/documents
GET    /api/v1/knowledge-bases/:id/documents
GET    /api/v1/documents/:id
DELETE /api/v1/documents/:id
GET    /api/v1/documents/:id/chunks
GET    /api/v1/ingestion-jobs/:id
```

RAG Chat：

```text
POST   /api/v1/dialogs
GET    /api/v1/dialogs
GET    /api/v1/dialogs/:id
PATCH  /api/v1/dialogs/:id
DELETE /api/v1/dialogs/:id

POST   /api/v1/dialogs/:dialog_id/rag/chat
POST   /api/v1/dialogs/:dialog_id/rag/chat/stream
GET    /api/v1/dialogs/:dialog_id/conversations
GET    /api/v1/dialogs/:dialog_id/conversations/:id
GET    /api/v1/dialogs/:dialog_id/conversations/:id/messages
DELETE /api/v1/dialogs/:dialog_id/conversations/:id
```

Agent、Memory 和 Tool：

```text
POST   /api/v1/agents
GET    /api/v1/agents
GET    /api/v1/agents/:id
PATCH  /api/v1/agents/:id
DELETE /api/v1/agents/:id

POST /api/v1/agents/:id/flow-versions
GET  /api/v1/agents/:id/flow-versions
GET  /api/v1/flow-versions/:id
POST /api/v1/flow-versions/:id/publish
POST /api/v1/flow-versions/:id/validate

POST /api/v1/agents/:id/runs
POST /api/v1/agents/:id/runs/stream
GET  /api/v1/runs/:id
GET  /api/v1/runs/:id/events
GET  /api/v1/runs/:id/node-logs
GET  /api/v1/runs/:id/memory-write-logs
GET  /api/v1/runs/:id/tool-invocations
POST /api/v1/runs/:id/cancel

GET    /api/v1/memories
POST   /api/v1/memories
PATCH  /api/v1/memories/:id
DELETE /api/v1/memories/:id

GET    /api/v1/tool-definitions
POST   /api/v1/tool-definitions
PATCH  /api/v1/tool-definitions/:id
DELETE /api/v1/tool-definitions/:id
POST   /api/v1/tool-definitions/:id/test
```

## 向量检索

知识库支持 `keyword`、`vector` 和 `hybrid` 三种检索模式。启用 `vector` 或 `hybrid` 前，需要在设置页创建支持 OpenAI-compatible `/v1/embeddings` 的 Provider，并配置 embedding model / dimensions。修改 embedding 配置后，可以通过前端或 `POST /api/v1/knowledge-bases/:id/reindex` 重建索引。

Hybrid Search 会融合 BM25 与 kNN 分数；配置 Rerank Provider 后，会使用 Chat Completions 做可选重排。Rerank 失败时会降级返回原排序。

## 验证

本次整理前已确认以下命令通过：

```bash
go test ./...
npm --prefix web run typecheck
npm --prefix web run test
npm --prefix web run build
```
