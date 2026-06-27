# Agent Runtime MVP 使用说明

## 已落地能力

本次 MVP 保留现有 Workflow DAG 执行器，在其上新增 `agent_loop` 节点。该节点内部运行 ReAct / Tool Calling 循环：

```text
用户输入 -> agent_loop -> LLM 选择工具 -> 执行工具 -> 观察结果回填 -> LLM 继续推理 -> 最终答案
```

已支持：

- OpenAI-compatible Chat Completions `tool_calls`。
- `internal/runtime/agent` ReAct Runner。
- `internal/runtime/toolruntime` 统一工具接口。
- 已有 HTTP Tool 作为 Agent 可调用工具。
- 配置 `knowledge_ids` 后自动暴露 `search_knowledge` 工具。
- 配置 `call_agent_ids` 后自动暴露 `call_agent` 工具。
- `agent_call` 节点可在确定性 Workflow 中调用另一个 Agent。
- `agent_started / agent_step / agent_finished / agent_failed` 运行事件。
- Canvas 中新增 `Agent Loop` 节点配置。

## Canvas 配置方式

1. 进入 Agent Canvas。
2. 添加 `Agent Loop` 节点。
3. 配置 `Provider` 和可选 `model`。
4. 填写 `System Prompt`，例如：

```text
你是一个严谨的研究型 Agent。必要时调用工具，不要编造事实。看到工具结果后再继续推理并给出最终答案。
```

5. 填写任务模板，常用值：

```text
{{sys.query}}
```

6. 在“可用工具”中选择已经创建的 HTTP Tool。
7. 在“知识库工具”中选择知识库后，Agent 会看到内置 `search_knowledge` 工具。
8. 在“可调用 Agent”中选择子 Agent 后，Agent 会看到内置 `call_agent` 工具。
9. 根据需要设置：
   - `max_iterations`：默认 8。
   - `max_tool_calls`：默认 16。
   - `max_execution_time_ms`：默认 120000。
   - `output_mode`：`final_answer` 或 `full`。

## DSL 示例

最小可运行 DSL：

```json
{
  "schema_version": "v1",
  "flow_id": "agent-1",
  "nodes": [
    {
      "id": "begin",
      "type": "begin",
      "name": "Begin",
      "config": {
        "input_schema": {
          "query": "string"
        }
      }
    },
    {
      "id": "agent_loop",
      "type": "agent_loop",
      "name": "Agent Loop",
      "config": {
        "provider_id": 1,
        "model": "",
        "system_prompt": "你是一个严谨的 Agent。必要时调用可用工具，看到工具结果后再继续推理并给出最终答案。",
        "task_template": "{{sys.query}}",
        "tool_ids": [1],
        "knowledge_ids": [10],
        "knowledge_top_k": 5,
        "knowledge_mode": "keyword",
        "call_agent_ids": [2, 3],
        "max_agent_call_depth": 3,
        "max_iterations": 8,
        "max_tool_calls": 16,
        "max_execution_time_ms": 120000,
        "temperature": 0.2,
        "return_intermediate_steps": true,
        "output_mode": "final_answer"
      }
    },
    {
      "id": "message",
      "type": "message",
      "name": "Message",
      "config": {
        "content": "{{agent_loop.content}}"
      }
    }
  ],
  "edges": [
    {
      "from": "begin",
      "to": "agent_loop"
    },
    {
      "from": "agent_loop",
      "to": "message"
    }
  ]
}
```

## 多 Agent 调用

确定性调用可以直接使用 `agent_call` 节点：

```json
{
  "id": "call_writer",
  "type": "agent_call",
  "name": "Call Writer",
  "config": {
    "agent_id": 2,
    "flow_version_id": 0,
    "input": {
      "query": "{{sys.query}}"
    },
    "max_depth": 3
  }
}
```

自治调用可以在 `agent_loop` 里配置：

```json
{
  "call_agent_ids": [2],
  "max_agent_call_depth": 3
}
```

这样模型会看到 `call_agent` 工具，可把子任务交给允许列表里的 worker Agent。子运行会写入：

- `parent_run_id`
- `caller_node_id`
- `call_depth`

这些字段用于追踪 Supervisor-Worker 运行关系，并防止无限递归。

## 调试观察

流式调试时，事件面板会看到：

- `agent_started`：Agent 节点启动。
- `agent_step`：每一轮 LLM 响应、工具调用、工具结果、最终答案。
- `tool_invocations`：HTTP Runtime Tool 的持久化调用记录，可在调试结果下方查看。
- `agent_finished`：Agent 停止原因、轮次、工具调用数和 token 统计。

`agent_loop` 输出字段：

```json
{
  "content": "最终答案",
  "final_answer": "最终答案",
  "stop_reason": "final_answer",
  "iterations": 2,
  "tool_calls": 1,
  "total_tokens": 1234,
  "latency_ms": 1500
}
```

当开启 `return_intermediate_steps` 或 `output_mode = full` 时，输出会额外包含 `steps`。

## 当前边界

本 MVP 先完成真正 Agent 的核心闭环，没有推翻现有 Workflow：

- 暂不支持任意带环 Canvas。
- 暂不支持沙盒代码执行。
- 已支持 `agent_call` 节点和 `call_agent` 工具的初版 Supervisor-Worker 调用。
- 暂未持久化独立 `agent_run_steps` 表，当前通过 `agent_run_events` 保存 step 事件。
- 当前 Runtime Tool 已接入已有 HTTP Tool、知识库检索和子 Agent 调用；Memory / Sandbox 可按同一接口继续扩展。

## 验收建议

1. 创建一个 HTTP Tool，并确保它有清晰的 `input_schema_json`。
2. 创建包含 `begin -> agent_loop -> message` 的 Agent。
3. 发布后运行：

```json
{
  "query": "请调用可用工具查询信息，然后总结结果"
}
```

4. 确认事件流中存在 `agent_step`，且可以看到：
   - LLM 返回 `tool_calls`。
   - Tool Runtime 执行工具。
   - 工具结果作为 observation 注入下一轮 LLM。
   - 最终输出 `final_answer`。
