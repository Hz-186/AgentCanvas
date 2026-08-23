import { FormEvent, useEffect, useState } from "react";
import { Boxes, FlaskConical, Globe2, Pencil, Trash2 } from "lucide-react";
import { settingsApi } from "../api/resources";
import {
  Button,
  EmptyState,
  Field,
  IconButton,
  Modal,
  Panel,
  Select,
  StatusBadge,
  TextArea,
  TextInput,
  Toast,
} from "../components/ui";
import type { ToolDefinition, ToolPack, ToolPackItem } from "../types/api";
import { friendlyErrorMessage, parseJsonObject } from "../utils/format";

const defaultToolConfig =
  '{\n  "url": "https://api.example.com/search",\n  "method": "GET",\n  "timeout_ms": 5000,\n  "max_response_bytes": 524288\n}';

export function ToolSettings({ section }: { section: "http" | "packs" }) {
  const [tools, setTools] = useState<ToolDefinition[]>([]);
  const [packs, setPacks] = useState<ToolPack[]>([]);
  const [packItems, setPackItems] = useState<ToolPackItem[]>([]);
  const [selectedPackId, setSelectedPackId] = useState(0);
  const [packToolId, setPackToolId] = useState(0);
  const [toolOpen, setToolOpen] = useState(false);
  const [editingToolId, setEditingToolId] = useState(0);
  const [toolName, setToolName] = useState("");
  const [toolDescription, setToolDescription] = useState("");
  const [toolConfig, setToolConfig] = useState(defaultToolConfig);
  const [toolTestInput, setToolTestInput] = useState("{}");
  const [toolTestResult, setToolTestResult] = useState("");
  const [packOpen, setPackOpen] = useState(false);
  const [packName, setPackName] = useState("");
  const [packDescription, setPackDescription] = useState("");
  const [message, setMessage] = useState("");
  const [error, setError] = useState("");

  async function load() {
    try {
      const toolList = await settingsApi.tools.list();
      setTools(toolList);
      if (section === "packs") {
        const packList = await settingsApi.tools.listPacks();
        setPacks(packList);
        if (
          selectedPackId &&
          packList.some((pack) => pack.id === selectedPackId)
        )
          setPackItems(await settingsApi.tools.listPackItems(selectedPackId));
      }
      setError("");
    } catch (err) {
      setError(friendlyErrorMessage(err, "加载工具配置失败"));
    }
  }

  useEffect(() => {
    void load();
  }, [section]);
  useEffect(() => {
    if (!selectedPackId) {
      setPackItems([]);
      return;
    }
    void settingsApi.tools
      .listPackItems(selectedPackId)
      .then(setPackItems)
      .catch((err) =>
        setError(friendlyErrorMessage(err, "加载 Tool Pack 明细失败")),
      );
  }, [selectedPackId]);

  function openCreateTool() {
    setEditingToolId(0);
    setToolName("");
    setToolDescription("");
    setToolConfig(defaultToolConfig);
    setToolTestInput("{}");
    setToolTestResult("");
    setToolOpen(true);
  }

  async function saveTool(event: FormEvent) {
    event.preventDefault();
    let config: Record<string, unknown>;
    try {
      config = parseJsonObject(toolConfig);
    } catch (err) {
      setError(friendlyErrorMessage(err, "HTTP Tool 配置需要是合法 JSON 对象"));
      return;
    }
    try {
      if (editingToolId)
        await settingsApi.tools.update(editingToolId, {
          name: toolName,
          description: toolDescription,
          config_json: config,
        });
      else
        await settingsApi.tools.create({
          name: toolName,
          tool_type: "http",
          description: toolDescription,
          config_json: config,
        });
      setToolOpen(false);
      setMessage(editingToolId ? "HTTP Tool 已更新" : "HTTP Tool 已创建");
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, "保存 HTTP Tool 失败"));
    }
  }

  async function editTool(summary: ToolDefinition) {
    try {
      const tool = await settingsApi.tools.get(summary.id);
      setEditingToolId(tool.id);
      setToolName(tool.name);
      setToolDescription(tool.description ?? "");
      setToolConfig(JSON.stringify(tool.config_json ?? {}, null, 2));
      setToolTestInput("{}");
      setToolTestResult("");
      setToolOpen(true);
    } catch (err) {
      setError(friendlyErrorMessage(err, "加载 HTTP Tool 详情失败"));
    }
  }

  async function testTool() {
    try {
      const result = await settingsApi.tools.test(
        editingToolId,
        parseJsonObject(toolTestInput),
      );
      setToolTestResult(JSON.stringify(result, null, 2));
      setMessage("HTTP Tool 测试完成");
    } catch (err) {
      setError(friendlyErrorMessage(err, "HTTP Tool 测试失败"));
    }
  }

  async function removeTool(id: number) {
    try {
      await settingsApi.tools.remove(id);
      setMessage("HTTP Tool 已删除");
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, "删除 HTTP Tool 失败"));
    }
  }

  async function createPack(event: FormEvent) {
    event.preventDefault();
    try {
      const created = await settingsApi.tools.createPack({
        name: packName,
        description: packDescription,
      });
      setPackOpen(false);
      setPackName("");
      setPackDescription("");
      setSelectedPackId(created.id);
      setMessage("Tool Pack 已创建");
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, "创建 Tool Pack 失败"));
    }
  }

  async function removePack(id: number) {
    try {
      await settingsApi.tools.removePack(id);
      if (selectedPackId === id) setSelectedPackId(0);
      setMessage("Tool Pack 已删除");
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, "删除 Tool Pack 失败"));
    }
  }

  async function addToolToPack() {
    if (!selectedPackId || !packToolId) {
      setError("请选择 Tool Pack 和工具");
      return;
    }
    try {
      await settingsApi.tools.addPackItem(selectedPackId, packToolId);
      setPackToolId(0);
      setPackItems(await settingsApi.tools.listPackItems(selectedPackId));
      setMessage("工具已加入 Tool Pack");
      setError("");
    } catch (err) {
      setError(friendlyErrorMessage(err, "添加工具到 Tool Pack 失败"));
    }
  }

  async function removeToolFromPack(toolId: number) {
    if (!selectedPackId) return;
    try {
      await settingsApi.tools.removePackItem(selectedPackId, toolId);
      setPackItems(await settingsApi.tools.listPackItems(selectedPackId));
      setMessage("工具已移出 Tool Pack");
    } catch (err) {
      setError(friendlyErrorMessage(err, "移除 Tool Pack 工具失败"));
    }
  }

  return (
    <>
      {error ? <p className="error-text">{error}</p> : null}
      {section === "http" ? (
        <Panel
          className="management-panel section-http"
          title="HTTP 工具"
          eyebrow="HTTP Tools"
          action={
            <Button tone="primary" onClick={openCreateTool}>
              <Globe2 size={16} />
              New
            </Button>
          }
        >
          <div className="stack">
            {tools.length === 0 ? (
              <EmptyState
                title="还没有 HTTP 工具"
                description="新增工具后，Agent 可以在流程中调用外部接口。"
              />
            ) : (
              tools.map((tool) => (
                <article className="card" key={tool.id}>
                  <div className="card-title">
                    <h3 className="truncate">{tool.name}</h3>
                    <StatusBadge tone={tool.enabled ? "good" : "neutral"}>
                      {tool.enabled ? "Active" : "Disabled"}
                    </StatusBadge>
                  </div>
                  <p className="muted truncate">
                    {tool.description || "HTTP Tool"}
                  </p>
                  <div className="row-wrap">
                    <Button onClick={() => void editTool(tool)}>
                      <Pencil size={15} />
                      编辑与测试
                    </Button>
                    <IconButton
                      label="删除 HTTP Tool"
                      onClick={() => void removeTool(tool.id)}
                    >
                      <Trash2 size={16} />
                    </IconButton>
                  </div>
                </article>
              ))
            )}
          </div>
        </Panel>
      ) : (
        <Panel
          className="management-panel section-packs"
          title="Tool Pack"
          eyebrow="Collections"
          action={
            <Button tone="primary" onClick={() => setPackOpen(true)}>
              <Boxes size={16} />
              New
            </Button>
          }
        >
          <div className="stack">
            {packs.length === 0 ? (
              <EmptyState
                title="还没有 Tool Pack"
                description="把常用工具组合成工具包，便于后续绑定到 Agent。"
              />
            ) : (
              <>
                <Field label="当前 Pack">
                  <Select
                    value={selectedPackId}
                    onChange={(event) =>
                      setSelectedPackId(Number(event.target.value))
                    }
                  >
                    <option value={0}>选择 Tool Pack</option>
                    {packs.map((pack) => (
                      <option key={pack.id} value={pack.id}>
                        {pack.name}
                      </option>
                    ))}
                  </Select>
                </Field>
                <div className="inline-form">
                  <Select
                    value={packToolId}
                    onChange={(event) =>
                      setPackToolId(Number(event.target.value))
                    }
                  >
                    <option value={0}>选择工具</option>
                    {tools.map((tool) => (
                      <option key={tool.id} value={tool.id}>
                        {tool.name}
                      </option>
                    ))}
                  </Select>
                  <Button onClick={() => void addToolToPack()}>加入</Button>
                </div>
                {packs.map((pack) => (
                  <article className="card" key={pack.id}>
                    <div className="card-title">
                      <h3 className="truncate">{pack.name}</h3>
                      <StatusBadge
                        tone={pack.id === selectedPackId ? "info" : "neutral"}
                      >
                        {pack.id === selectedPackId ? "selected" : "pack"}
                      </StatusBadge>
                    </div>
                    <p className="muted clamp-2">
                      {pack.description || "无描述"}
                    </p>
                    <IconButton
                      label="删除 Tool Pack"
                      onClick={() => void removePack(pack.id)}
                    >
                      <Trash2 size={16} />
                    </IconButton>
                  </article>
                ))}
                {selectedPackId ? (
                  <div className="trace-list">
                    {packItems.length === 0 ? (
                      <p className="muted">当前 Pack 暂无工具。</p>
                    ) : (
                      packItems.map((item) => (
                        <article className="trace-item" key={item.id}>
                          <div className="trace-item-head">
                            <strong>
                              {tools.find((tool) => tool.id === item.tool_id)
                                ?.name ?? `Tool #${item.tool_id}`}
                            </strong>
                            <IconButton
                              label="移出 Tool Pack"
                              onClick={() =>
                                void removeToolFromPack(item.tool_id)
                              }
                            >
                              <Trash2 size={15} />
                            </IconButton>
                          </div>
                        </article>
                      ))
                    )}
                  </div>
                ) : null}
              </>
            )}
          </div>
        </Panel>
      )}
      <Modal
        open={toolOpen}
        title={editingToolId ? "编辑 HTTP Tool" : "新增 HTTP Tool"}
        onClose={() => setToolOpen(false)}
        footer={
          <>
            <Button type="button" onClick={() => setToolOpen(false)}>
              取消
            </Button>
            <Button form="create-tool-form" tone="primary">
              保存
            </Button>
          </>
        }
      >
        <form
          id="create-tool-form"
          className="form-stack"
          onSubmit={(event) => void saveTool(event)}
        >
          <Field label="名称">
            <TextInput
              value={toolName}
              onChange={(event) => setToolName(event.target.value)}
              required
            />
          </Field>
          <Field label="描述">
            <TextInput
              value={toolDescription}
              onChange={(event) => setToolDescription(event.target.value)}
            />
          </Field>
          <Field label="配置 JSON">
            <TextArea
              value={toolConfig}
              onChange={(event) => setToolConfig(event.target.value)}
              required
            />
          </Field>
          <pre className="code-box">{toolConfig}</pre>
          {editingToolId ? (
            <>
              <Field label="测试输入 JSON">
                <TextArea
                  value={toolTestInput}
                  onChange={(event) => setToolTestInput(event.target.value)}
                />
              </Field>
              <Button type="button" onClick={() => void testTool()}>
                <FlaskConical size={15} />
                运行测试
              </Button>
              {toolTestResult ? (
                <pre className="code-box">{toolTestResult}</pre>
              ) : null}
            </>
          ) : null}
        </form>
      </Modal>
      <Modal
        open={packOpen}
        title="新增 Tool Pack"
        onClose={() => setPackOpen(false)}
        footer={
          <>
            <Button type="button" onClick={() => setPackOpen(false)}>
              取消
            </Button>
            <Button form="create-pack-form" tone="primary">
              保存
            </Button>
          </>
        }
      >
        <form
          id="create-pack-form"
          className="form-stack"
          onSubmit={(event) => void createPack(event)}
        >
          <Field label="名称">
            <TextInput
              value={packName}
              onChange={(event) => setPackName(event.target.value)}
              required
            />
          </Field>
          <Field label="描述">
            <TextArea
              value={packDescription}
              onChange={(event) => setPackDescription(event.target.value)}
            />
          </Field>
        </form>
      </Modal>
      <Toast message={message} tone="good" />
    </>
  );
}
