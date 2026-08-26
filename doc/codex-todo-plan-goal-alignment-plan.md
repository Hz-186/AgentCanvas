# AgentCanvas 对齐 Codex Todo、Plan Mode 与 Goal 的修正计划

## 1. 结论与基线

本计划只定义后续修正工作，不实施代码变更。

- Codex 源码基线：`/Users/zhanghongze/GolandProjects/codex`，`HEAD=46aa019e805352c9d7fd9a740cbf7f8b9aeb162d`，已确认与 2026-08-25 的 GitHub `openai/codex` `main` 一致。
- AgentCanvas 对比基线：`/Users/zhanghongze/GolandProjects/AgentCanvas`，`HEAD=5184b28b574f6d600baf5be29172edc46343f4a0`，同时以当前未提交工作区内容为准。
- “完全看齐”的定义：对齐 Codex 的对外语义、状态机、模型可见工具、Prompt、事件、恢复/分叉、自动续跑、计费和 UI 交互；底层继续使用 AgentCanvas 现有 Go、MySQL、HTTP/SSE 和 Web 技术栈，不为形式一致另建 SQLite 或 Rust 式扩展框架。
- 最关键的概念修正：Codex 没有 “Goal collaboration mode”。Codex 的协作模式只有 `Default` 和 `Plan`；Goal 是附着在持久化线程上的独立长任务状态。AgentCanvas 当前把 `goal` 当作默认执行模式，必须拆开。

## 2. Codex 源码事实

### 2.1 Todo / `update_plan`

源码锚点：

- `codex-rs/protocol/src/plan_tool.rs:6-28`
- `codex-rs/core/src/tools/handlers/plan_spec.rs:7-57`
- `codex-rs/core/src/tools/handlers/plan.rs:62-108`
- `codex-rs/app-server/src/bespoke_event_handling.rs:1231-1285`
- `codex-rs/tui/src/history_cell/plans.rs:169-229`

确定行为：

1. Todo 是名为 `update_plan` 的模型工具，不是 Plan Mode，也不是自动执行调度器。
2. 参数固定为：

   ```json
   {
     "explanation": "optional string",
     "plan": [
       { "step": "task text", "status": "pending|in_progress|completed" }
     ]
   }
   ```

3. 对象拒绝未知字段；`plan` 必填；状态只有 `pending`、`in_progress`、`completed`，没有 `failed`、编号、工具名、错误、版本和百分比。
4. “最多一个 `in_progress`”是工具说明和模型指令，不是 handler 的额外语义校验；对齐实现不得自行增加更严格的服务端规则。
5. 工具默认开启，可由 `tools.update_plan.enabled` 关闭。
6. Plan Mode 调用时返回精确错误：`update_plan is a TODO/checklist tool and is not allowed in Plan mode`。
7. 成功后发出计划更新事件，模型只收到 `Plan updated`；客户端将事件显示为 `Updated Plan` 检查清单。

### 2.2 Plan Mode

源码锚点：

- `codex-rs/protocol/src/config_types.rs:668-710`
- `codex-rs/models-manager/src/collaboration_mode_presets.rs:16-33`
- `codex-rs/collaboration-mode-templates/templates/plan.md:1-128`
- `codex-rs/core/src/tools/handlers/request_user_input_spec.rs:9-126`
- `codex-rs/core/src/tools/handlers/request_user_input.rs:38-90`
- `codex-rs/core/src/tools/spec_plan.rs:970-1145`
- `codex-rs/tools/src/tool_config.rs:17-31`
- `codex-rs/utils/stream-parser/src/proposed_plan.rs:7-111`
- `codex-rs/core/src/session/turn.rs:1597-2011`
- `codex-rs/tui/src/chatwidget/turn_runtime.rs:226-266`
- `codex-rs/tui/src/chatwidget/plan_implementation.rs:9-113`

确定行为：

1. `ModeKind` 只有 `Default` 与 `Plan`；协作模式设置同时容纳 `model`、`reasoning_effort`、`developer_instructions`。
2. 内置 Plan preset 默认使用 `Medium` reasoning effort，并注入完整 Plan developer instructions。
3. Plan Mode developer prompt 只允许为形成计划而进行的非变更探索。可以读文件、静态检查、执行不会修改仓库跟踪状态的测试/构建；不能编辑、应用迁移、运行会重写文件的 formatter，或执行计划本身。Codex 没有额外安装通用的 Plan 只读 permission profile：shell、`apply_patch`、MCP 和协作工具仍按普通配置注册；运行时只对 `update_plan`、`request_user_input` 等特定工具做模式检查。
4. 工作流是“先探索，再询问意图和实现取舍，最后产出 decision-complete 计划”。
5. `request_user_input` 默认只在 Plan Mode、根线程可用，且 Plan 请求是阻塞的；实验性 `DefaultModeRequestUserInput` feature 开启后，根 Default 也可调用，但请求为非阻塞。请求包含 1–3 个问题，每题提供选项，客户端自动增加自由输入的 Other；handler 实际只强制每题选项非空，其余数量、标签长度和推荐项规则主要由 schema 描述约束。
6. 正式计划必须使用独占行的 `<proposed_plan>` 与 `</proposed_plan>` 包裹；解析器把它流式拆为独立 `TurnItem::Plan`，并从普通 assistant message 中去掉该块。“每 turn 最多一个”是 prompt 规则，不是 parser 拒绝规则；若模型输出多个完整块，实时流复用同一个 Plan item，第二块不会撤回或重置此前已发送的 delta；最终提取阶段则在每个新块开始时重置提取缓冲，因此完成后的 Plan item 使用最后一个完整块。
7. 只有真正产生 `Plan` item 的 Plan turn 才出现三选一实现提示：切到 Default 在当前会话实现、清空 UI 后启动只携带实现提示与完整计划的全新会话、留在 Plan。第二项不是 fork，不复制旧历史或 Goal；Todo 更新不能触发该提示。
8. 自动空闲工作不能启动 Plan turn；Plan turn 不计入 Goal 使用量，也不触发 Goal 自动续跑。

### 2.3 Goal

源码锚点：

- `codex-rs/features/src/lib.rs:1451-1455`
- `codex-rs/ext/goal/src/spec.rs:9-93`
- `codex-rs/ext/goal/src/tool.rs:185-274,407-445`
- `codex-rs/ext/goal/src/runtime.rs:83-580`
- `codex-rs/ext/goal/src/accounting.rs:313-337`
- `codex-rs/ext/goal/src/extension.rs:133-163,192-355`
- `codex-rs/ext/goal/src/api.rs:143-179`
- `codex-rs/protocol/src/protocol.rs:3833-3844`
- `codex-rs/state/src/model/thread_goal.rs:12-80`
- `codex-rs/state/src/runtime/goals.rs:20-417,499-630`
- `codex-rs/state/goals_migrations/0001_thread_goals.sql:1-18`
- `codex-rs/state/goals_migrations/0002_thread_goal_continuation_deferrals.sql:1-3`
- `codex-rs/app-server/src/request_processors/thread_goal_processor.rs`
- `codex-rs/app-server/src/request_processors/thread_processor.rs:4953-4998`
- `codex-rs/app-server-protocol/src/protocol/v2/thread.rs:595-626`
- `codex-rs/tui/src/app/thread_goal_actions.rs:23-78`
- `codex-rs/tui/src/chatwidget/goal_menu.rs:39-111`
- `codex-rs/tui/src/chatwidget/slash_dispatch.rs:825-930`

确定行为：

1. Goals 是默认开启的 Stable feature，只支持有持久化线程状态的线程；Goal 独立于 `Default/Plan` 模式。Goal 模型工具对没有持久化状态的 ephemeral thread 和 Review subagent 隐藏，其他持久化 subagent 不被一概排除。
2. 模型工具有 `get_goal`、`create_goal`、`update_goal`：
   - `create_goal` 只能在用户、system 或 developer 明确要求时使用；普通任务不能被推断成 Goal。
   - 模型仅在用户明确要求预算时传 `token_budget`；若服务端配置 `max_goal_token_budget`，该值同时作为默认预算和上限。
   - 未完成 Goal 存在时模型创建失败；已完成 Goal 可被新 Goal 替换。
   - `update_goal` 只允许模型设为 `complete` 或 `blocked`。
   - “相同阻塞连续三轮才可 blocked”由模型工具说明约束，Codex 没有持久化阻塞计数器；对齐实现不新增一套不同语义。
3. 用户/API 可控制完整状态：`active`、`paused`、`blocked`、`usage_limited`、`budget_limited`、`complete`。
4. Goal 字段为 `thread_id`、内部 `goal_id`、`objective`、`status`、`token_budget`、`tokens_used`、`time_used_seconds`、`created_at`、`updated_at`；objective 先 trim，再按 Unicode 字符数校验非空且最多 4000 字符，并持久化 trim 后的值；预算必须为正且不能超过配置上限。
5. Codex 分别使用 Goal 状态锁和 progress-accounting 锁协调线程内竞态；usage accounting 与外部 read-modify-write 使用 `expected_goal_id` 防止旧 Goal 污染新 Goal，但模型 `update_goal` 的最终状态更新显式不带该条件。对齐时不能把“所有更新都必须携带 goal ID”写成 Codex 契约。
6. active Goal 在线程空闲后自动启动下一 turn并注入 continuation；运行中修改 objective 会向当前 turn 注入 steering。
7. 计费公式是 `input_tokens - cached_input_tokens + output_tokens`；`reasoning_output_tokens` 不单独加计，也不从 provider 已报告的 `output_tokens` 中扣除。Plan turn 完全不计 Goal token/time。
8. 到达预算会原子累计使用量并转为 `budget_limited`；usage limit 转为 `usage_limited`；不可恢复 turn error 转为 `blocked`，从而停止自动死循环。
9. resume 恢复 Goal runtime。只有 app-server fork 显式传实验参数 `deferGoalContinuation=true` 时，才先结算源线程用量、复制 Goal 快照并设置 continuation deferral；普通 fork 不继承 Goal。deferral 在新线程下一次 turn 开始时清除。
10. `/goal [<objective>|clear|edit|pause|resume]` 管理 Goal；替换未完成 Goal 前弹确认；裸 `/goal` 直接展示状态摘要和可用子命令提示，不打开操作选择菜单；状态栏展示状态、耗时和 token 预算。
11. Goal 表更新是权威持久化；app-server 随后 best-effort 追加 rollout audit item，追加失败只告警，不阻止 response、通知和 runtime effects。
12. 恢复会话时，`paused`、`blocked`、`usage_limited` Goal 会出现专用的“Resume paused goal?”二选一提示；`budget_limited` 与 `complete` 不提示。

## 3. AgentCanvas 原始基线与差距

本节中的“当前实现”特指 AgentCanvas 基线提交
`5184b28b574f6d600baf5be29172edc46343f4a0`，不是本次审查时的未提交工作区。
工作区已经包含一部分对齐尝试，不能因为测试通过就视为已达到 Codex 对齐；最终是否完成以第 9 节的阻塞门槛为准。

| 领域 | AgentCanvas 当前实现 | 与 Codex 的实质差距 |
| --- | --- | --- |
| 模式模型 | `conversation.agent_mode` 与运行时只接受 `plan|goal`，并兼容 `react|plan_execute`；UI 把 `/goal` 当模式切换 | Codex 只有 `Default|Plan`；`/goal` 是独立 Goal 管理命令 |
| Todo 来源 | `execution.go:234-255` 每个新 Run 先额外调用一次 LLM 自动生成 `Plan`，失败时回退单步骤 | Codex Todo 只由模型显式调用 `update_plan`，不在每轮前自动规划 |
| Todo 数据 | `planner.go:15-64` 使用编号、`failed`、tool/error、version、revision、percentage | Codex 只有 `step/status` 和顶层可选 explanation |
| Todo 推进 | `runner.go:43-87,584-827` 按工具调用顺序自动 claim/完成/失败 Todo | Codex 不把 Todo 当工具执行调度器，也不根据工具结果自动改状态 |
| Todo 与 Plan | 同一个 `Plan` 同时充当 Todo、Prompt execution plan、checkpoint 和最终 output | Codex Todo update 与 Proposed Plan item 完全分离 |
| Plan Prompt | `context_assembler.go:373-383` 只有一句 system instruction | 缺少完整三阶段 Plan developer instructions、模式设置与注入快照 |
| Plan 工具注册 | `execution.go:47-52` 关闭 MCP、code execution、memory、reflection、subagent；`filterPlanTools` 仅留只读工具；Plan workspace 禁用 exec | Codex 不设通用 Plan 专用门禁，普通工具仍按相同配置/审批/沙箱注册，mutation 禁令由 developer prompt 约束；AgentCanvas 当前过度删工具，又没有 `request_user_input` |
| Plan 最终产物 | 普通 assistant message；`Plan.ExecutionState=ended_unverified` | 缺少 `<proposed_plan>` 流式 parser、独立 Plan item、专门持久化和实现选择弹窗 |
| 用户问答 | 只有工具审批 checkpoint 和 retrieval clarification | 缺少根线程专用、Plan 阻塞式的结构化 `request_user_input` |
| Goal | 所谓 Goal 只是“自主执行”Prompt 和普通 Run 的别名 | 完整 Goal 数据、工具、状态机、预算、计费、自动 continuation、steering、API、UI 均不存在 |
| 持久化 | Run/step/checkpoint/event 已持久化；Todo 依赖 run event replay | 可复用基础设施，但没有 thread goal 表、goal snapshot/event、continuation deferral |
| Usage | 基线 `llm.Usage` 只有 prompt/completion/total（工作区后来已增加 cached/reasoning 字段） | 仍需把 provider 的增量 usage 事件按 Codex 公式即时记账，并在 turn 结束时只结算 residual，避免重复或漏记 |
| Fork/Resume | conversation fork 复制消息和 mode；Run resume 恢复 Plan checkpoint | 没有 Goal runtime 恢复、停滞状态恢复提示，以及显式 `deferGoalContinuation` fork 路径；普通 fork 不应默认复制 Goal |

## 4. 目标架构边界

只新增 Codex 语义真正需要的边界，并复用现有 AgentCanvas 设施：

```text
Conversation（Codex Thread）
├── Collaboration mode: Default | Plan
│   ├── mode preset / developer instructions
│   └── Proposed Plan item（仅 Plan turn）
├── Turn / Run
│   ├── update_plan Todo snapshots（进度展示，不调度工具）
│   ├── structured user-input interaction
│   └── existing tool/approval/checkpoint/event infrastructure
└── Thread Goal（可选、独立）
    ├── state + usage + budget
    ├── model tools / user API
    └── idle continuation + steering
```

约束：

- 不创建通用 workflow engine、第二套 Run 引擎或第二个数据库。
- Goal 存 AgentCanvas 的 MySQL；Todo 与 Proposed Plan 继续复用 `agent_run_events`、`agent_run_steps`、checkpoint 和 SSE。
- Goal 串行化优先使用数据库条件更新/事务保证多实例正确性；进程内 per-conversation lock 只负责缩小竞态窗口，不能代替数据库约束。
- 所有兼容别名仅留在 API/迁移边界，运行时内部只看 canonical 值。

## 5. 分阶段修正计划

### 阶段 0：先锁定 Codex 契约

1. 为三套能力建立 golden/contract tests，测试数据直接对应上述 Codex 基线，不先改生产逻辑。
2. 固定以下契约：Todo schema/error/result、`Default|Plan` 枚举、Plan prompt 与 tag parser、Goal schema/status/tool rules/accounting 公式。
3. 增加一份测试常量记录 Codex baseline commit；未来升级基线时显式更新，不在运行时依赖本机 Codex 路径。

完成门槛：新测试准确表达 Codex 行为，并在当前 AgentCanvas 上因真实差距失败，而不是因测试装配错误失败。

### 阶段 1：拆分模式、Todo、Goal 词义

1. 将运行时 canonical collaboration mode 改为 `default|plan`：
   - 数据迁移将 `goal`/`react` 改为 `default`，`plan_execute` 改为 `plan`，并把 `conversations.agent_mode` 默认值改为 `default`。
   - 读入旧快照时在唯一兼容函数中转换旧值；所有新写入只写 canonical 值。
   - 一个兼容发布周期后删除旧别名；最终枚举与 Codex 一致。
2. 将 `/goal` 从模式菜单移除；模式菜单只显示 Default 与 Plan，保留 `/plan` 切换，增加 `/default` 或等价 UI 操作回到 Default。
3. 将协作模式快照写入 Turn/Run input 和 checkpoint，确保活跃 turn 期间不可切换，resume 使用原模式，fork 继承模式。
4. 为模式 preset 增加 `model`、`reasoning_effort`、`developer_instructions` 的解析结果；先复用 Agent 定义中的 model，只有 provider 支持时才发送 reasoning effort，Plan 默认 Medium。

完成门槛：新会话默认是 `default`；普通任务不再被称为 Goal；旧数据可读；Plan/Default 切换、fork、resume 都使用稳定快照。

### 阶段 2：用真正的 `update_plan` 替换自动 Planner

1. 在 `toolruntime` 增加内建 `update_plan`：
   - schema 与 Codex 字段完全一致，拒绝未知字段；不加入 `failed` 等扩展。
   - 默认启用，增加 `tools.update_plan.enabled` 配置开关。
   - Default 成功时持久化完整快照事件并向模型返回 `Plan updated`。
   - Plan Mode 保持工具可识别，但 handler 返回 Codex 的精确错误，不发更新事件。
   - 不额外验证“最多一个 in_progress”，保持与 Codex handler 一致。
2. 事件投影改为 Codex 等价语义：包含 conversation/thread id、turn id、explanation 和完整 plan；SSE 可保留版本化 envelope，但 kind/字段由一个后端投影函数统一生成。
3. Web 将 Todo 渲染为 `Updated Plan` 检查清单，移除百分比、编号、失败态、tool/error、version/revision 展示；重连时从该 turn 最新快照恢复。
4. 删除以下耦合行为：
   - Run 前额外 LLM `GeneratePlanWithLessons`；
   - fallback 单步骤 Plan；
   - 工具调用自动 claim/complete/fail Todo；
   - reflection 自动 `RevisePlan`；
   - Todo 作为 `RunResult.Plan`、execution context 和 checkpoint 调度状态。
5. 如果其他功能仍需要“执行轨迹”，继续使用现有 `agent_run_steps`，不要再借用 Todo 状态。

完成门槛：没有 `update_plan` 调用就没有 Todo；一次调用只产生一次 durable update；工具成功/失败不会偷偷改 Todo；Plan Mode 调用得到精确错误；Todo 永远不会触发 Plan 实现弹窗。

### 阶段 3：补齐 Codex Plan Mode

1. 用 developer role 注入 Codex Plan preset 的完整规则，替换当前一句话 system prompt：
   - 三阶段流程；
   - explore-first；
   - mutation 禁令和允许的只读检查；
   - `request_user_input` 使用规则；
   - 最终 `<proposed_plan>` 格式与最多一个完整计划规则。
2. 撤销 AgentCanvas 现有的 Plan 专用工具裁剪和 exec 禁用：Plan 与 Default 使用相同的普通工具注册、审批和沙箱配置；一般 mutation 禁令由完整 developer prompt 约束。只保留 Codex 确有的特定模式规则，例如 `update_plan` 在 Plan 返回错误、`request_user_input` 根据模式决定可见性/阻塞性。若产品另需强制只读，可后续作为显式安全增强单独设计，但不得算作 Codex 完全对齐契约或默认验收门槛。
3. 在现有 interaction/checkpoint 基础上新增 `request_user_input`：
   - 默认仅根 conversation 的 Plan Mode 可用；subagent 调用返回根线程限制错误。
   - 增加默认关闭的 `default_mode_request_user_input` feature；开启后根 Default 可调用。
   - schema 与 Codex 一致；每题 options 必须非空，服务端把 `isOther=true`，Plan 请求标记 blocking。
   - Default 请求标记 non-blocking；Plan 请求保持 blocking。
   - 新 interaction kind 与普通 tool approval 分离，SSE 发 request，Web 渲染 1–3 个问题和自动 Other，提交答案后恢复同一 turn。
4. 增加增量 `<proposed_plan>` parser：
   - 仅识别独占行的精确英文 tag，支持 tag 被流 chunk 拆开、未闭合流结束、块前后普通文本。
   - 不因重复块报错；实时流复用同一个 Plan item，第二块不重置已发 delta；仅最终提取缓冲在每次新开块时清空，完成后的专用 Plan item 使用最后一个完整块。“最多一个”只由 Plan prompt 约束。
   - plan block 不进入普通 assistant message；流式发 plan start/delta/end，结束后落为独立 `proposed_plan` RunStep/Turn item。
   - 持久化原始 Markdown，使重连和页面刷新可以恢复专用计划卡片。
5. Plan turn 完成时，仅当本 turn 确有 Proposed Plan item 且没有排队 follow-up/弹窗时显示三项操作：
   - 切到 Default，在同一 conversation 发送 `Implement the plan.`；
   - 清空 UI 并创建 fresh conversation，只把固定实现提示与完整计划作为新会话首个实现意图发送；不走 fork，不复制旧消息或 Goal；
   - 保持 Plan，不启动实现。
6. 禁止后台 idle job 自动启动 Plan turn；Plan 完成、失败或问答暂停均不得被误判为可执行 Goal continuation。

完成门槛：Plan Mode 注入完整非变更规则，普通工具注册/审批/沙箱与 Default 一致，并可按 Codex 规则结构化提问；只有 `<proposed_plan>` 生成专用 item 和三选一提示；普通回答与 Todo 均不会触发提示。运行时强制 tracked-tree 只读不作为 Codex 对齐门槛。

### 阶段 4：建立持久化 Thread Goal 核心

1. 新增 MySQL 表（名称可沿用 AgentCanvas 习惯，但字段语义必须一致）：

   ```text
   thread_goals
   - owner_id
   - conversation_id                 PK/unique scope
   - goal_id                         opaque UUID，内部并发版本
   - objective                       trim 后 1..=4000 Unicode chars
   - status                          active|paused|blocked|usage_limited|budget_limited|complete
   - token_budget                    nullable positive bigint
   - tokens_used                     non-negative bigint, default 0
   - time_used_seconds               non-negative bigint, default 0
   - created_at / updated_at

   thread_goal_continuation_deferrals
   - owner_id + conversation_id      PK，Goal 删除时级联
   ```

2. 增加 domain model、repository 和 application service；usage accounting 与外部 read-modify-write SQL 接受 `expected_goal_id`，不匹配时返回 unchanged/conflict，严禁把旧 Goal 用量写到新 Goal；模型 `update_goal` 最终状态更新按 Codex 行为不强制该条件。
3. 实现完整状态规则：
   - objective 一律先 trim，再校验非空和 Unicode 字符数并持久化；
   - 模型 create 仅允许“无 Goal”或“现 Goal complete”；
   - 用户直接替换未完成 Goal 必须确认；
   - 模型 update 只接受 complete/blocked；
   - 用户/API 可设置 Codex 的所有状态；
   - budget 已超限时不能被 pause/block/reactivate 覆盖 `budget_limited`。
4. 增加 `get_goal`、`create_goal`、`update_goal` 内建工具，使用 Codex 的描述文字和结构化返回；只在有持久化线程状态且不是 Review subagent 时暴露，ephemeral thread 与 Review subagent 隐藏，不能增加一律 root-only 的门禁。
5. 增加 Goal feature flag，默认启用；增加 `max_goal_token_budget`，同时作为默认预算和 ceiling；请求预算只允许正整数。

完成门槛：Goal CRUD、模型权限边界、4000 字符和预算校验、替换规则、乐观并发和 terminal-state precedence 全部通过事务测试。

### 阶段 5：Goal accounting、自动续跑与错误停止

1. 扩展 provider-neutral `llm.Usage`，至少保留 `input/prompt`、`cached_input`、`output/completion`、`reasoning_output`；所有 provider adapter 统一映射，未知字段为 0。
2. Goal token delta 精确使用：

   ```text
   max(input_tokens - cached_input_tokens, 0) + max(output_tokens, 0)
   ```

   不使用现有 `total_tokens` 直接计费；`reasoning_output_tokens` 不单独加计，也不从 `output_tokens` 中扣除。
3. 在 turn start 记录 Goal/goal_id、token baseline 和 wall-clock baseline；在 usage event、tool 生命周期、turn complete/abort/error、外部 Goal mutation 前进行幂等增量结算。
4. Plan turn 在 start 时显式清除本 turn Goal accounting marker：不计 token/time，不续跑，但不删除或改变 Goal。
5. 按 Codex 的职责拆分并发边界：Goal 状态锁串行化外部 set/clear 与 idle continuation 的 read/start；独立 progress-accounting 锁保护 usage baseline/delta；数据库条件更新/事务处理多实例竞态。
6. active Goal 在 thread idle 时注入 Codex 等价 continuation steering 并原子尝试启动下一 turn；如果已有 turn、处于 Plan、存在 continuation deferral 或 tools 不可用，则不启动。
7. active turn 中编辑 objective 时注入 objective-updated steering；清除/暂停/阻塞 Goal 时先结算已发生用量，再清除 active marker。
8. 预算越界时在同一原子更新中累计用量并转 `budget_limited`，随后向运行中 turn 注入 budget-limit steering，禁止再启动实质工作。
9. usage limit 转 `usage_limited`；不可恢复或重试耗尽的 turn error 转 `blocked`；这两类状态都停止 idle continuation。
10. 模型标记 budgeted Goal complete 后，工具结构化结果包含最终 tokens/time，并要求最终答复报告使用量。

完成门槛：并发 usage 不丢失、重放不重复、换 Goal 不串账、Plan 不计账、超预算原子停机、空闲续跑只启动一次；错误不会形成无限自动 turn。

### 阶段 6：Goal API、SSE、Web UI、Resume 与 Fork

1. 在现有 REST 风格中提供 thread-goal 等价 API：get、set/edit、set status、clear；response 使用统一 Goal DTO，事件包含 conversation/thread id、可选 turn id 和完整 Goal 快照。
2. SSE 增加 goal updated/cleared/snapshot 通知；Goal 表写入成功是 response、通知和 runtime effects 的权威前置，rollout/audit event 采用 best-effort 追加并在失败时告警；断线重连先发当前 Goal snapshot，忽略其他 conversation 的通知。
3. 将 `/goal` 实现为 Goal 命令与菜单，而不是模式开关：
   - `/goal <objective>` 创建或请求替换；
   - 裸 `/goal` 直接展示状态摘要和可用子命令提示，不弹操作选择菜单；
   - `/goal edit|pause|resume|clear`；
   - 未完成 Goal 替换前确认。
4. Web 状态区展示 Goal objective、状态、elapsed time、`tokens_used/token_budget`；`budget_limited`、`usage_limited`、`blocked` 使用明确终止提示。
5. Resume conversation 时加载 Goal 快照并恢复 runtime/accounting baseline；不能把 downtime 计入 active work time。若状态为 `paused`、`blocked` 或 `usage_limited`，展示恢复 Goal 的二选一提示；`budget_limited` 与 `complete` 不提示。
6. 为 fork API 增加与 Codex 等价、默认 false 的实验参数 `deferGoalContinuation`：仅为 true 时，才在源 conversation 的串行化窗口内 flush usage，复制包括 goal_id、status、预算和累计用量在内的 Goal 快照，并设置 continuation deferral；新 conversation 的下一 turn 开始时删除 deferral，普通 fork 不复制 Goal。
7. Conversation 删除时级联删除 Goal 与 deferral；Goal clear 只清 Goal，不删除会话或历史。

完成门槛：刷新、重连、服务重启、pause/resume、普通 fork、deferred Goal fork、delete 后的 Goal 状态与 Codex 一致；UI 命令不再改变 collaboration mode。

### 阶段 7：删除兼容层并完成端到端验收

1. 在迁移窗口结束后删除 `goal/react/plan_execute` 模式别名、旧 Planner/Plan 调度类型、旧 Todo payload 和前端百分比 UI。
2. 更新 README、API 类型与配置样例，明确：Default/Plan 是协作模式，Goal 是可选线程目标，Todo 是模型进度工具。
3. 与固定 Codex commit 做一次逐项 contract review；若上游语义已变化，先升级基线和测试，再改实现，避免无版本地“追 main”。

完成门槛：代码库中不存在把 Goal 当 mode、把 Todo 当 execution scheduler、把 Proposed Plan 当普通 Todo 的路径。

## 6. 必测场景

### Todo

- 缺少 `plan`、未知字段、非法 status 被拒绝。
- Default 中成功返回 `Plan updated` 并 durable replay。
- Plan 中返回精确禁用错误且不产生更新。
- 多个 `in_progress` 与 Codex 一样由模型约束，handler 不额外拒绝。
- 工具成功/失败、reflection、resume 不会自动改 Todo。

### Plan Mode

- Default/Plan 设置可持久化、切换、fork、resume；活跃 turn 期间不能切换。
- Plan 与 Default 的普通工具注册/审批/沙箱一致；完整 prompt 要求非变更探索，Plan 没有额外通用运行时只读门禁。
- `request_user_input`：默认仅根 Plan 且 blocking；feature 开启后根 Default 可用且 non-blocking；选项非空、自动 Other、答案恢复原 turn。
- `<proposed_plan>` tag 跨 chunk、前后普通文本、未闭合、重复块、非独占行均有 parser 测试。
- 重复块不被 parser 拒绝：实时流保持同一 Plan item 且不重置已发 delta，最终提取缓冲重置并使完成 item 使用最后一个块；“最多一个”仅是 prompt 规则。
- Plan item 与 assistant message 分离；刷新可恢复；无 Plan item 不弹实现菜单。
- Todo update 不弹实现菜单；自动 idle work 不启动 Plan。
- “清空上下文后实现”创建 fresh conversation，只携带计划实现提示，不复制旧消息或 Goal。

### Goal

- ephemeral thread 与 Review subagent 无 Goal 工具；其他持久化 subagent 按 Codex 条件暴露；普通用户任务不会自动创建 Goal。
- 模型 create、用户替换确认、complete 后替换、所有状态转换与 terminal precedence。
- objective 先 trim；覆盖纯空白、trim 后 4000/4001 字符；预算 0/负数/上限内/超上限。
- cached input 扣除；改变独立 `reasoning_output_tokens` 不影响公式，且不从 `output_tokens` 反扣；并发增量、重复事件、旧 goal_id、Plan turn 零计费。
- active idle continuation 恰好一次；编辑 objective steering；pause/clear/blocked/limits 停止 continuation。
- budget crossing、usage limit、turn error、完成报告。
- restart/resume；paused/blocked/usage_limited 恢复提示与 budget_limited/complete 不提示；普通 fork 不继承 Goal，显式 deferred fork 才 flush/copy/deferral，且 deferral 在新线程下一 turn 开始时清除；delete cascade/SSE reconnect snapshot。

## 7. 最终验收标准

全部满足才可宣称“完全看齐”：

1. AgentCanvas 对外只存在 `Default|Plan` collaboration mode；Goal 不再出现在 mode enum。
2. `update_plan` 的 schema、Plan 禁用错误、模型结果和 UI 语义与 Codex 一致，没有 AgentCanvas 私有状态扩展。
3. Plan 使用完整 preset，支持结构化问答和独立 Proposed Plan item；一般 mutation 禁令与 Codex 一样由 developer prompt 约束，运行时只实现 Codex 确有的特定工具模式规则。
4. Proposed Plan、Todo、Goal 三者在类型、事件、存储、UI 和测试中没有复用同一状态对象。
5. Goal 拥有 Codex 六状态、三模型工具、用户控制、预算/usage、自动 continuation、steering、错误停机、resume/fork 语义。
6. Goal 计费公式和 Plan 排除规则通过 provider usage fixtures 与并发事务测试。
7. 现有用户未提交改动得到保留；迁移具备 up/down SQL；后端、Web、数据库集成和端到端测试全部通过。

## 8. 明确不做

- 不照搬 Codex 的 Rust extension 注册框架、TUI 组件或独立 Goals SQLite；AgentCanvas 已有等价的 Go service、MySQL repository、SSE 和 Web 边界。
- 不保留当前自动 LLM Planner 作为“增强功能”，因为它会继续混淆 Todo 和 Plan，并额外消耗一次模型调用。
- 不新增持久化 blocked 三轮计数器或 Todo 单一 in-progress 强校验，因为 Codex 当前也没有这些 handler 级规则。
- 不在本计划阶段修改任何生产代码、数据库或现有文档。

## 9. 子代理复审后的修订与阻塞门槛

子代理已直接阅读固定 Codex 源码并对本计划复审。首轮复审结论为 `FAIL`：计划原本把基线差距表误称为工作区现状，而且没有把若干已经识别出的实现缺口明确列成完成条件。本节是主代理根据复审结果补入的修订；在这些条款完成并验证前，只能称为“计划已对齐”，不能称为“AgentCanvas 已完全看齐”。

1. **Goal 状态 precedence**：`budget_limited` 必须保留为预算终态，用户/API 或模型请求 `paused`、`blocked`、重新 `active` 时不能覆盖它；模型请求 `complete` 必须允许把 `budget_limited` 正常结束。只有 `complete` 不得回退到其他状态。对应 Codex `state/src/runtime/goals.rs` 的 SQL CASE 语义，不能用“一律禁止 terminal 状态变更”替代。
2. **Objective 规范化顺序**：所有 API、工具和替换路径都必须先 `TrimSpace`，再按非空和最多 4000 个 Unicode 字符校验，最后持久化 trim 后值；必须覆盖 trim 后刚好 4000/4001 字符的测试。
3. **Deferred fork 结算**：只有显式 `deferGoalContinuation=true` 的 fork 才继承 Goal。复制前必须在源线程 Goal 状态锁/串行化窗口内 flush 当前 active turn 或 idle wall-clock 的未结算增量；已完成 run 不得再次累计。普通 fork 不复制 Goal，也不复制 deferral。
4. **增量 accounting**：provider 每个 `ModelUsage` 事件都要用 run 内 baseline 计算增量并立即持久化；turn complete/abort/error、外部 Goal mutation 和 fork 只结算 residual。增量必须带 `expected_goal_id`，旧 Goal 写入新 Goal 时返回 unchanged/conflict；Plan turn 从 start 起完全不计 token/time。baseline 必须在 run 结束时清理。
5. **工具可见性**：Goal 工具只在持久化线程可用；ephemeral thread 与 `SessionSource::SubAgent(SubAgentSource::Review)` 必须隐藏，其他持久化 subagent 不得被一律隐藏。不要把“root-only”误写成 Codex 的 Goal 工具规则。
6. **`request_user_input` 两种模式**：默认只有根 Plan 可见且 blocking；`DefaultModeRequestUserInput` feature 开启后，根 Default 才可见且必须 non-blocking。schema 可描述 1–3 题、`header` 长度等约束，但 handler 的 Codex 事实校验只强制每题 options 非空，并自动补 `Other`；不能额外把数量、header、推荐项等描述性规则升级成不兼容的服务端拒绝。
7. **Goal 事件不依赖 run**：Goal API/tool 的 create、update、clear 即使当前没有 latest turn/run，也必须产生 conversation-level 的 Goal updated/cleared 事件；重连时必须先发送当前 Goal snapshot。run 关联字段只能是可选上下文，不能因无 run 而静默丢事件。
8. **Objective steering**：active turn 中修改 objective 时，向正在运行的 turn 注入 developer steering；取消 queued/running run 只能作为无法注入时的兜底，不能代替 Codex 的 `inject_if_running` 语义。外部 mutation 仍需先 flush usage 并与 continuation 串行化。
9. **多实例 continuation 原子性**：进程内 `goalContinuationMu` 只能缩小单进程竞态，不能作为完成条件。必须有 Goal state permit/数据库事务或等价的条件 claim，保证“读取 active Goal—确认无 deferral—`StartIfIdle`”在多实例下只提交一次；已有 run、Plan、不可用工具和 deferral 都必须原子拒绝续跑。
10. **Todo 自动调度复核**：子代理已核对 `runner.go` 与测试，当前没有 runner 自动 claim/complete/fail Todo 的生产路径；后续修改不得重新引入自动 Planner、fallback Plan 或工具结果驱动的 Todo 状态变更。

复审结论：**计划文档已根据 FAIL 修订；实现审查仍为 FAIL，直到第 1–9 项具备代码与测试证据。**

第二轮复审（固定 Codex commit `46aa019e805352c9d7fd9a740cbf7f8b9aeb162d`）确认：

- `request_user_input` 工具层已补齐 Plan blocking、Default feature-enabled non-blocking 和 `Other` 标记；仍需验证独立 feature 配置命名、checkpoint/resume 透传及 Web 交互。
- 仍未达标的高优先级实现证据是：多实例 `StartIfIdle` 原子 claim；ModelUsage 的 model-call/累计边界与 abort/error/fork flush；预算 steering；deferred fork 的 token residual 和状态锁；Goal cleared/durable event；以及 Review subagent 的工具隐藏条件。
- 当前 `go test ./...`、Web tests/build 全绿只证明已有覆盖场景，不得替代上述并发、恢复和跨实例 contract tests。
