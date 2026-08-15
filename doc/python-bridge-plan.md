# AgentCanvas 多语言 Python Bridge 规划

## 总结

当前仓库已经安全同步到 `main`，HEAD 与 `origin/main` 均为 `7499b1e9`。执行前原 `feat/git-workspaces` 工作区的 19 个修改文件和 2 个未跟踪文件已保存到 `stash@{0}`，未被丢弃。

当前 `origin/main` 没有 Python 或 Protobuf 实现，但 Go 已提供适合扩展的边界：

- `internal/runtime/agentruntime.AgentRuntime`：唯一 Agent 执行入口。
- `internal/runtime/toolruntime.RuntimeTool`：工具执行协议。
- `internal/infrastructure/chunker.Registry`：文档切片注册表。
- 当前链路为 `Parser → Go Chunker → MySQL Chunks → Embedding → Elasticsearch`。

已确定方向：Go 保留主控、持久化、安全、审批和 Agent Loop；Python 作为独立常驻侧车；两者通过 gRPC + Protobuf v1 通信；首期实现 Python 工具与文档切片，不迁移 Go Agent Loop；LangChain/LangGraph 只按基准收益准入。

## 方案比较

| 方案 | 优点 | 代价 | 结论 |
| --- | --- | --- | --- |
| Go 主控 + Python 侧车 | 保留 Go 的持久化、安全、审批和恢复能力；可快速使用 Python 生态 | 需要维护 gRPC 协议和一个服务 | 采用 |
| Python 主循环 + Go Gateway | Python/LangGraph 迭代快 | 需要迁移状态、审批、Memory、Reflection 和 Checkpoint | 暂不采用 |
| Go/Python 双运行时可选 | 长期灵活 | 两套循环、状态和测试成本高 | 后续再评估 |

Hermes 的 RPC 工具调用、令牌校验、工具白名单、调用上限和超时作为安全参考；OpenClaw 的握手、能力声明、作用域、版本协商、事件和等待语义作为协议参考，不复制完整 Gateway。

## 实施阶段

### 1. gRPC/Protobuf v1

定义版本化 `PythonBridge` 服务，包含 `Health`、`GetCapabilities`、`ChunkDocument`、`ListTools` 和 `ExecuteTool`。协议只允许向后兼容的字段追加。

`ChunkDocument` 传输现有 `ParsedDocument` 的文本、Block、页码和元数据，返回 Chunk 的索引、内容、token 数、字符数、章节、页码、元数据和算法版本。`ExecuteTool` 只传工具名、JSON 参数和清洗后的运行上下文。

统一使用 gRPC metadata 传递请求 ID、trace ID 和进程级随机认证令牌；启用 deadline、取消、最大消息大小、并发限制和结构化错误码。侧车只绑定回环地址或受限容器网络，首期使用共享令牌，跨主机部署时再增加 mTLS。

错误处理约定：`UNAVAILABLE` 和 `DEADLINE_EXCEEDED` 由 Go `pythonbridge.IsRetryable` 标记为可重试，但客户端不自动重试，避免未来带副作用工具被重复执行；`INVALID_ARGUMENT`、`UNAUTHENTICATED`、`RESOURCE_EXHAUSTED` 和 `CANCELED` 必须由调用方修正请求或结束任务。

### 2. Go 适配层

- `PythonChunker` 实现现有 `chunker.Chunker` 接口。
- Python 切片方法通过 `KnowledgeBase.ChunkMethod` 选择，例如 `python:recursive`。
- Go Worker 负责解析、调用 Python、校验结果、持久化 Chunk、生成 Embedding 和写入 Elasticsearch。
- Python 返回非法索引、超出预算或字段缺失时，任务明确失败，不静默切换算法。
- `PythonRuntimeTool` 实现 `toolruntime.RuntimeTool`，Go 继续执行白名单、风险、审批、超时、输出截断、审计和事件持久化。
- Agent Runtime 配置增加 `python_tool_names`，不增加数据库表。
- 首期 Python 工具只允许纯计算、文本处理和结构化转换；文件、Shell、Git、网络和数据库操作继续使用 Go 工具和沙箱。
- 增加 `python_bridge` 配置段，默认关闭；关闭时现有 Go-only 路径不变。
- `shadow_enabled` 默认关闭；开启后 Worker 保留 Go 主结果并记录 Python 对比指标。`allow_experimental_chunking` 默认关闭，只有固定基准达到门槛并完成审阅后才允许知识库选择 `python:*`。

### 3. Python 常驻侧车

由 `make dev` 或 Docker Compose 启动独立 Python 服务，API 与 Worker 连接同一个 Bridge。首期提供 gRPC Server、健康检查、能力发现、Python Chunker 和低风险 Python Tool Registry。Python 不持有 AgentCanvas 数据库连接和用户密钥。

必需依赖仅包括 `grpcio`、`protobuf` 和经过基准验证的 tokenizer/解析库。先用 Python 标准库实现基线；逐个评估 `tiktoken`、`langchain-text-splitters`、PyMuPDF、unstructured 等。LangChain 不作为统一抽象层，LangGraph 不进入首期主循环。

### 4. 切片与检索评估

固定测试集覆盖中英文 Markdown、中文长文本、FAQ、标题/列表/表格/页码、极长段落、空文档、乱码和超限输入。比较 Go 与 Python 的边界质量、token 预算、overlap、元数据保留、确定性、p50/p95 延迟、内存、Recall@K、Precision@K 以及失败/取消行为。

先提供 shadow/对比模式，只有 Python 达到质量和性能门槛后，才允许知识库选择 `python:*` 方法。固定跨语言 fixture 位于 `internal/infrastructure/pythonbridge/testdata/fixtures.json`，真实侧车基准通过 `TestLivePythonBridgeBenchmark` 输出边界匹配、p50/p95、客户端分配量和 Recall@K/Precision@K；设置 Elasticsearch 地址时使用真实 keyword index，否则退回确定性的本地检索代理。侧车进程 RSS 由 Docker 运行环境单独观测，不冒充 Go 客户端分配量。

### 5. Python Agent Loop 的后续边界

首期不迁移 Agent Loop。未来若 LangGraph 或 Hermes 风格 Loop 证明有收益，必须复用同一套 `RunRequest`、`RunResult`、Event、Approval、Checkpoint 契约；Go 继续拥有运行持久化、权限、安全和恢复状态。Python Loop 只能作为可选 Workflow Runtime 或远程子 Agent。

## 测试与验收

- 协议：兼容性、握手、能力版本、认证失败、deadline、取消、重试、重启、消息大小、错误码和非法响应。
- Go：Chunk 转换、元数据、token 预算、工具 Schema、审批、审计、事件、Bridge 不可用和默认关闭回归。
- Python：切片边界、元数据、中文/英文 tokenizer、工具白名单、输入输出限制、并发、超时、取消和异常映射。
- 集成：Bridge 关闭时 Go-only 无回归；Bridge 正常时完成入库、Embedding 和索引；杀掉侧车时返回可诊断的 `UNAVAILABLE`；侧车重启后 Run、Checkpoint 和数据库状态仍可恢复。
- 验证至少连续执行协议单测、Go/Python 集成测试、Docker Compose 端到端测试三轮。

## 回滚与交付

- 默认功能开关保持关闭。
- Python 切片只对显式选择 `python:*` 的知识库生效。
- 回滚时关闭 Bridge，将实验知识库切回 `recursive` 或 `fixed_token` 后重新索引。
- 不做数据库迁移，不复制 Go Agent Loop。
- 本文件只记录计划，不包含实现代码。
