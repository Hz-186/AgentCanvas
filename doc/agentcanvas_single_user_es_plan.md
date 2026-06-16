# AgentCanvas 单人版 + Elasticsearch 检索版完整建设文档

**项目名称**：AgentCanvas  
**项目定位**：Go 语言实现的单人版可视化 Agent Flow + RAG 知识库平台  
**文档版本**：v2.0-single-user-es  
**当前设计重点**：去掉 tenant/workspace，多人协作后置；第一检索后端使用 Elasticsearch；预留向量数据库接口；保留 Agent 五层架构与工程化扩展空间。  
**适用目标**：个人学习、就业/实习项目、AI 工程师面试项目、Go 后端综合项目。

---

## 0. 本版相对上一版的核心修改

这一版不再把项目设计成企业 SaaS 多租户系统，而是明确改成**单人应用**。

### 0.1 去掉的东西

第一版不做：

1. tenant；
2. workspace；
3. workspace member；
4. workspace invite；
5. workspace role；
6. 复杂企业组织架构；
7. 计费系统；
8. 多人协同编辑画布。

这些能力不是永远不做，而是后置。当前阶段你的目标是把最有价值的 AI 工程能力做扎实：

1. RAG 知识库；
2. Elasticsearch 检索；
3. 文件上传、解析、切片、索引；
4. Agent Flow 画布；
5. Flow DSL；
6. Go Flow Runtime；
7. LLM Provider 管理；
8. Memory；
9. Tool 调用；
10. 运行事件与调试；
11. 输出管控。

### 0.2 保留的东西

虽然去掉 tenant/workspace，但仍保留：

1. user；
2. owner_id；
3. 登录认证；
4. GitHub OAuth 登录；
5. API Token；
6. Provider Key 加密存储；
7. Audit Log；
8. 权限边界的基础设计。

也就是说，本项目中的资源归属关系变成：

```text
User
  ├── Model Providers
  ├── Knowledge Bases
  ├── Documents
  ├── Chunks
  ├── Agents
  ├── Flow Versions
  ├── Conversations
  ├── Memories
  ├── Tools
  └── Audit Logs
```

在当前单人应用中，`user_id` 与 `owner_id` 基本等价。之所以仍然保留 `owner_id`，是因为它非常便宜，但未来扩展空间很大。后期如果要加 workspace，可以迁移成：

```text
owner_id -> workspace_id / user_id polymorphic owner
```

或者简单地新增 `workspace_id` 字段，再用迁移脚本把历史数据挂到默认 workspace 下。

---

## 1. 项目最终要呈现的状态

最终你要做出来的不是一个简单的“上传文档然后问答”的 RAG demo，而是一个真正可以展示的 AI 工程平台。它应该具备如下可见能力。

### 1.1 登录与配置

用户打开系统后，可以：

1. 使用邮箱密码注册/登录；
2. 使用 GitHub OAuth 登录；
3. 在个人设置中配置大模型 Provider；
4. 配置 OpenAI-compatible、DeepSeek、Qwen、Ollama 等模型；
5. API Key 使用加密方式存储；
6. 登录凭据和 refresh token 使用单向哈希存储；
7. 可以查看自己的模型调用日志与消耗统计。

### 1.2 知识库

用户可以：

1. 创建知识库；
2. 上传 txt、md、pdf 文件；
3. 文件原文进入 MinIO；
4. MySQL 保存文档元数据；
5. Worker 异步解析文件；
6. 文档被切成 chunks；
7. chunk 元数据进入 MySQL；
8. chunk 可检索内容进入 Elasticsearch；
9. 用户可以查看 chunk 列表；
10. 用户可以测试检索 query；
11. 检索结果展示 score、document、chunk、页码、命中内容。

第一阶段检索直接使用 Elasticsearch BM25。后续再加 ES dense_vector、hybrid search、rerank，或者替换为 Milvus/Qdrant。

### 1.3 Agent Flow 画布

用户可以：

1. 新建 Agent；
2. 进入画布；
3. 拖拽节点；
4. 配置节点参数；
5. 连接节点；
6. 保存为 Flow DSL；
7. 发布 Flow Version；
8. 在调试窗口运行 Agent；
9. 前端通过 SSE 看到节点运行过程；
10. 可以查看每个节点的输入、输出、耗时、错误、token 用量；
11. 可以回放某次运行。

第一批节点只做：

1. Begin；
2. Knowledge Retrieval；
3. Prompt；
4. LLM；
5. Message。

第二批再做：

1. Switch；
2. Memory Read；
3. Memory Write；
4. HTTP Tool；
5. JSON Output；
6. Guardrail。

### 1.4 对话与运行

用户可以：

1. 选择一个 Agent；
2. 开启一个 Conversation；
3. 输入问题；
4. 系统运行 Agent Flow；
5. 流式输出回答；
6. 回答中附带引用来源；
7. 历史消息被保存；
8. Memory 可被注入 prompt；
9. 每次运行产生可观测日志。

### 1.5 项目最终展示话术

你最终可以这样介绍这个项目：

> 我做了一个 Go 语言实现的单人版 AgentCanvas。用户可以上传文档构建知识库，系统通过异步 worker 解析、切片并写入 Elasticsearch。用户可以在画布上拖拽节点搭建 Agent Flow，画布会保存成 DSL，由 Go 实现的 Flow Runtime 执行。运行时支持 RAG 检索、模型调用、流式输出、引用溯源、Memory、Tool 调用、运行事件追踪和输出管控。工程上使用 MySQL、Redis、MinIO、Elasticsearch、Docker Compose，并预留了向量数据库与消息队列的接口。

这句话比“我做了一个 RAG 问答系统”强很多。

---

## 2. 总体架构

### 2.1 单人版总体架构

```text
┌─────────────────────────────────────────────────────────────┐
│                         Frontend                            │
│ React/Vue + Flow Canvas + Chat UI + Admin Console           │
└───────────────────────────────┬─────────────────────────────┘
                                │ HTTP / SSE
┌───────────────────────────────▼─────────────────────────────┐
│                         Go API Server                        │
│ Gin/Hertz + Middleware + REST API + SSE                      │
│                                                             │
│  Access Layer                                                │
│  Conversation Layer                                          │
│  Management Layer                                            │
│  Agent Core Layer                                            │
│  Tool Layer                                                  │
│  Output Control Layer                                        │
└─────────────┬───────────────────┬───────────────────┬───────┘
              │                   │                   │
              │                   │                   │
┌─────────────▼──────┐   ┌────────▼────────┐   ┌──────▼─────────┐
│      MySQL          │   │      Redis      │   │     MinIO      │
│ metadata            │   │ cache/session   │   │ raw files      │
└─────────────────────┘   └─────────────────┘   └────────────────┘
              │
              │
┌─────────────▼────────────────────────────────────────────────┐
│                     Elasticsearch                            │
│ chunk index / BM25 / dense_vector later / hybrid search       │
└──────────────────────────────────────────────────────────────┘
              ▲
              │
┌─────────────┴────────────────────────────────────────────────┐
│                         Go Worker                            │
│ parse files / chunk / index to ES / status update             │
└──────────────────────────────────────────────────────────────┘
```

### 2.2 为什么先用 Elasticsearch

你现在的诉求是“先做能落地的检索核心”。这个判断是对的。

第一版直接使用 Elasticsearch 的好处：

1. BM25 全文检索成熟；
2. 对中文、英文、代码、文档都比较通用；
3. 可以直接保存 chunk 文本与 metadata；
4. 可以高亮命中内容；
5. 可以做字段过滤；
6. 可以先不接 embedding，也能完成 RAG 检索闭环；
7. 后续可以在 ES 中新增 dense_vector 字段；
8. 后续可以替换 Milvus/Qdrant，但上层接口不变。

Elasticsearch 官方文档中，`dense_vector` 字段用于存储数值向量，并主要用于 kNN 搜索；Elasticsearch 也支持基于 kNN 的向量检索。也就是说，先用 ES 做关键词检索，后面继续演进到 ES 向量检索是合理路径。

### 2.3 为什么不一开始上 Milvus

Milvus 很适合作为专门的向量数据库，但你第一阶段如果同时做：

1. MySQL；
2. MinIO；
3. Redis；
4. Milvus；
5. Embedding；
6. Worker；
7. Agent Flow；
8. 画布；
9. OAuth；
10. LLM Provider；

复杂度会瞬间爆炸。

先用 ES 的路线更像真实工程中的 trade-off：

> 第一版优先打通业务闭环，用 Elasticsearch 完成文档检索、过滤、排序、高亮和引用。检索层通过接口抽象，后续可以替换或扩展为 Milvus/Qdrant/ES dense_vector，而不会影响 Agent Runtime。

这就是能在面试里讲清楚的架构取舍。

---

## 3. Agent 制作的五层架构

你提到“五层架构”是这个项目的核心知识点之一。我们保留它，但根据单人应用调整为下面这个版本。

### 3.1 五层主架构

```text
┌──────────────────────────────────────────────────────────┐
│ 1. 接入层 Access Layer                                    │
│ HTTP API / SSE / Auth / GitHub OAuth / Rate Limit        │
└────────────────────────────┬─────────────────────────────┘
                             │
┌────────────────────────────▼─────────────────────────────┐
│ 2. 对话层 Conversation Layer                              │
│ Conversation / Message / Chat Context / Streaming        │
└────────────────────────────┬─────────────────────────────┘
                             │
┌────────────────────────────▼─────────────────────────────┐
│ 3. 管理层 Management Layer                                │
│ User / Provider / KnowledgeBase / Document / Agent / Flow│
└────────────────────────────┬─────────────────────────────┘
                             │
┌────────────────────────────▼─────────────────────────────┐
│ 4. Agent 核心层 Agent Core Layer                          │
│ Flow DSL / Node Registry / Executor / Run Event          │
└────────────────────────────┬─────────────────────────────┘
                             │
┌────────────────────────────▼─────────────────────────────┐
│ 5. 工具层 Tool Layer                                      │
│ LLM / Retriever / Parser / Embedding / HTTP Tool / ES    │
└──────────────────────────────────────────────────────────┘

横切层：Output Control Layer
Guardrail / Citation / JSON Schema / Sensitive Check / Formatter
```

### 3.2 每一层的职责

#### 3.2.1 接入层 Access Layer

负责所有外部请求进入系统的第一道门。

包括：

1. HTTP REST API；
2. SSE 流式接口；
3. 登录鉴权；
4. GitHub OAuth 回调；
5. JWT 验证；
6. Refresh Token；
7. API Token；
8. Rate Limit；
9. Request ID；
10. 基础日志。

这一层不要写业务逻辑。它只负责解析请求、鉴权、参数校验、调用 application usecase。

#### 3.2.2 对话层 Conversation Layer

负责一次用户对话如何组织上下文。

包括：

1. conversation；
2. messages；
3. chat history；
4. 当前用户输入；
5. 最近 N 轮消息；
6. Memory 读取；
7. Agent Run 绑定；
8. 流式输出聚合；
9. 引用信息保存。

这一层的关键问题是：

> 用户输入进来之后，到底应该带着哪些上下文去运行 Agent？

#### 3.2.3 管理层 Management Layer

负责资源的 CRUD 与配置管理。

包括：

1. 用户设置；
2. Provider Key；
3. KnowledgeBase；
4. Document；
5. Chunk 查看；
6. Agent；
7. Flow Version；
8. Tool Definition；
9. Audit Log。

这一层是控制平面，不负责真正执行 Agent。

#### 3.2.4 Agent 核心层 Agent Core Layer

这是整个项目最核心的地方。

包括：

1. Flow DSL；
2. Node Registry；
3. Node Interface；
4. Flow Validator；
5. Flow Executor；
6. Run Context；
7. Event Bus；
8. Node State；
9. Error Handling；
10. Retry/Fallback；
11. Run Log。

这一层要做得尽量干净，因为这是你项目最有面试含金量的部分。

#### 3.2.5 工具层 Tool Layer

负责对接外部能力。

包括：

1. LLM Provider；
2. Embedding Provider；
3. Retriever；
4. Elasticsearch；
5. MinIO；
6. Parser；
7. HTTP Tool；
8. SQL Tool 后置；
9. MCP Tool 后置；
10. Sandbox 后置。

Agent Core 不应该直接依赖 Elasticsearch 细节，而应该依赖 `Retriever` 接口。

#### 3.2.6 输出管控层 Output Control Layer

这一层贯穿 Agent 运行链路，负责最终输出是否安全、规范、可追踪。

包括：

1. citation 注入；
2. JSON schema 校验；
3. 敏感词检查；
4. 输出格式化；
5. 幻觉风险提示；
6. tool result 过滤；
7. prompt injection 基础防护；
8. 最终消息落库。

---

## 4. 项目阶段总览

整个项目建议分为 8 个阶段。

```text
Phase 0: 单人版基础工程骨架
Phase 1: 登录、GitHub OAuth、Provider Key 管理
Phase 2: ES 知识库最小闭环
Phase 3: 普通 RAG Chat，不做画布
Phase 4: Agent Flow DSL 与 Runtime
Phase 5: 前端画布与 Agent 调试
Phase 6: Memory、Tool、Guardrail
Phase 7: ES 向量检索 / Hybrid / Rerank / 可观测性
```

注意顺序：

> 先做知识库，再做普通 RAG Chat，再做 Agent Flow。不要一开始就做画布。

原因很简单：画布只是编排入口，真正底层能力是 RAG、LLM、Message、Run Event。如果这些基础能力没做，画布只是空壳。

---

## 5. Phase 0：单人版基础工程骨架

### 5.1 阶段目标

搭建最小可运行后端工程。

本阶段完成后，你应该可以：

1. 启动 Go API 服务；
2. 连接 MySQL；
3. 连接 Redis；
4. 连接 MinIO；
5. 连接 Elasticsearch；
6. 运行数据库 migration；
7. 提供健康检查接口；
8. 统一日志、配置、错误返回格式。

本阶段不做业务功能，只做地基。

### 5.2 阶段架构

```text
Frontend 暂无
   │
   │ curl / Postman
   ▼
Go API Server
   ├── Config
   ├── Logger
   ├── MySQL Client
   ├── Redis Client
   ├── MinIO Client
   ├── ES Client
   └── Health API
```

### 5.3 Docker Compose

本阶段建议启动：

```text
api
mysql
redis
minio
elasticsearch
kibana 可选
```

第一阶段可以不用 NATS，也不用 Milvus。

### 5.4 项目目录结构

```text
agentcanvas/
  cmd/
    api/
      main.go
    migrate/
      main.go
  configs/
    config.yaml
    config.local.yaml
  deployments/
    docker-compose.yml
    docker-compose.dev.yml
    docker/
      mysql/
      elasticsearch/
      minio/
  internal/
    bootstrap/
      app.go
      wire.go
    interface/
      http/
        router.go
        middleware/
          cors.go
          request_id.go
          recovery.go
          auth.go
        handler/
          health_handler.go
    infrastructure/
      mysql/
        mysql.go
      redis/
        redis.go
      minio/
        minio.go
      elasticsearch/
        client.go
    pkg/
      config/
        config.go
      logger/
        logger.go
      response/
        response.go
      errors/
        errors.go
      idgen/
        idgen.go
  migrations/
    000001_init.up.sql
    000001_init.down.sql
  scripts/
    dev.sh
    migrate.sh
  Makefile
  go.mod
  go.sum
  README.md
```

### 5.5 配置文件设计

```yaml
app:
  name: agentcanvas
  env: local
  port: 8080
  base_url: http://localhost:8080

mysql:
  dsn: root:password@tcp(localhost:3306)/agentcanvas?charset=utf8mb4&parseTime=True&loc=Local

redis:
  addr: localhost:6379
  password: ""
  db: 0

minio:
  endpoint: localhost:9000
  access_key: minioadmin
  secret_key: minioadmin
  bucket: agentcanvas
  use_ssl: false

elasticsearch:
  addresses:
    - http://localhost:9200
  username: ""
  password: ""
  chunk_index: agentcanvas_chunks_v1

security:
  jwt_secret: change-me
  refresh_token_pepper: change-me
  secret_encrypt_key: 32-bytes-base64-key
```

配置文件约定：

1. `configs/config.yaml` 作为默认配置与仓库中的参考模板，可以提交到 GitHub；
2. `configs/config.local.yaml` 作为本地真实配置，不提交到 GitHub；
3. 服务启动时优先读取环境变量 `AGENTCANVAS_CONFIG_PATH` 指定的配置；
4. 如果没有指定环境变量，则优先读取 `configs/config.local.yaml`；
5. 如果本地配置不存在，再回退读取 `configs/config.yaml`。

这样既能保证本地开发优先使用 local 配置，也能保证别人 clone 项目后可以参考默认配置启动或复制模板。

### 5.6 健康检查 API

```text
GET /api/v1/health
GET /api/v1/health/mysql
GET /api/v1/health/redis
GET /api/v1/health/minio
GET /api/v1/health/es
```

### 5.7 本阶段交付状态

本阶段结束后，你可以在 README 里写：

```text
已完成 Go API 工程骨架，支持 Docker Compose 启动 MySQL、Redis、MinIO、Elasticsearch，后端可读取配置、连接基础设施，并提供健康检查接口。
```

---

## 6. Phase 1：用户登录、GitHub OAuth、Provider Key 管理

### 6.1 阶段目标

本阶段做单人应用的登录与模型配置。

完成后用户可以：

1. 邮箱密码注册；
2. 邮箱密码登录；
3. GitHub OAuth 登录；
4. 查看当前用户信息；
5. 配置 LLM Provider；
6. 保存 API Key；
7. API Key 加密存储；
8. 创建 API Token；
9. 查看审计日志。

### 6.2 阶段架构

```text
Browser
  │
  ├── Email/Password Login
  │
  └── GitHub OAuth Redirect
          │
          ▼
Go API Server
  ├── Auth Handler
  ├── OAuth Handler
  ├── User Usecase
  ├── Provider Usecase
  ├── Secret Crypto Service
  ├── JWT Service
  └── Audit Service
          │
          ▼
        MySQL
```

### 6.3 安全原则

这里要区分两类数据。

#### 6.3.1 只需要校验，不需要还原的数据

这类数据使用单向哈希。

包括：

1. 用户密码；
2. refresh token；
3. API token；
4. OAuth state nonce；
5. 邮箱验证 token；
6. 找回密码 token。

推荐：

1. 密码：Argon2id 或 bcrypt；
2. refresh token / API token：`sha256(token + pepper)`；
3. state nonce：Redis 保存短期值，或 MySQL 保存 hash。

#### 6.3.2 调用时必须还原的数据

这类数据不能单向哈希，因为系统调用模型时必须拿到原始值。

包括：

1. OpenAI API Key；
2. DeepSeek API Key；
3. Qwen API Key；
4. GitHub access token，如果你后续需要调用 GitHub API；
5. 第三方工具密钥。

推荐使用：

1. AES-256-GCM 加密；
2. 加密主密钥从环境变量读取；
3. 数据库只保存 ciphertext、nonce、key_version；
4. 不在日志中打印明文；
5. 返回前端时只展示 mask，例如 `sk-****abcd`。

### 6.4 GitHub OAuth 登录流程

GitHub OAuth Web Application Flow 的核心步骤是：

```text
1. 用户点击 GitHub 登录
2. 后端生成 state
3. 后端把用户重定向到 GitHub 授权页面
4. 用户授权后 GitHub redirect 回 callback
5. 后端校验 state
6. 后端用 code 换 access token
7. 后端调用 GitHub API 获取用户身份
8. 根据 github_user_id 查找 oauth_accounts
9. 如果不存在，创建 user 和 oauth_account
10. 签发本系统 access token / refresh token
```

GitHub 官方文档描述的 OAuth Web Application Flow 也是这个基本流程：用户被重定向到 GitHub 请求身份、GitHub 再重定向回站点、应用使用 access token 调用 API。

### 6.5 本阶段新增目录结构

```text
internal/
  domain/
    user/
      entity.go
      repository.go
    auth/
      token.go
      session.go
      repository.go
    provider/
      entity.go
      repository.go
    audit/
      entity.go
      repository.go
  application/
    auth_usecase/
      register.go
      login.go
      github_oauth.go
      refresh.go
      logout.go
    provider_usecase/
      create_provider.go
      update_provider.go
      test_provider.go
    audit_usecase/
      list_audits.go
  infrastructure/
    mysql/
      user_repo.go
      oauth_repo.go
      session_repo.go
      provider_repo.go
      audit_repo.go
    crypto/
      password.go
      aes_gcm.go
      token_hash.go
    oauth/
      github_client.go
    llm/
      provider_client.go
  interface/
    http/
      handler/
        auth_handler.go
        oauth_handler.go
        provider_handler.go
        audit_handler.go
```

### 6.6 数据库表：users

```sql
CREATE TABLE users (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    username VARCHAR(64) NOT NULL UNIQUE,
    email VARCHAR(128) UNIQUE,
    password_hash VARCHAR(255),
    avatar_url VARCHAR(512),
    login_type VARCHAR(32) NOT NULL DEFAULT 'password',
    status TINYINT NOT NULL DEFAULT 1,
    last_login_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME NULL,
    INDEX idx_email (email),
    INDEX idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

字段说明：

| 字段 | 说明 |
|---|---|
| id | 用户 ID，当前系统中基本等同 owner_id |
| username | 用户名 |
| email | 邮箱 |
| password_hash | 密码哈希；GitHub 登录用户可以为空 |
| avatar_url | 头像 |
| login_type | password/github/mixed |
| status | 1 正常，0 禁用 |
| last_login_at | 最后登录时间 |

### 6.7 数据库表：oauth_accounts

```sql
CREATE TABLE oauth_accounts (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    user_id BIGINT NOT NULL,
    provider VARCHAR(32) NOT NULL,
    provider_user_id VARCHAR(128) NOT NULL,
    provider_username VARCHAR(128),
    provider_email VARCHAR(128),
    avatar_url VARCHAR(512),
    access_token_encrypted TEXT,
    refresh_token_encrypted TEXT,
    scopes VARCHAR(512),
    token_expires_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE KEY uk_provider_user (provider, provider_user_id),
    INDEX idx_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

注意：

如果 GitHub OAuth 只用于登录，可以不保存 GitHub access token。拿到用户信息后就丢弃 token，只保存 `provider_user_id`。如果后续要接 GitHub Repo 导入知识库，再加密保存 token。

### 6.8 数据库表：auth_sessions

```sql
CREATE TABLE auth_sessions (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    user_id BIGINT NOT NULL,
    refresh_token_hash CHAR(64) NOT NULL,
    user_agent VARCHAR(512),
    ip_address VARCHAR(64),
    expires_at DATETIME NOT NULL,
    revoked_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE KEY uk_refresh_token_hash (refresh_token_hash),
    INDEX idx_user_id (user_id),
    INDEX idx_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 6.9 数据库表：api_tokens

```sql
CREATE TABLE api_tokens (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    token_hash CHAR(64) NOT NULL,
    token_prefix VARCHAR(16) NOT NULL,
    scopes JSON,
    last_used_at DATETIME NULL,
    expires_at DATETIME NULL,
    revoked_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE KEY uk_token_hash (token_hash),
    INDEX idx_owner_id (owner_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 6.10 数据库表：model_providers

```sql
CREATE TABLE model_providers (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    name VARCHAR(64) NOT NULL,
    provider_type VARCHAR(64) NOT NULL,
    base_url VARCHAR(512),
    encrypted_api_key TEXT,
    api_key_mask VARCHAR(64),
    default_chat_model VARCHAR(128),
    default_embedding_model VARCHAR(128),
    status TINYINT NOT NULL DEFAULT 1,
    last_test_status VARCHAR(32),
    last_test_error TEXT,
    last_test_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME NULL,
    INDEX idx_owner_id (owner_id),
    INDEX idx_provider_type (provider_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

Provider 类型建议：

```text
openai_compatible
deepseek
qwen
ollama
azure_openai
local
```

### 6.11 数据库表：audit_logs

```sql
CREATE TABLE audit_logs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    actor_id BIGINT NOT NULL,
    action VARCHAR(128) NOT NULL,
    resource_type VARCHAR(64) NOT NULL,
    resource_id VARCHAR(64),
    detail_json JSON,
    ip_address VARCHAR(64),
    user_agent VARCHAR(512),
    created_at DATETIME NOT NULL,
    INDEX idx_owner_id (owner_id),
    INDEX idx_actor_id (actor_id),
    INDEX idx_action (action),
    INDEX idx_resource (resource_type, resource_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 6.12 Provider Key 加密结构

可以设计一个通用结构：

```go
type EncryptedSecret struct {
    Ciphertext string `json:"ciphertext"`
    Nonce      string `json:"nonce"`
    KeyVersion string `json:"key_version"`
    Algorithm  string `json:"algorithm"` // AES-256-GCM
}
```

数据库中 `encrypted_api_key` 存 JSON 字符串即可。

### 6.13 本阶段 API

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
GET    /api/v1/model-providers/{id}
PATCH  /api/v1/model-providers/{id}
DELETE /api/v1/model-providers/{id}
POST   /api/v1/model-providers/{id}/test

GET    /api/v1/api-tokens
POST   /api/v1/api-tokens
DELETE /api/v1/api-tokens/{id}

GET /api/v1/audit-logs
```

### 6.14 本阶段交付状态

本阶段结束后，你的系统已经有了“平台壳子”：

1. 用户能登录；
2. GitHub 能登录；
3. Provider Key 能配置；
4. 密钥能加密；
5. token 能哈希；
6. audit 能记录。

这时还没有 RAG，也没有 Agent，但工程基础已经像一个真实项目了。

---

## 7. Phase 2：Elasticsearch 知识库最小闭环

### 7.1 阶段目标

本阶段实现知识库的最小闭环。

完成后用户可以：

1. 创建知识库；
2. 上传文件；
3. 文件进入 MinIO；
4. MySQL 保存 document；
5. Worker 解析文件；
6. 生成 chunks；
7. chunks 进入 MySQL；
8. chunks 同步写入 Elasticsearch；
9. 用户可以搜索知识库；
10. 返回命中 chunk 和高亮内容。

这一阶段先不调用 LLM，也不做 embedding。

### 7.2 阶段架构

```text
Browser / API Client
   │
   │ upload file / search kb
   ▼
Go API Server
   ├── KnowledgeBase API
   ├── Document API
   ├── Search API
   └── Ingestion Job Creator
       │
       ├── MySQL: metadata
       ├── MinIO: raw file
       └── Redis List / DB job: pending

Go Worker
   ├── Pull pending job
   ├── Download file from MinIO
   ├── Parse text
   ├── Chunk text
   ├── Save chunks to MySQL
   ├── Index chunks to Elasticsearch
   └── Update job status
```

### 7.3 为什么本阶段不用 MQ

最简单版本可以不用 NATS/Kafka，直接用 MySQL job 表 + worker 轮询。

原因：

1. 少启动一个组件；
2. 更容易调试；
3. 任务量小；
4. 先把业务闭环做出来；
5. 后续可以替换为 Redis Stream/NATS。

这也是一个工程取舍：

> 第一个可运行版本使用 MySQL Job Table 实现异步任务，保证实现简单和状态可追踪。后续当任务吞吐量上升，再把 Job Dispatch 抽象为 Queue 接口，并接入 Redis Stream 或 NATS JetStream。

### 7.4 本阶段新增目录结构

```text
cmd/
  worker/
    main.go

internal/
  domain/
    knowledge/
      knowledge_base.go
      document.go
      chunk.go
      ingestion_job.go
      repository.go
    retrieval/
      query.go
      result.go
      retriever.go
      indexer.go
  application/
    knowledge_usecase/
      create_kb.go
      upload_document.go
      list_documents.go
      search_kb.go
      get_chunks.go
    ingestion_usecase/
      create_job.go
      process_job.go
  infrastructure/
    mysql/
      knowledge_repo.go
      document_repo.go
      chunk_repo.go
      ingestion_job_repo.go
    minio/
      file_storage.go
    parser/
      text_parser.go
      markdown_parser.go
      pdf_parser.go
    chunker/
      fixed_token_chunker.go
      markdown_chunker.go
    retrieval/
      elasticsearch/
        indexer.go
        retriever.go
        mapping.go
  interface/
    http/
      handler/
        knowledge_handler.go
        document_handler.go
        retrieval_handler.go
```

### 7.5 检索接口设计

上层不直接依赖 ES。

当前第一版只实现 Elasticsearch BM25 检索，但接口设计不能把上层锁死在 ES 上。后续可以增加 ES dense_vector、Milvus、Qdrant、HybridRetriever 或 MockRetriever。Agent Runtime、Knowledge Retrieval Node 和 application usecase 都只依赖 `Retriever` / `RetrievalIndexer` 接口，不直接依赖具体 ES client。

```go
type RetrievalIndexer interface {
    CreateIndex(ctx context.Context, req CreateIndexRequest) error
    IndexChunks(ctx context.Context, chunks []ChunkIndexDocument) error
    DeleteDocumentChunks(ctx context.Context, documentID int64) error
    DeleteKnowledgeBase(ctx context.Context, kbID int64) error
}

type Retriever interface {
    Search(ctx context.Context, req RetrievalRequest) (*RetrievalResponse, error)
}
```

请求结构：

```go
type RetrievalRequest struct {
    OwnerID int64
    KBIDs   []int64
    Query   string
    TopK    int
    Filters map[string]any
    Mode    string // keyword, vector, hybrid. 第一版只实现 keyword
}
```

返回结构：

```go
type RetrievalResult struct {
    ChunkID      int64
    DocumentID   int64
    KBID         int64
    Score        float64
    Content      string
    Highlight    string
    DocumentName string
    PageNo       *int
    Metadata     map[string]any
}
```

### 7.6 ES Index 命名

第一版建议所有 chunk 放一个 index：

```text
agentcanvas_chunks_v1
```

通过字段过滤区分 owner/kb/document：

```text
owner_id
kb_id
document_id
chunk_id
```

未来数据量变大后，可以改为：

```text
agentcanvas_chunks_user_{owner_id}
```

但现在没有必要。

### 7.7 ES Mapping：BM25 第一版

```json
{
  "settings": {
    "analysis": {
      "analyzer": {
        "default_text_analyzer": {
          "type": "standard"
        }
      }
    }
  },
  "mappings": {
    "properties": {
      "owner_id": { "type": "long" },
      "kb_id": { "type": "long" },
      "document_id": { "type": "long" },
      "chunk_id": { "type": "long" },
      "chunk_index": { "type": "integer" },
      "document_name": { "type": "keyword" },
      "file_type": { "type": "keyword" },
      "section_title": {
        "type": "text",
        "fields": {
          "keyword": { "type": "keyword", "ignore_above": 256 }
        }
      },
      "content": {
        "type": "text",
        "analyzer": "default_text_analyzer"
      },
      "content_hash": { "type": "keyword" },
      "page_no": { "type": "integer" },
      "token_count": { "type": "integer" },
      "metadata": { "type": "object", "enabled": true },
      "created_at": { "type": "date" },
      "updated_at": { "type": "date" }
    }
  }
}
```

中文检索可以先用 standard analyzer。后续如果想更好，可以加 IK 分词器，但这会增加 ES 插件依赖。第一版不建议一开始加。

### 7.8 ES 查询 DSL：关键词检索

```json
{
  "size": 8,
  "query": {
    "bool": {
      "filter": [
        { "term": { "owner_id": 1 } },
        { "terms": { "kb_id": [1, 2] } }
      ],
      "must": [
        {
          "multi_match": {
            "query": "用户问题",
            "fields": ["content^3", "section_title^2", "document_name"],
            "type": "best_fields"
          }
        }
      ]
    }
  },
  "highlight": {
    "fields": {
      "content": {}
    }
  }
}
```

### 7.9 数据库表：knowledge_bases

```sql
CREATE TABLE knowledge_bases (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    description TEXT,
    retrieval_backend VARCHAR(32) NOT NULL DEFAULT 'elasticsearch',
    retrieval_mode VARCHAR(32) NOT NULL DEFAULT 'keyword',
    chunk_method VARCHAR(64) NOT NULL DEFAULT 'fixed_token',
    chunk_size INT NOT NULL DEFAULT 800,
    chunk_overlap INT NOT NULL DEFAULT 100,
    status TINYINT NOT NULL DEFAULT 1,
    document_count INT NOT NULL DEFAULT 0,
    chunk_count INT NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME NULL,
    INDEX idx_owner_id (owner_id),
    INDEX idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 7.10 数据库表：documents

```sql
CREATE TABLE documents (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    kb_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    original_filename VARCHAR(255) NOT NULL,
    file_type VARCHAR(32),
    mime_type VARCHAR(128),
    file_size BIGINT NOT NULL DEFAULT 0,
    object_key VARCHAR(512) NOT NULL,
    content_hash VARCHAR(64),
    parser_status VARCHAR(32) NOT NULL DEFAULT 'pending',
    parser_error TEXT,
    chunk_count INT NOT NULL DEFAULT 0,
    token_count INT NOT NULL DEFAULT 0,
    indexed_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME NULL,
    INDEX idx_owner_kb (owner_id, kb_id),
    INDEX idx_parser_status (parser_status),
    INDEX idx_content_hash (content_hash)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

`parser_status` 取值：

```text
pending
parsing
chunking
indexing
completed
failed
```

### 7.11 数据库表：document_chunks

```sql
CREATE TABLE document_chunks (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    kb_id BIGINT NOT NULL,
    document_id BIGINT NOT NULL,
    chunk_index INT NOT NULL,
    content MEDIUMTEXT NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    token_count INT NOT NULL DEFAULT 0,
    char_count INT NOT NULL DEFAULT 0,
    page_no INT NULL,
    section_title VARCHAR(255),
    es_index VARCHAR(128),
    es_doc_id VARCHAR(128),
    metadata_json JSON,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE KEY uk_doc_chunk_index (document_id, chunk_index),
    INDEX idx_owner_kb (owner_id, kb_id),
    INDEX idx_document_id (document_id),
    INDEX idx_content_hash (content_hash)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

注意：

1. MySQL 保存 chunk 原文，便于引用和查看；
2. ES 保存可检索副本，便于搜索；
3. ES 的 doc_id 可以用 `chunk_id`；
4. 删除文档时，同时删除 MySQL chunk 和 ES doc。

### 7.12 数据库表：ingestion_jobs

```sql
CREATE TABLE ingestion_jobs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    kb_id BIGINT NOT NULL,
    document_id BIGINT NOT NULL,
    job_type VARCHAR(64) NOT NULL DEFAULT 'document_ingestion',
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    priority INT NOT NULL DEFAULT 0,
    attempt_count INT NOT NULL DEFAULT 0,
    max_attempts INT NOT NULL DEFAULT 3,
    error_message TEXT,
    locked_by VARCHAR(128),
    locked_at DATETIME NULL,
    started_at DATETIME NULL,
    finished_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    INDEX idx_status_priority (status, priority, created_at),
    INDEX idx_document_id (document_id),
    INDEX idx_owner_id (owner_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

Worker 处理逻辑：

```text
1. SELECT pending job
2. 使用事务把 status 改为 processing，并写 locked_by
3. 下载 MinIO 文件
4. 解析
5. 切片
6. 保存 chunks
7. 写 ES
8. 更新 document 状态 completed
9. 更新 job 状态 completed
```

### 7.13 数据库表：retrieval_logs

```sql
CREATE TABLE retrieval_logs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    kb_ids JSON NOT NULL,
    query_text TEXT NOT NULL,
    retrieval_backend VARCHAR(32) NOT NULL,
    retrieval_mode VARCHAR(32) NOT NULL,
    top_k INT NOT NULL,
    result_count INT NOT NULL,
    latency_ms INT NOT NULL DEFAULT 0,
    results_json JSON,
    created_at DATETIME NOT NULL,
    INDEX idx_owner_id (owner_id),
    INDEX idx_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

这个表非常重要。它让你的项目不是黑盒 RAG，而是可调试 RAG。

### 7.14 文件解析策略

第一版支持：

| 文件类型 | 解析方式 |
|---|---|
| txt | 直接读取 |
| md | 按 heading 辅助切片 |
| pdf | 先使用 Go PDF 文本提取库或外部 pdftotext |
| docx | 后置 |
| xlsx | 后置 |
| html | 后置 |

PDF 不要一开始做复杂 OCR。只做普通文本型 PDF。

### 7.15 切片策略

第一版做 fixed_token：

```text
chunk_size = 800 tokens
chunk_overlap = 100 tokens
```

为了简单，可以先用字符估算 token：

```text
中文：1 字 ≈ 1 token
英文：4 chars ≈ 1 token
```

后期再接 tokenizer。

### 7.16 本阶段 API

```text
POST   /api/v1/knowledge-bases
GET    /api/v1/knowledge-bases
GET    /api/v1/knowledge-bases/{id}
PATCH  /api/v1/knowledge-bases/{id}
DELETE /api/v1/knowledge-bases/{id}

POST   /api/v1/knowledge-bases/{id}/documents
GET    /api/v1/knowledge-bases/{id}/documents
GET    /api/v1/documents/{id}
DELETE /api/v1/documents/{id}
GET    /api/v1/documents/{id}/chunks

POST   /api/v1/knowledge-bases/{id}/search
GET    /api/v1/ingestion-jobs/{id}
```

### 7.17 本阶段交付状态

本阶段结束后，项目可以展示：

1. 创建知识库；
2. 上传文件；
3. 后台异步处理；
4. 查看 chunk；
5. 使用 ES 搜索 chunk；
6. 查看高亮命中。

这时你已经有一个完整的检索系统了。

---

## 8. Phase 3：普通 RAG Chat，不做画布

### 8.1 阶段目标

本阶段在知识库基础上接入 LLM，完成最小 RAG 问答。

完成后用户可以：

1. 选择知识库；
2. 输入问题；
3. 系统检索 ES；
4. 将检索结果拼成 context；
5. 调用 LLM；
6. 流式返回答案；
7. 保存 conversation 和 messages；
8. 保存引用来源；
9. 保存 token usage。

这一阶段仍然不做画布。

### 8.2 阶段架构

```text
Browser Chat UI
   │
   │ POST /rag/chat/stream
   ▼
Go API Server
   ├── Conversation Usecase
   ├── Retriever Interface
   │     └── Elasticsearch Retriever
   ├── Prompt Builder
   ├── LLM Provider Client
   ├── SSE Stream Writer
   ├── Citation Builder
   └── Message Repository
```

### 8.3 RAG 调用链路

```text
User Question
  ↓
Create Conversation if needed
  ↓
Save User Message
  ↓
Retrieve chunks from ES
  ↓
Build Prompt
  ↓
Call LLM with streaming
  ↓
Stream tokens to frontend
  ↓
Save Assistant Message
  ↓
Save References
  ↓
Save Usage Log
```

### 8.4 Prompt 模板

```text
你是一个严谨的知识库问答助手。
请基于给定的知识库上下文回答用户问题。
如果上下文中没有答案，请明确说“不知道”，不要编造。
回答时尽量给出引用依据。

【知识库上下文】
{{context}}

【用户问题】
{{query}}
```

### 8.5 Context Pack 规则

检索结果不能直接无脑塞进 prompt。建议做一个 `ContextPacker`：

```go
type ContextPacker interface {
    Pack(ctx context.Context, results []RetrievalResult, budget TokenBudget) (*PackedContext, error)
}
```

规则：

1. 按 score 排序；
2. 同一文档最多取 N 个 chunk；
3. 超过 token budget 时截断；
4. 每个 chunk 添加引用编号；
5. 保留 document_id、chunk_id、page_no。

示例：

```text
[引用 1] 文档：Go并发.md，位置：chunk 3
内容：...

[引用 2] 文档：RAG论文.pdf，页码：5
内容：...
```

### 8.6 本阶段新增目录结构

```text
internal/
  domain/
    conversation/
      conversation.go
      message.go
      reference.go
      repository.go
    usage/
      model_usage.go
      repository.go
  application/
    chat_usecase/
      rag_chat.go
      stream_chat.go
      prompt_builder.go
      context_packer.go
  infrastructure/
    mysql/
      conversation_repo.go
      message_repo.go
      usage_repo.go
    llm/
      openai_compatible.go
      deepseek.go
      qwen.go
      ollama.go
  interface/
    http/
      handler/
        chat_handler.go
      sse/
        writer.go
```

### 8.7 数据库表：conversations

```sql
CREATE TABLE conversations (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    title VARCHAR(255),
    source VARCHAR(32) NOT NULL DEFAULT 'rag_chat',
    agent_id BIGINT NULL,
    last_message_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME NULL,
    INDEX idx_owner_id (owner_id),
    INDEX idx_agent_id (agent_id),
    INDEX idx_last_message_at (last_message_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 8.8 数据库表：messages

```sql
CREATE TABLE messages (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    conversation_id BIGINT NOT NULL,
    role VARCHAR(32) NOT NULL,
    content MEDIUMTEXT NOT NULL,
    content_type VARCHAR(32) NOT NULL DEFAULT 'text',
    run_id BIGINT NULL,
    token_count INT NOT NULL DEFAULT 0,
    metadata_json JSON,
    created_at DATETIME NOT NULL,
    INDEX idx_conversation_id (conversation_id),
    INDEX idx_owner_id (owner_id),
    INDEX idx_run_id (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

`role` 取值：

```text
user
assistant
system
tool
```

### 8.9 数据库表：message_references

```sql
CREATE TABLE message_references (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    message_id BIGINT NOT NULL,
    kb_id BIGINT NOT NULL,
    document_id BIGINT NOT NULL,
    chunk_id BIGINT NOT NULL,
    ref_index INT NOT NULL,
    score DOUBLE,
    quote_text TEXT,
    page_no INT NULL,
    metadata_json JSON,
    created_at DATETIME NOT NULL,
    INDEX idx_message_id (message_id),
    INDEX idx_chunk_id (chunk_id),
    INDEX idx_owner_id (owner_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 8.10 数据库表：model_usage_logs

```sql
CREATE TABLE model_usage_logs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    provider_id BIGINT,
    provider_type VARCHAR(64),
    model_name VARCHAR(128),
    usage_type VARCHAR(32) NOT NULL,
    prompt_tokens INT NOT NULL DEFAULT 0,
    completion_tokens INT NOT NULL DEFAULT 0,
    total_tokens INT NOT NULL DEFAULT 0,
    estimated_cost DECIMAL(12, 6) DEFAULT 0,
    latency_ms INT NOT NULL DEFAULT 0,
    success TINYINT NOT NULL DEFAULT 1,
    error_message TEXT,
    request_id VARCHAR(128),
    created_at DATETIME NOT NULL,
    INDEX idx_owner_id (owner_id),
    INDEX idx_provider_id (provider_id),
    INDEX idx_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 8.11 本阶段 API

```text
POST /api/v1/rag/chat
POST /api/v1/rag/chat/stream
GET  /api/v1/conversations
GET  /api/v1/conversations/{id}
GET  /api/v1/conversations/{id}/messages
DELETE /api/v1/conversations/{id}
```

### 8.12 本阶段交付状态

本阶段结束后，你已经有了一个可以演示的 RAG 应用：

1. 上传知识；
2. 检索知识；
3. 调 LLM 回答；
4. 展示引用；
5. 保存对话。

这个阶段是项目第一个重要里程碑。

---

## 9. Phase 4：Agent Flow DSL 与 Runtime

### 9.1 阶段目标

本阶段开始做 Agent 的核心：Flow DSL 和 Go Runtime。

注意：这一阶段可以先不做可视化画布，用 JSON DSL 手写测试。

完成后系统可以：

1. 创建 Agent；
2. 保存 Flow DSL；
3. 发布 Flow Version；
4. 执行 Flow；
5. 执行 Begin、Retrieval、Prompt、LLM、Message 节点；
6. 产生 run events；
7. 通过 SSE 返回节点事件和模型输出。

### 9.2 阶段架构

```text
API
  │
  │ Run Agent
  ▼
Agent Application Service
  │
  ├── Load Agent Flow Version
  ├── Validate DSL
  ├── Create Run
  └── Invoke Flow Runtime
          │
          ▼
Agent Core Runtime
  ├── Node Registry
  ├── DAG Validator
  ├── Executor
  ├── Run Context
  ├── Event Emitter
  └── Node Implementations
          │
          ├── Retriever Tool
          ├── LLM Tool
          └── Message Writer
```

### 9.3 Flow DSL 设计

最小 DSL：

```json
{
  "schema_version": "v1",
  "flow_id": "flow_demo",
  "nodes": [
    {
      "id": "begin_1",
      "type": "begin",
      "name": "开始",
      "config": {
        "input_schema": {
          "query": "string"
        }
      }
    },
    {
      "id": "retrieval_1",
      "type": "knowledge_retrieval",
      "name": "检索知识库",
      "config": {
        "kb_ids": [1],
        "top_k": 8,
        "mode": "keyword"
      }
    },
    {
      "id": "prompt_1",
      "type": "prompt",
      "name": "构造提示词",
      "config": {
        "template": "请基于上下文回答问题。\n上下文：{{retrieval_1.context}}\n问题：{{sys.query}}"
      }
    },
    {
      "id": "llm_1",
      "type": "llm",
      "name": "调用模型",
      "config": {
        "provider_id": 1,
        "model": "deepseek-chat",
        "temperature": 0.2,
        "stream": true
      }
    },
    {
      "id": "message_1",
      "type": "message",
      "name": "输出消息",
      "config": {
        "content": "{{llm_1.content}}",
        "with_citation": true
      }
    }
  ],
  "edges": [
    { "from": "begin_1", "to": "retrieval_1" },
    { "from": "retrieval_1", "to": "prompt_1" },
    { "from": "prompt_1", "to": "llm_1" },
    { "from": "llm_1", "to": "message_1" }
  ]
}
```

### 9.4 Node 接口

```go
type Node interface {
    Type() string
    Validate(config json.RawMessage) error
    Run(ctx context.Context, rc *RunContext, input NodeInput) (NodeOutput, error)
}
```

### 9.5 Node Registry

```go
type Registry struct {
    factories map[string]NodeFactory
}

type NodeFactory func(deps NodeDeps) Node

func (r *Registry) Register(nodeType string, factory NodeFactory) {
    r.factories[nodeType] = factory
}
```

注册：

```go
registry.Register("begin", NewBeginNode)
registry.Register("knowledge_retrieval", NewKnowledgeRetrievalNode)
registry.Register("prompt", NewPromptNode)
registry.Register("llm", NewLLMNode)
registry.Register("message", NewMessageNode)
```

### 9.6 RunContext 设计

```go
type RunContext struct {
    OwnerID        int64
    AgentID        int64
    FlowVersionID  int64
    RunID          int64
    ConversationID *int64
    Input          map[string]any
    Variables      map[string]any
    NodeOutputs    map[string]NodeOutput
    Events         EventEmitter
    Retriever      Retriever
    LLMFactory     LLMFactory
    MessageWriter  MessageWriter
}
```

### 9.7 运行事件设计

事件类型：

```text
workflow_started
node_started
node_finished
node_failed
retrieval_started
retrieval_finished
llm_started
llm_delta
llm_finished
message_created
workflow_finished
workflow_failed
```

SSE 示例：

```text
event: node_started
data: {"run_id":1,"node_id":"retrieval_1","node_type":"knowledge_retrieval"}

event: retrieval_finished
data: {"run_id":1,"node_id":"retrieval_1","result_count":8}

event: llm_delta
data: {"run_id":1,"node_id":"llm_1","delta":"你好"}
```

### 9.8 本阶段新增目录结构

```text
internal/
  domain/
    agent/
      agent.go
      flow_version.go
      run.go
      run_event.go
      node_log.go
      repository.go
    flow/
      dsl.go
      node.go
      edge.go
      validator.go
  runtime/
    engine/
      executor.go
      scheduler.go
      context.go
      event_emitter.go
      variable_resolver.go
    node/
      registry.go
      begin_node.go
      retrieval_node.go
      prompt_node.go
      llm_node.go
      message_node.go
    event/
      event.go
      sse_emitter.go
      db_emitter.go
  application/
    agent_usecase/
      create_agent.go
      save_flow.go
      publish_flow.go
      run_agent.go
      get_run.go
  infrastructure/
    mysql/
      agent_repo.go
      flow_version_repo.go
      run_repo.go
      run_event_repo.go
  interface/
    http/
      handler/
        agent_handler.go
        flow_handler.go
        run_handler.go
```

### 9.9 数据库表：agents

```sql
CREATE TABLE agents (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    description TEXT,
    avatar_url VARCHAR(512),
    current_version_id BIGINT NULL,
    status TINYINT NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME NULL,
    INDEX idx_owner_id (owner_id),
    INDEX idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 9.10 数据库表：agent_flow_versions

```sql
CREATE TABLE agent_flow_versions (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    agent_id BIGINT NOT NULL,
    version_no INT NOT NULL,
    dsl_json JSON NOT NULL,
    description TEXT,
    is_draft TINYINT NOT NULL DEFAULT 1,
    is_published TINYINT NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE KEY uk_agent_version (agent_id, version_no),
    INDEX idx_owner_agent (owner_id, agent_id),
    INDEX idx_published (is_published)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 9.11 数据库表：agent_runs

```sql
CREATE TABLE agent_runs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    agent_id BIGINT NOT NULL,
    flow_version_id BIGINT NOT NULL,
    conversation_id BIGINT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'running',
    input_json JSON,
    output_json JSON,
    error_message TEXT,
    total_tokens INT NOT NULL DEFAULT 0,
    latency_ms INT NOT NULL DEFAULT 0,
    started_at DATETIME NOT NULL,
    finished_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    INDEX idx_owner_id (owner_id),
    INDEX idx_agent_id (agent_id),
    INDEX idx_conversation_id (conversation_id),
    INDEX idx_status (status),
    INDEX idx_started_at (started_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

`status` 取值：

```text
running
succeeded
failed
cancelled
```

### 9.12 数据库表：agent_run_events

```sql
CREATE TABLE agent_run_events (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    run_id BIGINT NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    node_id VARCHAR(128),
    node_type VARCHAR(64),
    payload_json JSON,
    created_at DATETIME NOT NULL,
    INDEX idx_run_id (run_id),
    INDEX idx_owner_id (owner_id),
    INDEX idx_event_type (event_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 9.13 数据库表：agent_node_logs

```sql
CREATE TABLE agent_node_logs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    run_id BIGINT NOT NULL,
    node_id VARCHAR(128) NOT NULL,
    node_type VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    input_json JSON,
    output_json JSON,
    error_message TEXT,
    token_count INT NOT NULL DEFAULT 0,
    latency_ms INT NOT NULL DEFAULT 0,
    started_at DATETIME NOT NULL,
    finished_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    INDEX idx_run_id (run_id),
    INDEX idx_node (node_id, node_type),
    INDEX idx_owner_id (owner_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 9.14 本阶段 API

```text
POST   /api/v1/agents
GET    /api/v1/agents
GET    /api/v1/agents/{id}
PATCH  /api/v1/agents/{id}
DELETE /api/v1/agents/{id}

POST   /api/v1/agents/{id}/flow-versions
GET    /api/v1/agents/{id}/flow-versions
GET    /api/v1/flow-versions/{id}
POST   /api/v1/flow-versions/{id}/publish
POST   /api/v1/flow-versions/{id}/validate

POST   /api/v1/agents/{id}/runs
POST   /api/v1/agents/{id}/runs/stream
GET    /api/v1/runs/{id}
GET    /api/v1/runs/{id}/events
GET    /api/v1/runs/{id}/node-logs
POST   /api/v1/runs/{id}/cancel
```

### 9.15 本阶段交付状态

本阶段结束后，即使没有画布，也可以用 JSON DSL 创建 Agent 并运行。

这是整个项目技术含金量最高的第一个阶段。

---

## 10. Phase 5：前端画布与 Agent 调试

### 10.1 阶段目标

本阶段做可视化呈现。

完成后用户可以：

1. 打开 Agent 编辑页；
2. 拖拽节点；
3. 配置节点；
4. 连线；
5. 保存 DSL；
6. 点击运行；
7. 看到运行事件；
8. 看到流式回答；
9. 查看节点日志。

### 10.2 阶段架构

```text
Frontend
  ├── Agent List
  ├── Flow Canvas
  ├── Node Panel
  ├── Config Panel
  ├── Debug Console
  ├── Run Event Timeline
  └── Chat Preview

Backend
  ├── Agent API
  ├── Flow API
  ├── Run API
  └── SSE API
```

### 10.3 前端目录结构

如果用 React：

```text
web/
  src/
    app/
      router.tsx
      providers.tsx
    api/
      client.ts
      auth.ts
      knowledge.ts
      agent.ts
      run.ts
    pages/
      login/
      dashboard/
      knowledge/
      agents/
      agent-editor/
      conversations/
      settings/
    components/
      layout/
      form/
      table/
      code-editor/
    features/
      auth/
        LoginPage.tsx
        GitHubLoginButton.tsx
      knowledge/
        KnowledgeBaseList.tsx
        DocumentUpload.tsx
        ChunkViewer.tsx
        RetrievalTester.tsx
      agent-canvas/
        AgentCanvasPage.tsx
        Canvas.tsx
        NodePalette.tsx
        ConfigPanel.tsx
        nodes/
          BeginNode.tsx
          RetrievalNode.tsx
          PromptNode.tsx
          LLMNode.tsx
          MessageNode.tsx
        dsl/
          toDSL.ts
          fromDSL.ts
          validateDSL.ts
      run-debugger/
        RunConsole.tsx
        EventTimeline.tsx
        NodeLogPanel.tsx
        StreamOutput.tsx
    stores/
      authStore.ts
      canvasStore.ts
    types/
      agent.ts
      flow.ts
      knowledge.ts
```

### 10.4 画布节点配置

#### Begin Node

配置：

```json
{
  "input_schema": {
    "query": "string"
  }
}
```

#### Retrieval Node

配置：

```json
{
  "kb_ids": [1],
  "top_k": 8,
  "mode": "keyword"
}
```

#### Prompt Node

配置：

```json
{
  "template": "请根据上下文回答：{{retrieval_1.context}}\n问题：{{sys.query}}"
}
```

#### LLM Node

配置：

```json
{
  "provider_id": 1,
  "model": "deepseek-chat",
  "temperature": 0.2,
  "stream": true
}
```

#### Message Node

配置：

```json
{
  "content": "{{llm_1.content}}",
  "with_citation": true
}
```

### 10.5 调试台设计

调试台应该显示：

```text
左侧：用户输入
中间：流式回答
右侧：节点事件
下方：当前选中节点 input/output
```

事件示例：

```text
[10:01:01] workflow_started
[10:01:01] begin_1 started
[10:01:01] begin_1 finished
[10:01:02] retrieval_1 started
[10:01:02] retrieval_1 finished, result_count=8
[10:01:03] llm_1 started
[10:01:05] llm_1 streaming...
[10:01:08] message_1 finished
[10:01:08] workflow_finished
```

### 10.6 本阶段交付状态

本阶段结束后，你的项目从“后端 AI 工程”变成“可视化 Agent 平台”。

这个阶段适合录制 demo 视频。

---

## 11. Phase 6：Memory、Tool、Guardrail

### 11.1 阶段目标

本阶段让 Agent 不只是 RAG，而是更像真正的 Agent 编排系统。

新增能力：

1. Memory Read；
2. Memory Write；
3. HTTP Tool；
4. Switch；
5. JSON Output；
6. Guardrail；
7. Tool 调用日志。

### 11.2 阶段架构

```text
Agent Runtime
  ├── Memory Node
  │     └── MySQL Memory Store
  ├── Tool Node
  │     ├── HTTP Tool Executor
  │     └── Tool Permission Check
  ├── Switch Node
  ├── Guardrail Node
  └── Output Formatter
```

### 11.3 Memory 设计

Memory 类型：

```text
profile_memory       长期偏好
summary_memory       对话摘要
episodic_memory      某次事件记忆
task_memory          当前任务状态
```

Memory 不要一开始就搞向量记忆。先用 MySQL 即可。

### 11.4 Prompt 注入顺序

建议顺序：

```text
System Prompt
  ↓
Long-term Memory
  ↓
Conversation Summary
  ↓
Retrieved Knowledge Context
  ↓
Recent Messages
  ↓
Current User Query
```

Memory 和 RAG 的区别：

| 类型 | 作用 |
|---|---|
| Memory | 用户偏好、历史状态、长期上下文 |
| RAG | 外部文档、知识证据、可引用来源 |

不要把它们混在同一个字段里。

### 11.5 数据库表：memories

```sql
CREATE TABLE memories (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    conversation_id BIGINT NULL,
    memory_type VARCHAR(64) NOT NULL,
    title VARCHAR(255),
    content TEXT NOT NULL,
    importance DOUBLE NOT NULL DEFAULT 0.5,
    source VARCHAR(64),
    metadata_json JSON,
    last_used_at DATETIME NULL,
    expires_at DATETIME NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME NULL,
    INDEX idx_owner_type (owner_id, memory_type),
    INDEX idx_conversation_id (conversation_id),
    INDEX idx_importance (importance)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 11.6 数据库表：memory_write_logs

```sql
CREATE TABLE memory_write_logs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    memory_id BIGINT,
    run_id BIGINT,
    source_message_id BIGINT,
    action VARCHAR(32) NOT NULL,
    before_json JSON,
    after_json JSON,
    reason TEXT,
    created_at DATETIME NOT NULL,
    INDEX idx_owner_id (owner_id),
    INDEX idx_run_id (run_id),
    INDEX idx_memory_id (memory_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 11.7 Tool 设计原则

Tool 是危险点，不要随便让 Agent 调。

第一版只做 HTTP Tool，并且加限制：

1. 只能调用用户配置的 URL；
2. 默认只允许 GET/POST；
3. 超时限制；
4. 响应大小限制；
5. 禁止访问内网地址；
6. 记录完整调用日志；
7. 不允许执行任意代码。

### 11.8 数据库表：tool_definitions

```sql
CREATE TABLE tool_definitions (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    tool_type VARCHAR(64) NOT NULL,
    description TEXT,
    config_json JSON NOT NULL,
    input_schema_json JSON,
    output_schema_json JSON,
    status TINYINT NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME NULL,
    INDEX idx_owner_id (owner_id),
    INDEX idx_tool_type (tool_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 11.9 数据库表：tool_invocations

```sql
CREATE TABLE tool_invocations (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    run_id BIGINT,
    node_id VARCHAR(128),
    tool_id BIGINT,
    tool_name VARCHAR(128),
    tool_type VARCHAR(64),
    input_json JSON,
    output_json JSON,
    status VARCHAR(32) NOT NULL,
    error_message TEXT,
    latency_ms INT NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    INDEX idx_owner_id (owner_id),
    INDEX idx_run_id (run_id),
    INDEX idx_tool_id (tool_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 11.10 Guardrail 设计

第一版 Guardrail 不要上复杂模型审核。先做规则型：

1. 输出长度限制；
2. 禁止泄漏 API Key；
3. 禁止输出系统 prompt；
4. 检查是否存在 citation；
5. JSON schema 校验；
6. 敏感词表。

后期再接模型审核。

### 11.11 新增节点

#### Memory Read Node

输入：

```json
{
  "memory_types": ["profile_memory", "summary_memory"],
  "limit": 5
}
```

输出：

```json
{
  "memories": [
    {"id": 1, "content": "用户偏好使用 Go 语言实现项目"}
  ]
}
```

#### Memory Write Node

配置：

```json
{
  "memory_type": "summary_memory",
  "content": "{{llm_1.summary}}"
}
```

#### HTTP Tool Node

配置：

```json
{
  "tool_id": 1,
  "input": {
    "query": "{{sys.query}}"
  }
}
```

#### Switch Node

配置：

```json
{
  "conditions": [
    {
      "expr": "{{retrieval_1.result_count}} > 0",
      "target": "llm_1"
    },
    {
      "expr": "default",
      "target": "message_no_context"
    }
  ]
}
```

### 11.12 本阶段交付状态

本阶段结束后，Agent 已经具备：

1. 记忆；
2. 工具调用；
3. 条件分支；
4. 输出校验；
5. 安全日志。

这个阶段完成后，项目就明显区别于普通 RAG 项目。

---

## 12. Phase 7：ES 向量检索、Hybrid、Rerank、可观测性

### 12.1 阶段目标

本阶段把检索系统从 BM25 升级成可演进检索平台。

新增能力：

1. Embedding Provider；
2. ES dense_vector 字段；
3. ES kNN 检索；
4. keyword/vector/hybrid 三种模式；
5. Rerank 接口；
6. OpenTelemetry；
7. Prometheus/Grafana；
8. Redis Stream 或 NATS。

### 12.2 阶段架构

```text
Document Ingestion
  ├── Parse
  ├── Chunk
  ├── Embedding
  ├── Save Chunk MySQL
  └── Index to ES with dense_vector

Retrieval
  ├── Keyword Retriever
  ├── Vector Retriever
  ├── Hybrid Retriever
  └── Reranker

Observability
  ├── Logs
  ├── Metrics
  └── Traces
```

### 12.3 ES dense_vector Mapping

后期可以把 mapping 升级为：

```json
{
  "mappings": {
    "properties": {
      "owner_id": { "type": "long" },
      "kb_id": { "type": "long" },
      "document_id": { "type": "long" },
      "chunk_id": { "type": "long" },
      "content": { "type": "text" },
      "content_vector": {
        "type": "dense_vector",
        "dims": 1536,
        "index": true,
        "similarity": "cosine"
      },
      "embedding_model": { "type": "keyword" }
    }
  }
}
```

注意：不同 embedding model 的维度可能不同。不要把不同维度模型混进同一个 vector field。可选策略：

1. 每种 embedding model 一个 index；
2. 固定全系统默认 embedding model；
3. knowledge_base 绑定 embedding model。

建议第一版向量检索时采用第 3 种：一个知识库绑定一个 embedding model。

### 12.4 Embedding 表

```sql
CREATE TABLE chunk_embeddings (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    kb_id BIGINT NOT NULL,
    document_id BIGINT NOT NULL,
    chunk_id BIGINT NOT NULL,
    provider_id BIGINT,
    embedding_model VARCHAR(128) NOT NULL,
    embedding_dim INT NOT NULL,
    vector_backend VARCHAR(32) NOT NULL DEFAULT 'elasticsearch',
    vector_index VARCHAR(128),
    vector_doc_id VARCHAR(128),
    content_hash VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'completed',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE KEY uk_chunk_model (chunk_id, embedding_model),
    INDEX idx_owner_kb (owner_id, kb_id),
    INDEX idx_document_id (document_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
```

### 12.5 检索模式

#### keyword

只使用 ES BM25。

适合：

1. 专有名词；
2. API 名称；
3. 代码关键字；
4. 精确匹配。

#### vector

使用 embedding + kNN。

适合：

1. 语义相近；
2. 用户表达与文档措辞不同；
3. 自然语言问答。

#### hybrid

结合 keyword 和 vector。

适合：

1. 技术文档；
2. 中英文混合；
3. 既有关键词又有语义表达的场景。

### 12.6 Rerank 接口

```go
type Reranker interface {
    Rerank(ctx context.Context, query string, docs []RetrievalResult, topN int) ([]RetrievalResult, error)
}
```

第一版可以做 no-op reranker：

```go
type NoopReranker struct{}
```

后期接：

1. bge-reranker；
2. provider rerank API；
3. 本地 rerank service。

### 12.7 可观测性

你需要记录三类东西：

1. logs：具体发生了什么；
2. metrics：系统整体指标；
3. traces：一次请求经过哪些组件。

Agent Run 最适合做 trace：

```text
HTTP request span
  └── agent.run span
      ├── node.begin span
      ├── node.retrieval span
      │   └── elasticsearch.search span
      ├── node.prompt span
      ├── node.llm span
      └── node.message span
```

这部分是面试加分项。

### 12.8 本阶段交付状态

本阶段结束后，你的项目已经具有“生产级 AI 工程”的味道：

1. 支持多检索模式；
2. 支持 embedding；
3. 支持 hybrid；
4. 支持 rerank；
5. 支持可观测性；
6. 支持队列化 ingestion。

---

## 13. 后端完整目录结构建议

最终后端目录建议如下：

```text
agentcanvas/
  cmd/
    api/
      main.go
    worker/
      main.go
    migrate/
      main.go

  configs/
    config.yaml
    config.local.yaml
    config.prod.yaml

  deployments/
    docker-compose.yml
    docker-compose.dev.yml
    docker-compose.observability.yml
    elasticsearch/
      init-index.sh
    minio/
      init-bucket.sh

  migrations/
    000001_create_users.up.sql
    000002_create_auth.up.sql
    000003_create_providers.up.sql
    000004_create_knowledge.up.sql
    000005_create_conversation.up.sql
    000006_create_agent.up.sql
    000007_create_memory_tool.up.sql
    000008_create_embedding.up.sql

  internal/
    bootstrap/
      app.go
      deps.go
      server.go
      worker.go

    interface/
      http/
        router.go
        middleware/
          auth.go
          cors.go
          request_id.go
          recovery.go
          rate_limit.go
        handler/
          health_handler.go
          auth_handler.go
          oauth_handler.go
          provider_handler.go
          knowledge_handler.go
          document_handler.go
          retrieval_handler.go
          chat_handler.go
          agent_handler.go
          flow_handler.go
          run_handler.go
          memory_handler.go
          tool_handler.go
        sse/
          writer.go
          event.go

    application/
      auth_usecase/
      provider_usecase/
      knowledge_usecase/
      ingestion_usecase/
      chat_usecase/
      agent_usecase/
      memory_usecase/
      tool_usecase/
      audit_usecase/

    domain/
      user/
      auth/
      provider/
      knowledge/
      retrieval/
      conversation/
      agent/
      flow/
      memory/
      tool/
      usage/
      audit/

    runtime/
      engine/
      node/
      event/
      guardrail/
      variable/

    infrastructure/
      mysql/
      redis/
      minio/
      elasticsearch/
      retrieval/
        elasticsearch/
        noop/
      llm/
        openai_compatible.go
        deepseek.go
        qwen.go
        ollama.go
      embedding/
        openai_compatible.go
        local.go
      parser/
      chunker/
      crypto/
      oauth/
      queue/
        mysql_job_queue.go
        redis_stream_queue.go
        nats_queue.go
      telemetry/

    pkg/
      config/
      logger/
      errors/
      response/
      idgen/
      ptr/
      timeutil/
      jsonutil/

  web/
    src/
      pages/
      features/
      components/
      api/
      stores/
      types/

  scripts/
    dev.sh
    migrate.sh
    create_es_index.sh

  Makefile
  README.md
```

---

## 14. 完整数据表清单

最终表按模块分类如下。

### 14.1 用户与认证

```text
users
oauth_accounts
auth_sessions
api_tokens
```

### 14.2 模型与密钥

```text
model_providers
model_usage_logs
```

### 14.3 知识库

```text
knowledge_bases
documents
document_chunks
ingestion_jobs
retrieval_logs
chunk_embeddings
```

### 14.4 对话

```text
conversations
messages
message_references
```

### 14.5 Agent Flow

```text
agents
agent_flow_versions
agent_runs
agent_run_events
agent_node_logs
```

### 14.6 Memory 与 Tool

```text
memories
memory_write_logs
tool_definitions
tool_invocations
```

### 14.7 审计

```text
audit_logs
```

当前不需要：

```text
tenants
workspaces
workspace_members
workspace_invites
workspace_roles
```

---

## 15. 完整数据库设计说明

这一节不重复贴 SQL，而是解释每类数据应该放在哪里。

### 15.1 MySQL 放什么

MySQL 存强一致元数据：

1. 用户；
2. 登录会话；
3. Provider 配置；
4. 知识库元数据；
5. 文档元数据；
6. chunk 原文；
7. Agent DSL；
8. Flow Version；
9. Run；
10. Run Events；
11. Message；
12. Memory；
13. Tool 定义；
14. Audit Log。

### 15.2 MinIO 放什么

MinIO 存大对象：

1. 用户上传的原始文件；
2. 后期解析出的中间文件；
3. 后期生成的图片、OCR 文件；
4. 可能的导出文件。

Object Key 设计：

```text
users/{owner_id}/knowledge-bases/{kb_id}/documents/{document_id}/raw/{filename}
users/{owner_id}/exports/{export_id}/{filename}
```

虽然路径里有 `users/{owner_id}`，但这不是 workspace，只是对象存储的命名空间。

### 15.3 Elasticsearch 放什么

ES 存检索文档：

1. chunk content；
2. section title；
3. document name；
4. metadata；
5. owner_id；
6. kb_id；
7. document_id；
8. chunk_id；
9. 后期 content_vector。

ES 是检索索引，不是事实源。事实源仍然是 MySQL + MinIO。

### 15.4 Redis 放什么

Redis 存短期状态：

1. OAuth state；
2. rate limit counter；
3. run cancel flag；
4. SSE 临时状态；
5. 分布式锁；
6. 后期 Redis Stream queue。

### 15.5 什么不应该放数据库

不要把这些东西明文放数据库：

1. API Key 明文；
2. 密码明文；
3. refresh token 明文；
4. GitHub access token 明文；
5. 系统加密主密钥；
6. 完整 LLM 请求中可能包含的敏感 header。

### 15.6 运行时资源访问约定

Phase 0 当前采用 `bootstrap.NewApp` 统一初始化 MySQL、Redis、MinIO、Elasticsearch，并通过构造函数把依赖传给 handler / usecase / repository。

后期如果为了开发便利，希望支持类似 `GetDB()`、`GetCache()` 的访问方式，可以增加一个受控的运行时资源容器，例如：

```text
internal/bootstrap/runtime.go
```

示例职责：

```go
func GetDB() *gorm.DB
func GetCache() *redis.Client
func GetMinIO() *minio.Client
func GetElasticsearch() *elasticsearch.Client
```

使用边界：

1. 优先使用构造函数注入依赖，保持模块依赖清晰；
2. `GetDB()` / `GetCache()` 只作为工程便利入口，不作为业务分层的主要依赖方式；
3. repository、usecase、runtime node 更推荐显式持有依赖；
4. 如果加入全局资源访问器，必须在应用启动阶段完成初始化，未初始化时应直接 panic 或返回明确错误；
5. 测试中要能替换这些资源，避免全局状态污染测试。

这个方案保留了小项目开发便利性，但不改变整体依赖注入为主的架构方向。

---

## 16. Retrieval 抽象与 ES 实现

### 16.1 为什么必须抽象

虽然第一版用 ES，但你不能让 Agent Core 直接依赖 ES。

错误做法：

```go
func (n *RetrievalNode) Run(...) {
    es.Search(...)
}
```

正确做法：

```go
func (n *RetrievalNode) Run(...) {
    results, err := n.retriever.Search(ctx, req)
}
```

这样后续可以替换：

```text
ElasticsearchRetriever
MilvusRetriever
QdrantRetriever
HybridRetriever
MockRetriever
```

实现约定：

1. 第一版的真实实现放在 `internal/infrastructure/retrieval/elasticsearch`；
2. `internal/domain/retrieval` 只放接口、请求结构、返回结构和通用类型；
3. application、runtime、node 层只依赖 `Retriever` 接口；
4. 不要在 Agent 节点里直接调用 Elasticsearch client；
5. 如果后续引入 Milvus/Qdrant，只新增新的 infrastructure 实现，不改上层调用方式。

### 16.2 接口分层

建议拆成三层：

```go
type ChunkIndexer interface {
    IndexChunks(ctx context.Context, docs []ChunkIndexDocument) error
    DeleteByDocument(ctx context.Context, ownerID, documentID int64) error
}

type KeywordSearcher interface {
    KeywordSearch(ctx context.Context, req KeywordSearchRequest) ([]RetrievalResult, error)
}

type VectorSearcher interface {
    VectorSearch(ctx context.Context, req VectorSearchRequest) ([]RetrievalResult, error)
}
```

再组合：

```go
type Retriever interface {
    Search(ctx context.Context, req RetrievalRequest) (*RetrievalResponse, error)
}
```

### 16.3 RetrievalRequest

```go
type RetrievalRequest struct {
    OwnerID int64
    KBIDs []int64
    Query string
    QueryVector []float32
    TopK int
    Mode RetrievalMode
    Filters map[string]any
    EnableHighlight bool
}

type RetrievalMode string

const (
    RetrievalModeKeyword RetrievalMode = "keyword"
    RetrievalModeVector  RetrievalMode = "vector"
    RetrievalModeHybrid  RetrievalMode = "hybrid"
)
```

### 16.4 RetrievalResponse

```go
type RetrievalResponse struct {
    Query string
    Mode RetrievalMode
    Results []RetrievalResult
    LatencyMS int64
    Backend string
}
```

### 16.5 Agent 节点如何使用 Retrieval

Retrieval Node 不关心底层是 ES 还是 Milvus：

```go
func (n *KnowledgeRetrievalNode) Run(ctx context.Context, rc *RunContext, input NodeInput) (NodeOutput, error) {
    query := rc.ResolveString(n.config.QueryTemplate)
    resp, err := n.retriever.Search(ctx, RetrievalRequest{
        OwnerID: rc.OwnerID,
        KBIDs: n.config.KBIDs,
        Query: query,
        TopK: n.config.TopK,
        Mode: n.config.Mode,
        EnableHighlight: true,
    })
    if err != nil {
        return NodeOutput{}, err
    }
    return NodeOutput{
        Data: map[string]any{
            "results": resp.Results,
            "context": BuildContext(resp.Results),
            "result_count": len(resp.Results),
        },
    }, nil
}
```

---

## 17. Agent 节点清单与实现顺序

### 17.1 第一批节点：必须完成

| 节点 | 作用 | 阶段 |
|---|---|---|
| Begin | 接收用户输入 | Phase 4 |
| Knowledge Retrieval | 检索知识库 | Phase 4 |
| Prompt | 构造 prompt | Phase 4 |
| LLM | 调用大模型 | Phase 4 |
| Message | 输出消息 | Phase 4 |

### 17.2 第二批节点：项目进阶

| 节点 | 作用 | 阶段 |
|---|---|---|
| Switch | 条件分支 | Phase 6 |
| Memory Read | 读取记忆 | Phase 6 |
| Memory Write | 写入记忆 | Phase 6 |
| HTTP Tool | 调用外部 HTTP API | Phase 6 |
| JSON Output | 结构化输出 | Phase 6 |
| Guardrail | 输出检查 | Phase 6 |

### 17.3 第三批节点：后期扩展

| 节点 | 作用 |
|---|---|
| Loop | 循环执行 |
| Parallel | 并行节点 |
| Join | 合并输出 |
| Human Review | 人工确认 |
| SQL Tool | 查询数据库 |
| MCP Tool | 接入 MCP 生态 |
| Code Sandbox | 安全代码执行 |
| Web Search | 联网搜索 |

第三批节点不要太早做。不然会稀释核心工作。

---

## 18. API 总览

### 18.1 Auth

```text
POST /api/v1/auth/register
POST /api/v1/auth/login
POST /api/v1/auth/refresh
POST /api/v1/auth/logout
GET  /api/v1/auth/me
GET  /api/v1/auth/github/redirect
GET  /api/v1/auth/github/callback
```

### 18.2 Provider

```text
GET    /api/v1/model-providers
POST   /api/v1/model-providers
GET    /api/v1/model-providers/{id}
PATCH  /api/v1/model-providers/{id}
DELETE /api/v1/model-providers/{id}
POST   /api/v1/model-providers/{id}/test
```

### 18.3 Knowledge

```text
POST   /api/v1/knowledge-bases
GET    /api/v1/knowledge-bases
GET    /api/v1/knowledge-bases/{id}
PATCH  /api/v1/knowledge-bases/{id}
DELETE /api/v1/knowledge-bases/{id}
POST   /api/v1/knowledge-bases/{id}/documents
GET    /api/v1/knowledge-bases/{id}/documents
POST   /api/v1/knowledge-bases/{id}/search
```

### 18.4 Document

```text
GET    /api/v1/documents/{id}
DELETE /api/v1/documents/{id}
GET    /api/v1/documents/{id}/chunks
POST   /api/v1/documents/{id}/reindex
```

### 18.5 Chat

```text
POST /api/v1/rag/chat
POST /api/v1/rag/chat/stream
GET  /api/v1/conversations
GET  /api/v1/conversations/{id}
GET  /api/v1/conversations/{id}/messages
```

### 18.6 Agent

```text
POST   /api/v1/agents
GET    /api/v1/agents
GET    /api/v1/agents/{id}
PATCH  /api/v1/agents/{id}
DELETE /api/v1/agents/{id}
POST   /api/v1/agents/{id}/flow-versions
GET    /api/v1/agents/{id}/flow-versions
POST   /api/v1/flow-versions/{id}/validate
POST   /api/v1/flow-versions/{id}/publish
POST   /api/v1/agents/{id}/runs
POST   /api/v1/agents/{id}/runs/stream
GET    /api/v1/runs/{id}
GET    /api/v1/runs/{id}/events
GET    /api/v1/runs/{id}/node-logs
POST   /api/v1/runs/{id}/cancel
```

---

## 19. Docker Compose 建议

### 19.1 Phase 0-3 最小组件

```yaml
services:
  mysql:
    image: mysql:8.0
    environment:
      MYSQL_ROOT_PASSWORD: password
      MYSQL_DATABASE: agentcanvas
    ports:
      - "3306:3306"

  redis:
    image: redis:7
    ports:
      - "6379:6379"

  minio:
    image: minio/minio
    command: server /data --console-address ":9001"
    environment:
      MINIO_ROOT_USER: minioadmin
      MINIO_ROOT_PASSWORD: minioadmin
    ports:
      - "9000:9000"
      - "9001:9001"

  elasticsearch:
    image: docker.elastic.co/elasticsearch/elasticsearch:8.15.0
    environment:
      discovery.type: single-node
      xpack.security.enabled: "false"
      ES_JAVA_OPTS: -Xms1g -Xmx1g
    ports:
      - "9200:9200"

  api:
    build: ../../../Downloads
    depends_on:
      - mysql
      - redis
      - minio
      - elasticsearch
    ports:
      - "8080:8080"

  worker:
    build: ../../../Downloads
    command: [ "./worker" ]
    depends_on:
      - mysql
      - minio
      - elasticsearch
```

### 19.2 后期组件

后期再加：

```text
kibana
prometheus
grafana
otel-collector
nats
embedding-service
rerank-service
```

不要一开始加太多容器。你的第一目标是让项目跑起来。

---

## 20. 实现顺序与每周计划

### Week 1：Phase 0

完成：

1. Go 项目初始化；
2. Docker Compose；
3. MySQL/Redis/MinIO/ES 连接；
4. migration；
5. health API；
6. 日志和配置。

产出：

```text
可以启动完整本地开发环境。
```

### Week 2：Phase 1

完成：

1. users；
2. password login；
3. JWT；
4. refresh session；
5. GitHub OAuth；
6. Provider Key 加密；
7. audit log。

产出：

```text
可以登录系统并配置模型 Key。
```

### Week 3：Phase 2 上半

完成：

1. knowledge_bases；
2. documents；
3. MinIO 上传；
4. ingestion_jobs；
5. worker 框架。

产出：

```text
可以上传文件并创建处理任务。
```

### Week 4：Phase 2 下半

完成：

1. parser；
2. chunker；
3. document_chunks；
4. ES indexer；
5. KB search API；
6. retrieval_logs。

产出：

```text
可以上传文件、切片、写入 ES，并搜索 chunk。
```

### Week 5：Phase 3

完成：

1. conversations；
2. messages；
3. RAG prompt builder；
4. LLM 调用；
5. SSE stream；
6. message_references；
7. usage logs。

产出：

```text
可以进行普通 RAG 对话。
```

### Week 6-7：Phase 4

完成：

1. agents；
2. flow_versions；
3. Flow DSL；
4. Node Registry；
5. Runtime Executor；
6. Begin/Retrieval/Prompt/LLM/Message 节点；
7. run events；
8. node logs。

产出：

```text
可以用 JSON DSL 运行 Agent Flow。
```

### Week 8-9：Phase 5

完成：

1. 前端基础页面；
2. 知识库页面；
3. Agent 列表；
4. Flow Canvas；
5. 节点配置；
6. 保存 DSL；
7. 调试运行；
8. Event Timeline。

产出：

```text
可以可视化搭建并运行 Agent。
```

### Week 10-11：Phase 6

完成：

1. Memory；
2. HTTP Tool；
3. Switch；
4. Guardrail；
5. tool invocation logs。

产出：

```text
Agent 支持记忆、工具和输出管控。
```

### Week 12+：Phase 7

完成：

1. Embedding；
2. ES dense_vector；
3. vector search；
4. hybrid search；
5. rerank；
6. OpenTelemetry；
7. Prometheus/Grafana。

产出：

```text
项目进入高级 AI 工程阶段。
```

---

## 21. 推荐先做的最小可展示版本

如果你想尽快做出东西，最小版本只做这些：

### 21.1 后端

1. 登录可以先假登录；
2. Provider Key 配置；
3. 创建知识库；
4. 上传 txt/md；
5. 切片；
6. 写 ES；
7. 搜索；
8. RAG Chat；
9. Agent DSL 手写运行。

### 21.2 前端

1. 登录页；
2. Provider 设置页；
3. 知识库列表；
4. 文档上传页；
5. 检索测试页；
6. RAG Chat 页；
7. 简单 Agent 调试页。

画布可以晚一点做。

### 21.3 第一个 Demo 流程

```text
1. 登录
2. 配置 DeepSeek/OpenAI-compatible key
3. 创建知识库：Go 项目文档
4. 上传 markdown 文件
5. 系统切片并写入 ES
6. 在检索测试页搜索“Agent Flow Runtime 如何执行节点？”
7. 进入 RAG Chat 提问
8. 得到带引用的回答
9. 用 JSON DSL 创建 Agent
10. 运行 Agent，看到 node_started / retrieval_finished / llm_delta / workflow_finished
```

这个 Demo 就已经很有说服力。

---

## 22. 关键工程取舍总结

### 22.1 单人应用，不做 workspace

原因：

1. 降低复杂度；
2. 避免 tenant_id 污染所有逻辑；
3. 优先完成 Agent/RAG 核心；
4. 后续可通过 owner_id 平滑扩展。

### 22.2 先 ES，不先 Milvus

原因：

1. ES 适合做第一版全文检索；
2. 不需要一开始接 embedding；
3. 支持高亮、过滤、文档检索；
4. 后续可加 dense_vector；
5. 通过接口预留 Milvus/Qdrant。

### 22.3 先 MySQL Job，不先 MQ

原因：

1. 实现简单；
2. 状态清楚；
3. 方便 debug；
4. 后续可以抽象 Queue 接口。

### 22.4 先 JSON DSL，不先画布

原因：

1. Runtime 是核心；
2. 画布只是 DSL 编辑器；
3. 先手写 DSL 更容易测试后端；
4. 后续前端只需要生成同样 DSL。

### 22.5 密钥加密，token 哈希

原则：

```text
需要还原调用的：加密
只需要校验的：哈希
```

---

## 23. 面试讲解重点

你可以准备下面这些话术。

### 23.1 项目介绍

> AgentCanvas 是我用 Go 实现的单人版 AI Agent Flow + RAG 平台。它支持用户上传文档构建知识库，后台 worker 异步解析、切片并写入 Elasticsearch。用户可以通过画布搭建 Agent Flow，Flow 会保存成 DSL，由 Go Runtime 执行。运行过程支持 SSE 流式事件、RAG 检索、LLM 调用、引用溯源、Memory、Tool 和 Guardrail。

### 23.2 为什么去掉 workspace

> 第一版定位是个人 AI 工程项目，不是 SaaS 多租户平台。workspace 会引入成员、邀请、权限继承、数据隔离等复杂逻辑，但对 Agent Runtime 和 RAG 核心帮助不大。所以我保留 user/owner_id，去掉 tenant/workspace，把精力放在核心链路上。后期如果需要多人协作，可以新增 workspace_id 并迁移历史数据。

### 23.3 为什么先用 ES

> 第一阶段检索核心直接用 Elasticsearch，因为它的全文检索、高亮、过滤和文档索引能力成熟，可以先不依赖 embedding 就完成 RAG 闭环。同时我把检索层抽象成 Retriever/Indexer 接口，后续可以增加 ES dense_vector、Milvus 或 Qdrant 实现，不影响 Agent Runtime。

### 23.4 为什么要 Flow DSL

> 画布不能只是前端状态。用户拖拽出来的流程必须保存成稳定 DSL，后端根据 DSL 进行校验、版本化、执行和回放。这样 Agent 才能被发布、调试、复现和追踪。

### 23.5 为什么要 Run Event

> 可视化 Agent 最大的问题是黑盒调试困难。我为每次运行记录 workflow_started、node_started、node_finished、llm_delta、workflow_finished 等事件，同时保存每个节点 input/output/error/latency/token。这样用户可以看到 Agent 到底卡在哪个节点。

---

## 24. 参考资料

这些资料用于确认设计方向，实际项目以本文档的缩小版实现为准。

1. RAGFlow GitHub 仓库：`https://github.com/infiniflow/ragflow`
2. Elasticsearch kNN Search 官方文档：`https://www.elastic.co/docs/solutions/search/vector/knn`
3. Elasticsearch dense_vector 官方文档：`https://www.elastic.co/docs/reference/elasticsearch/mapping-reference/dense-vector`
4. Elasticsearch Vector Search 官方文档：`https://www.elastic.co/docs/solutions/search/vector`
5. GitHub OAuth Apps 官方文档：`https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps`
6. GitHub OAuth App REST API 认证文档：`https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authenticating-to-the-rest-api-with-an-oauth-app`
7. Casbin RBAC 文档：`https://www.casbin.org/docs/rbac`

---

## 25. 最终结论

这一版的项目边界非常明确：

```text
单人应用
无 tenant
无 workspace
user 即 owner
第一检索后端 Elasticsearch
第一检索模式 BM25 keyword
后续预留 ES vector / Milvus / Qdrant
核心能力聚焦 Agent Flow + RAG + Runtime + Memory + Tool + Guardrail
```

你现在最应该做的不是继续扩需求，而是从 Phase 0 开始落地。

第一阶段不要追求完美，目标只有一个：

> 让系统跑起来，让数据流起来，让第一个 RAG 问答闭环出现。

然后再一步步把它升级成真正能展示、能讲清楚、能体现工程能力的 AI 项目。
