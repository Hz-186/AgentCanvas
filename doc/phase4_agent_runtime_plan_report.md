# Phase 4 Agent Flow DSL 与 Runtime 计划报告

## 目标

Phase 4 的目标是把 AgentCanvas 从普通 RAG Chat 推进到可用 JSON DSL 运行 Agent Flow 的后端闭环。该阶段不包含前端画布，验收重点是用户可以通过 API 创建 Agent、保存 DSL、发布 Flow Version、执行 Flow，并获得 run events 与 node logs。

## 范围

本阶段交付以下能力：

1. Agent 基础 CRUD。
2. Flow Version 保存、校验、发布。
3. Flow DSL v1 解析与 DAG 校验。
4. Runtime Executor 按 DAG 拓扑顺序执行节点。
5. Begin、Knowledge Retrieval、Prompt、LLM、Message 五类节点。
6. Agent Run 持久化。
7. workflow、node、retrieval、llm、message 事件记录。
8. node logs 查询。
9. 普通运行接口与 SSE 流式运行接口。

## 非目标

本阶段不实现以下内容：

1. 前端可视化画布。
2. Memory、HTTP Tool、Switch、Guardrail。
3. 向量检索、Hybrid Search、Rerank。
4. 多租户协作权限模型。

## 实施分解

1. 数据库层：新增 agents、agent_flow_versions、agent_runs、agent_run_events、agent_node_logs 五张表。
2. 领域层：补齐 Agent、FlowVersion、Run、RunEvent、NodeLog 实体和仓储接口。
3. DSL 层：定义 schema_version、flow_id、nodes、edges，并校验节点唯一性、边引用、单 begin 节点和 DAG 无环。
4. Runtime 层：实现 RunContext、EventEmitter、Executor 和变量解析，支持 `{{sys.query}}`、`{{node_id.field}}` 模板引用。
5. 节点层：实现 begin、knowledge_retrieval、prompt、llm、message 节点，并复用已有 ES Retriever 与 OpenAI-compatible LLM Client。
6. 应用层：提供 Agent/Flow/Run 用例，负责加载发布版本、创建 run、执行 runtime、落库事件与节点日志。
7. 接口层：暴露 Phase 4 文档中的 Agent、Flow Version、Run、Event、Node Log API。
8. 验证层：使用小范围单元测试覆盖 DSL 校验、变量解析和 Executor 执行链路。

## 验收标准

1. API 能创建 Agent 并保存合法 JSON DSL。
2. 非法 DSL 会在保存或 validate 阶段被拒绝。
3. Flow Version 可以发布为当前版本。
4. Agent Run 可以执行完整 Begin -> Retrieval -> Prompt -> LLM -> Message 链路。
5. 执行过程会持久化 run events。
6. 执行结束后可以查询 run 和 node logs。
7. 小范围单元测试通过。
