# Spec Delta: conversation-tool-history

## Purpose

定义会话历史的统一条目模型：每条消息携带条目类型（文本、工具调用、工具结果、推理、系统回声），运行时条目实时写入，子代理以两条目进父会话，工具条目排除出检索索引，API 与前端按类型区分呈现。

## ADDED Requirements

### Requirement: 条目类型模型

每条会话消息 MUST 携带条目类型，取值为 `text`、`function_call`、`function_call_output`、`reasoning`、`system_echo` 之一，缺省为 `text`。`function_call` 条目 MUST 携带工具调用标识、工具名与参数；`function_call_output` 条目 MUST 携带对应的工具调用标识与工具名。一条模型响应同时包含文本与多个工具调用时，MUST 拆分为 1 条 text 条目与 N 条 function_call 条目（每个调用一条）分别入库。

#### Scenario: 拆分带工具调用的助手响应

- **WHEN** 模型返回一条含文本正文与 2 个工具调用的响应
- **THEN** 消息表新增 3 行：1 行 text（role=assistant）、2 行 function_call（role=assistant，各带调用标识/工具名/参数）

#### Scenario: 工具结果条目关联调用

- **WHEN** 某次工具调用产生结果
- **THEN** 新增 1 行 function_call_output（role=tool），其工具调用标识与对应 function_call 条目一致

#### Scenario: 旧消息缺省类型

- **WHEN** 读取迁移前已存在的消息行
- **THEN** 其条目类型为 `text`，读取与压缩行为不受影响

### Requirement: 运行时条目实时写入

运行过程中每产生一条助手消息或工具结果，系统 MUST 立即将其写入会话消息表（不等待运行结束）；运行失败或被取消时，已写入的条目 MUST 保留（关联该运行的标识），不回滚。最终答案消息 MUST 不被重复写入（运行内已写入的终态助手消息与运行结束的落库二者只出现其一）。

#### Scenario: 运行中逐条可见

- **WHEN** 一次运行依次产生 助手文本、工具调用、工具结果、最终答案（每步后检查消息表）
- **THEN** 每一步完成后消息表中该行即存在，行序与产生顺序一致

#### Scenario: 失败运行保留已写条目

- **WHEN** 运行在产生 3 条条目后失败
- **THEN** 3 条条目仍在消息表中，均关联该运行标识；无补偿删除发生

#### Scenario: 最终答案不重复

- **WHEN** 一次成功运行结束时（运行内已写入最终助手消息）
- **THEN** 消息表中该运行对应的最终助手消息恰好一行（无第二行相同内容）

### Requirement: 暂停恢复写入幂等

运行因审批暂停后恢复时，重放的条目 MUST 不产生重复消息行；恢复产生的新条目 MUST 续接在已写入条目之后。

#### Scenario: 恢复不重复入库

- **WHEN** 运行已写入 5 条条目后暂停，随后从检查点恢复并完成（再次产生工具调用）
- **THEN** 原 5 条在消息表中各只出现一次，恢复后新增条目行紧随其后

### Requirement: 子代理条目写入父会话

子代理运行完成时，系统 MUST 向父会话写入恰好两条消息：一条 `function_call`（派发子代理，含子代理标识与任务参数）与一条 `function_call_output`（子代理最终输出）；子代理自身的中间轮次（助手文本、工具调用、工具结果）MUST 不写入父会话。

#### Scenario: 子代理完成写两条

- **WHEN** 一次子代理运行（内部 8 轮工具循环）成功完成
- **THEN** 父会话消息表新增恰好 2 行（1 function_call + 1 function_call_output），无第 3 行来自该子代理

#### Scenario: 子代理失败

- **WHEN** 一次子代理运行失败
- **THEN** 父会话写入 1 条 function_call 与 1 条携带错误信息的 function_call_output

### Requirement: 工具条目排除出索引

`function_call`、`function_call_output`、`reasoning`、`system_echo` 类型的条目 MUST 不进入会话搜索索引与上下文资源检索索引；`text` 类型条目行为不变。

#### Scenario: 工具条目不进索引

- **WHEN** 一条 function_call_output 消息写入消息表
- **THEN** 会话搜索索引与上下文资源索引均不新增该条对应的文档（索引写入调用次数为 0）

#### Scenario: 文本条目照常索引

- **WHEN** 一条 text 类型的用户消息与一条 text 类型的最终答案写入
- **THEN** 两者均按既有行为进入索引（各 1 次）

### Requirement: 消息 API 暴露条目类型

消息列表 API MUST 在每条消息上返回条目类型；工具条目 MUST 附带工具调用标识与工具名。列表消费方 MUST 能依据条目类型过滤。

#### Scenario: DTO 含类型字段

- **WHEN** 客户端请求某会话的消息列表，其中含 text、function_call、function_call_output 各 1 条
- **THEN** 响应中三条消息分别携带对应的条目类型，工具两条携带工具调用标识与工具名

### Requirement: 前端按类型渲染

前端聊天视图 MUST 照常渲染 user/assistant 的 text 条目；工具条目（function_call / function_call_output）MUST 以可折叠卡片呈现（默认折叠，显示工具名）；`reasoning` 与 `system_echo` 条目 MUST 不渲染。

#### Scenario: 工具条目折叠展示

- **WHEN** 会话历史含一条 function_call 与对应 function_call_output
- **THEN** 界面渲染 2 个折叠卡片（显示工具名），不显示原始 JSON 全文；展开后可见参数/结果内容

#### Scenario: 不可见类型不渲染

- **WHEN** 会话历史含 1 条 system_echo 与 1 条 text 消息
- **THEN** 界面只渲染 text 消息，system_echo 不出现在聊天流中

### Requirement: 旧数据自然共存

系统 MUST 不回填历史会话的工具条目；无工具条目的旧会话在压缩、渲染、检索各路径上的行为 MUST 与改造前一致（仅压缩输入信息量减少）。

#### Scenario: 旧会话正常压缩

- **WHEN** 对仅含 user/assistant text 行的旧会话触发压缩
- **THEN** 压缩正常完成，产生快照与摘要，无类型相关的错误
