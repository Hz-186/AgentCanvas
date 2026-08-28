# Exploration Handoff — AgentCanvas 日志与链路追踪

## 决策清单 (Decisions)

- **D1 [Change 划分]**：本主题独立为 `observability-correlation-tracing` change；对话缓存另建 `conversation-cache` change，两个 change 通过 correlation context 连接但分别设计、实现和验证。  
  **来源（A）**：用户原话“2”。

- **D2 [范围]**：只研究并改进 Hermes 对应的日志/链路追踪能力；不复制 Hermes 的其他功能，也不在 Explore 阶段修改业务代码、测试、配置或构建文件。  
  **来源（A）**：用户原话“总之就是学习一下 hermes agent 这两部分的操作（别的部分不要进行）”以及“按照 vsdd 的流程，写成一个 explore，开始！”。

- **D3 [现有持久化边界]**：`RunEvent`、`RunStep` 和现有 trace 聚合 API 继续作为业务事实、审计和回放来源；新增日志只做诊断索引，不新增第二套 durable trace 表。  
  **来源（B）**：`internal/application/agent_usecase/run_publisher.go:64-84`（RunEvent 先落库再发布）、`internal/application/agent_usecase/subagent.go:237-257`（RunStep 持久化）、`internal/interface/http/handler/agent_handler.go:673-702`（trace 聚合 API）。

- **D4 [日志实现路线]**：采用“现有 `slog` + correlation context + HTTP access/error 事件”的增量路线；OTel/Langfuse 只保留为后续可选 adapter，不直接移植 Hermes 的 rotating file/QueueHandler 体系。  
  **来源（B）**：`internal/pkg/logger/logger.go:8-18` 已提供 local text 与非 local JSON stdout；Hermes `hermes_logging.py:259-379,551-764` 使用文件轮转和异步队列；Hermes `plugins/observability/langfuse/__init__.py:883-952,1266-1723` 将 Langfuse 作为可选观察层。

- **D5 [Correlation 字段]**：统一上下文至少包含 `request_id`、`owner_id`、`conversation_id`、`run_id`、`turn_id`、`parent_run_id`、`step_index`、`tool_call_id`，并贯穿 HTTP、StartTurn、worker、runtime、LLM/tool、compaction 和 cache 诊断事件。  
  **来源（B）**：`internal/interface/http/handler/agent_handler.go:369-392`、`internal/application/agent_usecase/service.go:726-844`、`internal/application/agent_usecase/turn_worker.go:30-60,125-143`、`internal/application/agent_usecase/subagent.go:239-257`；`internal/interface/http/middleware/request_id.go:11-22` 当前只写 Gin context 和响应头。

- **D6 [调用链]**：目标链路为“HTTP request → StartTurn → queued Run/Turn → worker claim → runtime → LLM/tool/compaction → RunEvent/RunStep + slog”；RunEvent/RunStep 负责可回放事实，slog 负责运行诊断。  
  **来源（B）**：`internal/application/agent_usecase/turn_worker.go:30-154`、`internal/application/agent_usecase/run_publisher.go:64-145`；Hermes `agent/turn_context.py:502-503`、`agent/conversation_loop.py:3023-3075,6781-6809` 展示 turn/API 上下文传播。

- **D7 [隐私与失败策略]**：默认只记录 ID、状态、耗时、token/usage 摘要和错误类型，不记录 prompt、API key、完整 tool 参数/返回值；观察层写入失败必须 fail-open，不阻断 turn。  
  **来源（B）**：Hermes `hermes_logging.py:150-211` 的 session tag/RedactingFormatter，Hermes Langfuse README 的 metadata/sanitized/full 模式；AgentCanvas `internal/interface/http/middleware/recovery.go:13-23` 当前仅记录 panic 文本。

- **D8 [实施顺序]**：先在本 change 中建立 correlation context、HTTP access/error 日志和 LLM/tool/compaction 诊断字段，再由 `conversation-cache` change 复用这些字段记录命中、失效、延迟和错误。  
  **来源（A）**：用户选择拆成两个独立 change；**来源（B）**：相关边界位于 `internal/interface/http/middleware`、`internal/pkg/logger`、`internal/application/agent_usecase` 和 `internal/runtime`。

## 客观阻塞 (Real Blockers)

- 无。Hermes 源码、AgentCanvas 代码、code graph 结果和 OpenSpec 状态均已可读取；本阶段不依赖外部服务或额外 SDK 才能完成方案交接。

## 下一步建议 (Next Step)

- 进入 `propose`，为 `observability-correlation-tracing` 创建正式 proposal、design、spec 和 tasks；propose 阶段继续保持 RunEvent/RunStep 的持久化职责与 slog 的诊断职责分离。
