## Why

AgentCanvas 已有 `RunEvent`、`RunStep` 和 trace 查询接口作为业务审计事实，但普通运行日志仍缺少贯穿 HTTP 请求、异步 turn worker、模型调用、工具执行和压缩阶段的统一关联标识。出现超时、失败或性能回退时，运维人员无法仅凭结构化日志稳定地从入口定位到具体 run/turn/step，同时现有 recovery 日志也缺少请求与租户上下文。

## What Changes

- 建立统一 correlation context，规范传播 `request_id`、`owner_id`、`conversation_id`、`run_id`、`turn_id`、`parent_run_id`、`step_index` 和 `tool_call_id`。
- 让 HTTP request ID 和认证后的 owner ID 进入标准 `context.Context`，并增加 metadata-only access/recovery 日志。
- 将入口 request ID 持久化到 turn/run 的输入 metadata，worker claim 后依据持久化身份重新建立执行上下文。
- 为 turn、LLM、tool 和 compaction 生命周期增加统一结构化诊断事件，包括状态、耗时、模型/工具名称和 usage 摘要。
- 保持 `RunEvent`、`RunStep` 为唯一 durable trace 与回放事实；诊断日志不得复制 prompt、API key、完整工具参数或工具输出。
- 预留观察适配边界，但本 change 不引入 OpenTelemetry、Langfuse 或新的持久化 trace backend。

## Capabilities

### New Capabilities

- `correlated-agent-observability`: 定义 AgentCanvas 从 HTTP 请求到异步 agent runtime 的关联上下文、结构化诊断事件、隐私约束和持久化边界。

### Modified Capabilities

- 无。

## Impact

- HTTP：`internal/interface/http/middleware` 的 request ID、auth、access 和 recovery 行为。
- 应用层：`internal/application/agent_usecase` 的 StartTurn、worker claim、turn 生命周期与现有 RunEvent/RunStep 协作方式。
- Runtime：`internal/runtime/agent` 与 conversation compaction 的 metadata-only 生命周期日志。
- 基础设施：`internal/pkg/logger` 的 context enrichment，以及 LLM provider/model/usage 诊断字段。
- 数据兼容：复用现有 Run/Turn `InputJSON` metadata，不新增 durable trace 表；旧记录缺少 request ID 时仍可执行。
- 依赖：不新增外部可观测性 SDK，不改变公开 HTTP response schema；`X-Request-ID` 响应头继续兼容。
