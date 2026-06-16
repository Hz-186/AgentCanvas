# AgentCanvas

AgentCanvas 是一个用 Go 编写的单人版 Agent Flow + RAG 知识库项目。

当前项目已经完成 Phase 2：Elasticsearch 知识库最小闭环。系统已经具备单人应用的平台壳子、模型配置能力，以及 txt/md 文档上传、异步解析切片、ES BM25 检索能力。

## 当前阶段

Phase 2：Elasticsearch 知识库最小闭环。

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
- Worker 轮询处理文档解析、切片和索引
- document chunks 保存到 MySQL
- chunks 同步写入 Elasticsearch
- 知识库关键词搜索和高亮返回
- retrieval logs 检索日志记录

还没有包含：

- PDF / docx / xlsx 解析
- RAG Chat
- Agent Flow Runtime
- 前端画布

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

## 启动 API

```bash
make run
```

或者：

```bash
go run ./cmd/api
```

默认监听地址：

```text
http://localhost:8080
```

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

## 常用命令

```bash
make docker-up      # 启动本地依赖
make docker-down    # 停止本地依赖
make run            # 启动 API
make worker         # 启动文档处理 Worker
make dev            # 启动依赖并运行 API
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

## 当前状态说明

当前阶段已经完成基础工程骨架、Phase 1 平台能力，以及 Phase 2 的 txt/md 知识库最小闭环：创建知识库、上传文档、后台 worker 解析切片、写入 MySQL 与 Elasticsearch，并通过搜索接口返回命中 chunk 和高亮内容。

下一阶段会开始实现 Phase 3：普通 RAG Chat，不做画布，基于已有知识库检索结果接入 LLM 生成回答。
