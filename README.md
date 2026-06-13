# AgentCanvas

AgentCanvas 是一个用 Go 编写的单人版 Agent Flow + RAG 知识库项目。

当前项目还处在基础工程阶段，暂时不包含登录、知识库、RAG Chat 和 Agent 画布。这个阶段主要是把后端服务、配置、日志、数据库连接和基础依赖先搭起来。

## 当前阶段

Phase 0：基础工程骨架。

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

还没有包含：

- 用户注册和登录
- GitHub OAuth
- Provider Key 管理
- 知识库上传和检索
- RAG Chat
- Agent Flow Runtime
- 前端画布

## 目录说明

```text
cmd/                  程序入口
configs/              配置文件
deployments/          Docker Compose 等部署相关文件
internal/bootstrap/   应用初始化和依赖组装
internal/interface/   HTTP 接口层
internal/infrastructure/ MySQL、Redis、MinIO、Elasticsearch 等外部依赖
internal/pkg/         项目内部通用工具
migrations/           数据库迁移 SQL
scripts/              本地开发脚本
```

## 配置文件

项目里有两个配置文件：

```text
configs/config.yaml
configs/config.local.yaml
```

`config.yaml` 是默认配置，也作为提交到仓库里的参考模板。

`config.local.yaml` 是本地配置文件，不应该提交到 GitHub。它可以放本机数据库、Redis、MinIO、Elasticsearch 等真实连接信息。

服务启动时的配置读取顺序：

```text
1. 如果设置了 AGENTCANVAS_CONFIG_PATH，读取这个路径
2. 否则优先读取 configs/config.local.yaml
3. 如果 config.local.yaml 不存在，回退读取 configs/config.yaml
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

## 常用命令

```bash
make docker-up      # 启动本地依赖
make docker-down    # 停止本地依赖
make run            # 启动 API
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

当前阶段只保证基础服务可以启动，并且能够连接 MySQL、Redis、MinIO 和 Elasticsearch。

下一阶段会开始补用户体系、登录、GitHub OAuth 和模型 Provider 配置。
