import { type CSSProperties, type MouseEvent as ReactMouseEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useParams } from 'react-router-dom';
import {
  Background,
  BackgroundVariant,
  Controls,
  Handle,
  Position,
  ReactFlow,
  addEdge,
  applyEdgeChanges,
  applyNodeChanges,
  type Connection,
  type Edge,
  type EdgeChange,
  type Node,
  type NodeChange,
  type NodeProps,
} from '@xyflow/react';
import {
  Bot,
  BrainCircuit,
  Braces,
  Database,
  GitBranch,
  Globe2,
  MessageSquare,
  Play,
  Save,
  Send,
  ShieldCheck,
  Sparkles,
  Workflow,
} from 'lucide-react';
import { agentApi, knowledgeApi, settingsApi } from '../api/resources';
import { Button, EmptyState, Field, Panel, Segmented, Select, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
import type { Agent, FlowVersion, KnowledgeBase, MemoryWriteLog, ModelProvider, RunStep, ToolDefinition, ToolInvocation } from '../types/api';
import type { DSLEdge, DSLNode, FlowDSL, NodeConfig, NodeType } from '../types/flow';
import type { RuntimeEvent } from '../types/events';
import { friendlyErrorMessage, parseJsonObject, prettyJson } from '../utils/format';

interface CanvasNodeData extends Record<string, unknown> {
  label: string;
  nodeType: NodeType;
  config: NodeConfig & { _ui?: { x: number; y: number } };
}

type CanvasNode = Node<CanvasNodeData>;
type DebugRunMode = 'stream' | 'complete';

const nodeMeta: Record<NodeType, { label: string; icon: React.ElementType; description: string }> = {
  begin: { label: 'Begin', icon: Sparkles, description: '读取运行输入' },
  knowledge_retrieval: { label: 'Retrieval', icon: Database, description: '从知识库检索上下文' },
  prompt: { label: 'Prompt', icon: MessageSquare, description: '组装提示词' },
  llm: { label: 'LLM', icon: BrainCircuit, description: '调用模型生成内容' },
  agent_loop: { label: 'Agent Loop', icon: Bot, description: '自主选择工具并循环推理' },
  agent_call: { label: 'Call Agent', icon: Workflow, description: '调用另一个 Agent' },
  code_sandbox: { label: 'Code Sandbox', icon: Braces, description: '隔离执行 Python 代码' },
  message: { label: 'Message', icon: Send, description: '输出或写入会话消息' },
  memory_read: { label: 'Memory Read', icon: BrainCircuit, description: '读取长期记忆' },
  memory_write: { label: 'Memory Write', icon: Save, description: '写入或更新记忆' },
  http_tool: { label: 'HTTP Tool', icon: Globe2, description: '调用受控 HTTP 工具' },
  switch: { label: 'Switch', icon: GitBranch, description: '按条件选择分支' },
  json_output: { label: 'JSON Output', icon: Braces, description: '校验结构化输出' },
  guardrail: { label: 'Guardrail', icon: ShieldCheck, description: '检查输出规则' },
};

function defaultConfig(type: NodeType): CanvasNodeData['config'] {
  if (type === 'begin') return { input_schema: { query: 'string' } };
  if (type === 'knowledge_retrieval') return { kb_ids: [], top_k: 5, mode: 'keyword', query: '{{sys.query}}' };
  if (type === 'prompt') return { template: '请根据以下上下文回答用户问题：\n\n{{retrieval.context}}\n\n问题：{{sys.query}}' };
  if (type === 'llm') return { provider_id: 0, model: '', temperature: 0.7, stream: true };
  if (type === 'agent_loop') {
    return {
      provider_id: 0,
      model: '',
      system_prompt: '你是一个严谨的 Agent。必要时调用可用工具，看到工具结果后再继续推理并给出最终答案。',
      task_template: '{{sys.query}}',
      tool_ids: [],
      knowledge_ids: [],
      knowledge_top_k: 5,
      knowledge_mode: 'keyword',
      call_agent_ids: [],
      max_agent_call_depth: 3,
      code_execution_enabled: false,
      max_iterations: 8,
      max_tool_calls: 16,
      max_execution_time_ms: 120000,
      temperature: 0.2,
      return_intermediate_steps: true,
      output_mode: 'final_answer',
    };
  }
  if (type === 'agent_call') return { agent_id: 0, input: { query: '{{sys.query}}' }, max_depth: 3 };
  if (type === 'code_sandbox') return { language: 'python', code: 'print("hello from sandbox")', timeout_ms: 5000, max_output_bytes: 65536, network_enabled: false, memory_limit_mb: 128 };
  if (type === 'message') return { content: '{{llm.content}}', with_citation: true };
  if (type === 'memory_read') return { memory_types: ['profile_memory', 'summary_memory'], limit: 5 };
  if (type === 'memory_write') return { memory_type: 'summary_memory', content: '{{llm.content}}', importance: 0.5, source: 'agent' };
  if (type === 'http_tool') return { tool_id: 0, input: { query: '{{sys.query}}' } };
  if (type === 'switch') return { conditions: [{ expr: '{{retrieval.result_count}} > 0', target: 'llm' }, { expr: 'default', target: 'message' }] };
  if (type === 'json_output') return { value: '{{llm.content}}', schema: { type: 'object' } };
  return { source: '{{llm.content}}', max_length: 4000, banned_terms: [], require_citation: false, require_json: false };
}

function AgentNode({ data, selected }: NodeProps<CanvasNode>) {
  const meta = nodeMeta[data.nodeType];
  const Icon = meta.icon;
  return (
    <div className="agent-node" style={{ borderColor: selected ? 'var(--accent)' : undefined }}>
      <Handle type="target" position={Position.Left} />
      <div className="node-icon">
        <Icon size={16} />
      </div>
      <strong className="truncate">{data.label}</strong>
      <span className="truncate">{meta.description}</span>
      <Handle type="source" position={Position.Right} />
    </div>
  );
}

const nodeTypes = { agentNode: AgentNode };
const debugRunModeOptions: Array<{ value: DebugRunMode; label: string }> = [
  { value: 'stream', label: '流式运行' },
  { value: 'complete', label: '完整运行' },
];
const DEFAULT_PALETTE_WIDTH = 184;
const COLLAPSED_PALETTE_WIDTH = 58;
const MIN_PALETTE_WIDTH = 172;
const MAX_PALETTE_WIDTH = 240;
const DEFAULT_PANEL_WIDTH = 360;
const MIN_PANEL_WIDTH = 300;
const PANEL_COLLAPSE_THRESHOLD = 280;
const MAX_PANEL_WIDTH = 460;

function normalizeDSL(raw: unknown): FlowDSL | null {
  if (!raw) return null;
  const value = typeof raw === 'string' ? JSON.parse(raw) : raw;
  if (!value || typeof value !== 'object') return null;
  const maybe = value as Partial<FlowDSL>;
  if (!Array.isArray(maybe.nodes) || !Array.isArray(maybe.edges)) return null;
  return maybe as FlowDSL;
}

function stableRuntimeValue(value: unknown): unknown {
  if (Array.isArray(value)) return value.map(stableRuntimeValue);
  if (!value || typeof value !== 'object') return value;
  return Object.fromEntries(
    Object.entries(value as Record<string, unknown>)
      .sort(([left], [right]) => left.localeCompare(right))
      .map(([key, child]) => [key, stableRuntimeValue(child)]),
  );
}

function runtimeConfig(config: unknown): unknown {
  if (!config || typeof config !== 'object' || Array.isArray(config)) return stableRuntimeValue(config ?? {});
  const rest = { ...(config as Record<string, unknown>) };
  delete rest._ui;
  return stableRuntimeValue(rest);
}

function runtimeDSLKey(raw: unknown): string {
  const parsed = normalizeDSL(raw);
  if (!parsed) return '';
  return JSON.stringify({
    schema_version: parsed.schema_version,
    flow_id: parsed.flow_id,
    nodes: [...parsed.nodes]
      .map((node) => ({ ...node, config: runtimeConfig(node.config) }))
      .sort((left, right) => `${left.id}:${left.type}:${left.name}`.localeCompare(`${right.id}:${right.type}:${right.name}`)),
    edges: [...parsed.edges].sort((left, right) => `${left.from}:${left.to}`.localeCompare(`${right.from}:${right.to}`)),
  });
}

function defaultNodes(): CanvasNode[] {
  return [
    {
      id: 'begin',
      type: 'agentNode',
      position: { x: 120, y: 170 },
      data: { label: 'Begin', nodeType: 'begin', config: defaultConfig('begin') },
    },
  ];
}

function fromDSL(dsl: FlowDSL): { nodes: CanvasNode[]; edges: Edge[] } {
  const nodes: CanvasNode[] = dsl.nodes.map((node, index) => {
    const config = (node.config ?? {}) as CanvasNodeData['config'];
    const pos = config._ui ?? { x: 120 + index * 240, y: 170 };
    return {
      id: node.id,
      type: 'agentNode',
      position: pos,
      data: {
        label: node.name || nodeMeta[node.type]?.label || node.type,
        nodeType: node.type,
        config,
      },
    };
  });
  return {
    nodes: nodes.length > 0 ? nodes : defaultNodes(),
    edges: dsl.edges.map((edge, index) => ({ id: `edge-${edge.from}-${edge.to}-${index}`, source: edge.from, target: edge.to })),
  };
}

function toDSL(agentId: number, nodes: CanvasNode[], edges: Edge[]): FlowDSL {
  return {
    schema_version: 'v1',
    flow_id: `agent-${agentId}`,
    nodes: nodes.map<DSLNode>((node) => ({
      id: node.id,
      type: node.data.nodeType,
      name: node.data.label,
      config: {
        ...node.data.config,
        _ui: { x: Math.round(node.position.x), y: Math.round(node.position.y) },
      },
    })),
    edges: edges.map<DSLEdge>((edge) => ({ from: edge.source, to: edge.target })),
  };
}

function validateLocal(nodes: CanvasNode[], edges: Edge[]): string {
  const beginCount = nodes.filter((node) => node.data.nodeType === 'begin').length;
  if (beginCount !== 1) return '画布必须且只能包含一个 Begin 节点';
  const ids = new Set(nodes.map((node) => node.id));
  for (const edge of edges) {
    if (!ids.has(edge.source) || !ids.has(edge.target) || edge.source === edge.target) return '连线包含无效节点';
  }
  for (const node of nodes) {
    const config = node.data.config as Record<string, unknown>;
    if (node.data.nodeType === 'knowledge_retrieval' && (!Array.isArray(config.kb_ids) || config.kb_ids.length === 0)) return 'Retrieval 节点需要选择知识库';
    if (node.data.nodeType === 'prompt' && !String(config.template ?? '').trim()) return 'Prompt 节点需要模板';
    if (node.data.nodeType === 'llm' && Number(config.provider_id ?? 0) <= 0) return 'LLM 节点需要选择 Provider';
    if (node.data.nodeType === 'agent_loop' && Number(config.provider_id ?? 0) <= 0) return 'Agent Loop 节点需要选择 Provider';
    if (node.data.nodeType === 'agent_loop' && !String(config.task_template ?? '').trim()) return 'Agent Loop 节点需要任务模板';
    if (node.data.nodeType === 'agent_call' && Number(config.agent_id ?? 0) <= 0) return 'Call Agent 节点需要选择 Agent';
    if (node.data.nodeType === 'code_sandbox' && !String(config.code ?? '').trim()) return 'Code Sandbox 节点需要代码';
    if (node.data.nodeType === 'message' && !String(config.content ?? '').trim()) return 'Message 节点需要内容';
    if (node.data.nodeType === 'memory_write' && (!String(config.memory_type ?? '').trim() || !String(config.content ?? '').trim())) return 'Memory Write 节点需要类型和内容';
    if (node.data.nodeType === 'http_tool' && Number(config.tool_id ?? 0) <= 0) return 'HTTP Tool 节点需要选择 Tool';
    if (node.data.nodeType === 'switch' && !Array.isArray(config.conditions)) return 'Switch 节点需要 conditions';
    if (node.data.nodeType === 'json_output' && !String(config.value ?? '').trim()) return 'JSON Output 节点需要 value';
    if (node.data.nodeType === 'guardrail' && !String(config.source ?? '').trim()) return 'Guardrail 节点需要 source';
  }
  return '';
}

function nodeTitle(type: NodeType, nodes: CanvasNode[]) {
  const base = type === 'knowledge_retrieval' ? 'retrieval' : type;
  if (type === 'begin') return 'begin';
  let i = 1;
  let id = base;
  while (nodes.some((node) => node.id === id)) {
    i += 1;
    id = `${base}_${i}`;
  }
  return id;
}

function runOutputText(output: Record<string, unknown>) {
  const content = output.content;
  if (typeof content === 'string' && content.trim()) return content;
  return prettyJson(output);
}

export function CanvasPage() {
  const { id } = useParams();
  const agentId = Number(id);
  const [agent, setAgent] = useState<Agent | null>(null);
  const [version, setVersion] = useState<FlowVersion | null>(null);
  const [nodes, setNodes] = useState<CanvasNode[]>([]);
  const [edges, setEdges] = useState<Edge[]>([]);
  const [selectedId, setSelectedId] = useState<string>('begin');
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [callableAgents, setCallableAgents] = useState<Agent[]>([]);
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([]);
  const [tools, setTools] = useState<ToolDefinition[]>([]);
  const [mode, setMode] = useState<'config' | 'debug' | 'dsl'>('config');
  const [paletteWidth, setPaletteWidth] = useState(DEFAULT_PALETTE_WIDTH);
  const [configWidth, setConfigWidth] = useState(DEFAULT_PANEL_WIDTH);
  const [sidePanelOpen, setSidePanelOpen] = useState(true);
  const [pendingConnectId, setPendingConnectId] = useState<string>('');
  const [runInput, setRunInput] = useState('{\n  "query": "请总结知识库内容"\n}');
  const [debugRunMode, setDebugRunMode] = useState<DebugRunMode>('stream');
  const [runningDebugMode, setRunningDebugMode] = useState<DebugRunMode | null>(null);
  const [events, setEvents] = useState<RuntimeEvent[]>([]);
  const [runOutput, setRunOutput] = useState<Record<string, unknown> | null>(null);
  const [runSteps, setRunSteps] = useState<RunStep[]>([]);
  const [memoryLogs, setMemoryLogs] = useState<MemoryWriteLog[]>([]);
  const [toolInvocations, setToolInvocations] = useState<ToolInvocation[]>([]);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');
  const canvasBodyRef = useRef<HTMLDivElement | null>(null);
  const resizeFrameRef = useRef<number | null>(null);
  const resizeClientXRef = useRef(0);
  // 进行中的调试运行控制器：组件卸载或重新运行时取消，避免“卸载后 setState”。
  const runAbortRef = useRef<AbortController | null>(null);

  const selected = useMemo(() => nodes.find((node) => node.id === selectedId) ?? null, [nodes, selectedId]);
  const dsl = useMemo(() => toDSL(agentId, nodes, edges), [agentId, nodes, edges]);
  const currentRuntimeKey = useMemo(() => runtimeDSLKey(dsl), [dsl]);
  const savedRuntimeKey = useMemo(() => runtimeDSLKey(version?.dsl_json), [version]);
  const hasRuntimeChanges = !version || currentRuntimeKey !== savedRuntimeKey;

  useEffect(() => {
    let cancelled = false;
    async function load() {
      const [agentResp, agentsResp, providersResp, kbResp, toolResp] = await Promise.all([
        agentApi.get(agentId),
        agentApi.list(),
        settingsApi.providers.list(),
        knowledgeApi.list(),
        settingsApi.tools.list(),
      ]);
      // 快速切换 agent 时旧请求可能晚返回，确认仍是当前 agent 再写入。
      if (cancelled) return;
      setAgent(agentResp);
      setCallableAgents(agentsResp.filter((item) => item.id !== agentId));
      setProviders(providersResp);
      setKnowledgeBases(kbResp);
      setTools(toolResp);
      if (agentResp.current_version_id) {
        const flow = await agentApi.getFlowVersion(agentResp.current_version_id);
        if (cancelled) return;
        setVersion(flow);
        const parsed = normalizeDSL(flow.dsl_json);
        const canvas = parsed ? fromDSL(parsed) : { nodes: defaultNodes(), edges: [] };
        setNodes(canvas.nodes);
        setEdges(canvas.edges);
        setSelectedId(canvas.nodes[0]?.id ?? 'begin');
      } else {
        setNodes(defaultNodes());
        setEdges([]);
        setSelectedId('begin');
      }
    }
    if (agentId > 0) {
      void load().catch((err) => {
        if (!cancelled) setError(friendlyErrorMessage(err, '加载画布失败'));
      });
    }
    return () => {
      cancelled = true;
    };
  }, [agentId]);

  // 组件卸载时取消进行中的调试流。
  useEffect(() => () => runAbortRef.current?.abort(), []);

  const onNodesChange = useCallback((changes: NodeChange<CanvasNode>[]) => setNodes((current) => applyNodeChanges(changes, current)), []);
  const onEdgesChange = useCallback((changes: EdgeChange[]) => setEdges((current) => applyEdgeChanges(changes, current)), []);
  const onConnect = useCallback((connection: Connection) => {
    if (!connection.source || !connection.target || connection.source === connection.target) return;
    setEdges((current) => addEdge(connection, current));
  }, []);

  const startResize = useCallback((side: 'palette' | 'panel', startX: number) => {
    const body = canvasBodyRef.current;
    if (!body) return;
    const rect = body.getBoundingClientRect();
    const startsFromClosedPanel = side === 'panel' && !sidePanelOpen;

    function applyResize(clientX: number) {
      if (side === 'palette') {
        const next = clientX - rect.left;
        const width = Math.min(MAX_PALETTE_WIDTH, Math.max(COLLAPSED_PALETTE_WIDTH, next));
        setPaletteWidth((current) => (current === width ? current : width));
        return;
      }

      const next = startsFromClosedPanel ? MIN_PANEL_WIDTH + (startX - clientX) : rect.right - clientX;
      if (next < PANEL_COLLAPSE_THRESHOLD) {
        setSidePanelOpen(false);
        setConfigWidth(MIN_PANEL_WIDTH);
      } else {
        setSidePanelOpen(true);
        const width = Math.min(MAX_PANEL_WIDTH, Math.max(MIN_PANEL_WIDTH, next));
        setConfigWidth((current) => (current === width ? current : width));
      }
    }

    function onMove(event: PointerEvent) {
      resizeClientXRef.current = event.clientX;
      if (resizeFrameRef.current !== null) return;
      resizeFrameRef.current = window.requestAnimationFrame(() => {
        resizeFrameRef.current = null;
        applyResize(resizeClientXRef.current);
      });
    }

    function onUp() {
      if (resizeFrameRef.current !== null) {
        window.cancelAnimationFrame(resizeFrameRef.current);
        resizeFrameRef.current = null;
      }
      applyResize(resizeClientXRef.current);
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      document.body.classList.remove('is-resizing-panels');
    }

    document.body.classList.add('is-resizing-panels');
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    resizeClientXRef.current = startX;
    applyResize(startX);
  }, [sidePanelOpen]);

  function connectByShiftClick(node: CanvasNode) {
    if (!pendingConnectId) {
      setPendingConnectId(node.id);
      setSelectedId(node.id);
      setMessage(`已选择 ${node.data.label}，按住 Shift 点击另一个模块即可连线`);
      return;
    }
    if (pendingConnectId === node.id) {
      setPendingConnectId('');
      setMessage('已取消快速连线');
      return;
    }
    const edgeId = `edge-${pendingConnectId}-${node.id}`;
    setEdges((current) => {
      if (current.some((edge) => edge.source === pendingConnectId && edge.target === node.id)) return current;
      return [...current, { id: edgeId, source: pendingConnectId, target: node.id }];
    });
    setPendingConnectId('');
    setSelectedId(node.id);
    setMessage('已通过 Shift 点击创建连线');
  }

  function handleNodeClick(event: ReactMouseEvent, node: CanvasNode) {
    if (event.shiftKey) {
      connectByShiftClick(node);
      return;
    }
    setPendingConnectId('');
    setSelectedId(node.id);
  }

  function addNode(type: NodeType) {
    if (type === 'begin' && nodes.some((node) => node.data.nodeType === 'begin')) {
      setError('Begin 节点已经存在');
      return;
    }
    const nodeId = nodeTitle(type, nodes);
    const shouldAutoConnect = Boolean(selectedId) && selectedId !== nodeId && nodes.some((node) => node.id === selectedId);
    const node: CanvasNode = {
      id: nodeId,
      type: 'agentNode',
      position: { x: 180 + nodes.length * 38, y: 120 + nodes.length * 34 },
      data: { label: nodeMeta[type].label, nodeType: type, config: defaultConfig(type) },
    };
    setNodes((current) => [...current, node]);
    if (shouldAutoConnect) {
      setEdges((current) => {
        if (current.some((edge) => edge.source === selectedId && edge.target === nodeId)) return current;
        return [...current, { id: `edge-${selectedId}-${nodeId}`, source: selectedId, target: nodeId }];
      });
    }
    setSelectedId(nodeId);
    setError('');
  }

  function updateSelectedConfig(patch: Record<string, unknown>) {
    setNodes((current) =>
      current.map((node) =>
        node.id === selectedId
          ? { ...node, data: { ...node.data, config: { ...node.data.config, ...patch } } }
          : node,
      ),
    );
  }

  function updateSelectedJSON(key: string, raw: string) {
    try {
      updateSelectedConfig({ [key]: JSON.parse(raw) as unknown });
      setError('');
    } catch {
      setError('JSON 格式不正确');
    }
  }

  async function saveFlow(): Promise<FlowVersion | null> {
    const localError = validateLocal(nodes, edges);
    if (localError) {
      setError(localError);
      return null;
    }
    if (version && !hasRuntimeChanges) {
      setMessage(`Flow v${version.version_no} 无实质变化，无需保存`);
      setError('');
      return version;
    }
    try {
      const saved = await agentApi.createFlowVersion(agentId, { dsl_json: dsl, description: 'Saved from visual canvas' });
      setVersion(saved);
      setMessage(`已保存 Flow v${saved.version_no}`);
      setError('');
      return saved;
    } catch (err) {
      setError(friendlyErrorMessage(err, '保存 Flow 失败'));
      return null;
    }
  }

  async function publishFlow() {
    try {
      const target = hasRuntimeChanges ? await saveFlow() : version;
      if (!target) return;
      const published = await agentApi.publishFlowVersion(target.id);
      setVersion(published);
      setMessage(`已发布 v${published.version_no}`);
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, '发布 Flow 失败'));
    }
  }

  async function runDebug() {
    if (runAbortRef.current) return;
    let input: Record<string, unknown>;
    try {
      input = parseJsonObject(runInput);
    } catch (err) {
      setError(friendlyErrorMessage(err, '运行输入需要是合法 JSON 对象'));
      return;
    }
    // 未保存草稿时先保存，确保用当前画布内容运行，而不是传 flow_version_id=0。
    let target = version;
    if (!target || hasRuntimeChanges) {
      target = await saveFlow();
      if (!target) return;
    }
    const controller = new AbortController();
    const selectedRunMode = debugRunMode;
    runAbortRef.current = controller;
    setRunningDebugMode(selectedRunMode);
    setEvents([]);
    setRunOutput(null);
    setRunSteps([]);
    setMemoryLogs([]);
    setToolInvocations([]);
    setError('');
    try {
      if (selectedRunMode === 'complete') {
        const resp = await agentApi.run(agentId, { flow_version_id: target.id, input });
        if (controller.signal.aborted) return;
        setRunOutput(resp.output);
        void Promise.all([
          agentApi.listRunSteps(resp.run.id),
          agentApi.listMemoryWriteLogs(resp.run.id),
          agentApi.listToolInvocations(resp.run.id),
        ]).then(([stepResp, memoryResp, toolResp]) => {
          if (controller.signal.aborted) return;
          setRunSteps(stepResp);
          setMemoryLogs(memoryResp);
          setToolInvocations(toolResp);
        }).catch(() => undefined);
        return;
      }
      await agentApi.streamRun(agentId, { flow_version_id: target.id, input }, {
        signal: controller.signal,
        onMessage: (msg) => {
          if (controller.signal.aborted) return;
          if (msg.event === 'done') {
            const done = JSON.parse(msg.data) as { run: { id: number }; output: Record<string, unknown> };
            setRunOutput(done.output);
            void Promise.all([
              agentApi.listRunSteps(done.run.id),
              agentApi.listMemoryWriteLogs(done.run.id),
              agentApi.listToolInvocations(done.run.id),
            ]).then(([stepResp, memoryResp, toolResp]) => {
              if (controller.signal.aborted) return;
              setRunSteps(stepResp);
              setMemoryLogs(memoryResp);
              setToolInvocations(toolResp);
            }).catch(() => undefined);
            return;
          }
          if (msg.event === 'error') {
            setError(friendlyErrorMessage(msg.data, '调试运行失败'));
            return;
          }
          const data = JSON.parse(msg.data) as RuntimeEvent;
          setEvents((current) => [...current, data]);
        },
        onError: (err) => {
          if (controller.signal.aborted) return;
          setError(friendlyErrorMessage(err, '调试运行失败'));
        },
      });
    } catch (err) {
      if (!controller.signal.aborted) setError(friendlyErrorMessage(err, '调试失败'));
    } finally {
      if (runAbortRef.current === controller) {
        runAbortRef.current = null;
        if (!controller.signal.aborted) setRunningDebugMode(null);
      }
    }
  }

  const config = selected?.data.config as Record<string, unknown> | undefined;

  return (
    <div className="canvas-page">
      <header className="canvas-toolbar glass">
        <div className="min-w-0">
          <h1 className="truncate">{agent?.name ?? 'Agent Canvas'}</h1>
          <p className="muted truncate">当前版本：{version ? `v${version.version_no}` : '未保存草稿'}</p>
        </div>
        <div className="canvas-tools">
          <Segmented value={mode} onChange={setMode} options={[{ value: 'config', label: '配置' }, { value: 'debug', label: '调试' }, { value: 'dsl', label: 'DSL' }]} />
          <Button onClick={() => void saveFlow()}>
            <Save size={16} />
            保存
          </Button>
          <Button tone="primary" onClick={() => void publishFlow()}>
            <Sparkles size={16} />
            发布
          </Button>
        </div>
      </header>

      <div
        ref={canvasBodyRef}
        className={`canvas-body ${sidePanelOpen ? 'side-panel-open' : 'side-panel-closed'} ${paletteWidth < MIN_PALETTE_WIDTH ? 'palette-collapsed' : ''}`}
        style={{
          '--palette-width': `${paletteWidth}px`,
          '--config-width': `${configWidth}px`,
        } as CSSProperties}
      >
        <aside className="node-palette">
          {(Object.keys(nodeMeta) as NodeType[]).map((type) => {
            const Icon = nodeMeta[type].icon;
            return (
              <button className="palette-item" key={type} type="button" onClick={() => addNode(type)}>
                <Icon size={16} />
                <span className="truncate">{nodeMeta[type].label}</span>
              </button>
            );
          })}
        </aside>
        <div
          className="canvas-resizer canvas-resizer-left"
          role="separator"
          aria-orientation="vertical"
          aria-label="拖动调整模块栏宽度"
          onPointerDown={(event) => {
            event.preventDefault();
            startResize('palette', event.clientX);
          }}
        />

        <section className="flow-surface">
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onConnect={onConnect}
            onNodeClick={handleNodeClick}
            fitView
            proOptions={{ hideAttribution: true }}
          >
            <Background color="rgba(0, 113, 227, 0.22)" gap={64} size={1} variant={BackgroundVariant.Lines} />
            <Controls />
          </ReactFlow>
          {nodes.length === 0 ? (
            <div className="flow-empty-state">
              <EmptyState icon={<Bot size={24} />} title="画布为空" />
            </div>
          ) : null}
        </section>
        <div
          className="canvas-resizer canvas-resizer-right"
          role="separator"
          aria-orientation="vertical"
          aria-label="拖动调整配置面板宽度，拖到较窄时自动收起"
          onPointerDown={(event) => {
            event.preventDefault();
            startResize('panel', event.clientX);
          }}
        />

        <aside className="config-panel" aria-hidden={!sidePanelOpen}>
          {mode === 'config' && selected && config ? (
            <Panel title={selected.data.label} eyebrow={selected.id}>
              <Field label="显示名称">
                <TextInput value={selected.data.label} onChange={(event) => setNodes((current) => current.map((node) => node.id === selectedId ? { ...node, data: { ...node.data, label: event.target.value } } : node))} />
              </Field>
              {selected.data.nodeType === 'knowledge_retrieval' && (
                <>
                  <Field label="知识库">
                    <Select
                      value={Array.isArray(config.kb_ids) ? String(config.kb_ids[0] ?? '') : ''}
                      onChange={(event) => updateSelectedConfig({ kb_ids: event.target.value ? [Number(event.target.value)] : [] })}
                    >
                      <option value="">选择知识库</option>
                      {knowledgeBases.map((kb) => <option key={kb.id} value={kb.id}>{kb.name}</option>)}
                    </Select>
                  </Field>
                  <Field label="查询模板">
                    <TextInput value={String(config.query ?? '')} onChange={(event) => updateSelectedConfig({ query: event.target.value })} />
                  </Field>
                  <Field label="Top K">
                    <TextInput type="number" min={1} max={20} value={Number(config.top_k ?? 5)} onChange={(event) => updateSelectedConfig({ top_k: Number(event.target.value) })} />
                  </Field>
                  <Field label="检索模式">
                    <Select value={String(config.mode ?? 'keyword')} onChange={(event) => updateSelectedConfig({ mode: event.target.value })}>
                      <option value="keyword">Keyword</option>
                      <option value="vector">Vector</option>
                      <option value="hybrid">Hybrid</option>
                    </Select>
                  </Field>
                </>
              )}
              {selected.data.nodeType === 'prompt' && (
                <Field label="Prompt 模板" hint="支持 {{sys.query}} 和 {{node_id.field}}">
                  <TextArea value={String(config.template ?? '')} onChange={(event) => updateSelectedConfig({ template: event.target.value })} />
                </Field>
              )}
              {selected.data.nodeType === 'llm' && (
                <>
                  <Field label="Provider">
                    <Select value={Number(config.provider_id ?? 0)} onChange={(event) => updateSelectedConfig({ provider_id: Number(event.target.value) })}>
                      <option value={0}>选择 Provider</option>
                      {providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}
                    </Select>
                  </Field>
                  <Field label="模型">
                    <TextInput value={String(config.model ?? '')} onChange={(event) => updateSelectedConfig({ model: event.target.value })} placeholder="留空使用默认模型" />
                  </Field>
                  <Field label="Temperature">
                    <TextInput type="number" min={0} max={2} step={0.1} value={Number(config.temperature ?? 0.7)} onChange={(event) => updateSelectedConfig({ temperature: Number(event.target.value) })} />
                  </Field>
                </>
              )}
              {selected.data.nodeType === 'agent_loop' && (
                <>
                  <Field label="Provider">
                    <Select value={Number(config.provider_id ?? 0)} onChange={(event) => updateSelectedConfig({ provider_id: Number(event.target.value) })}>
                      <option value={0}>选择 Provider</option>
                      {providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}
                    </Select>
                  </Field>
                  <Field label="模型">
                    <TextInput value={String(config.model ?? '')} onChange={(event) => updateSelectedConfig({ model: event.target.value })} placeholder="留空使用默认模型" />
                  </Field>
                  <Field label="System Prompt">
                    <TextArea value={String(config.system_prompt ?? '')} onChange={(event) => updateSelectedConfig({ system_prompt: event.target.value })} />
                  </Field>
                  <Field label="任务模板" hint="支持 {{sys.query}} 和 {{node_id.field}}">
                    <TextArea value={String(config.task_template ?? '')} onChange={(event) => updateSelectedConfig({ task_template: event.target.value })} />
                  </Field>
                  <Field label="可用工具">
                    <Select
                      multiple
                      value={Array.isArray(config.tool_ids) ? config.tool_ids.map(String) : []}
                      onChange={(event) => updateSelectedConfig({ tool_ids: Array.from(event.target.selectedOptions).map((option) => Number(option.value)) })}
                    >
                      {tools.map((tool) => <option key={tool.id} value={tool.id}>{tool.name}</option>)}
                    </Select>
                  </Field>
                  <Field label="知识库工具">
                    <Select
                      multiple
                      value={Array.isArray(config.knowledge_ids) ? config.knowledge_ids.map(String) : []}
                      onChange={(event) => updateSelectedConfig({ knowledge_ids: Array.from(event.target.selectedOptions).map((option) => Number(option.value)) })}
                    >
                      {knowledgeBases.map((kb) => <option key={kb.id} value={kb.id}>{kb.name}</option>)}
                    </Select>
                  </Field>
                  <Field label="知识库 Top K">
                    <TextInput type="number" min={1} max={20} value={Number(config.knowledge_top_k ?? 5)} onChange={(event) => updateSelectedConfig({ knowledge_top_k: Number(event.target.value) })} />
                  </Field>
                  <Field label="知识库模式">
                    <Select value={String(config.knowledge_mode ?? 'keyword')} onChange={(event) => updateSelectedConfig({ knowledge_mode: event.target.value })}>
                      <option value="keyword">Keyword</option>
                      <option value="vector">Vector</option>
                      <option value="hybrid">Hybrid</option>
                    </Select>
                  </Field>
                  <Field label="可调用 Agent">
                    <Select
                      multiple
                      value={Array.isArray(config.call_agent_ids) ? config.call_agent_ids.map(String) : []}
                      onChange={(event) => updateSelectedConfig({ call_agent_ids: Array.from(event.target.selectedOptions).map((option) => Number(option.value)) })}
                    >
                      {callableAgents.map((agent) => <option key={agent.id} value={agent.id}>{agent.name}</option>)}
                    </Select>
                  </Field>
                  <Field label="Agent 调用深度">
                    <TextInput type="number" min={1} max={5} value={Number(config.max_agent_call_depth ?? 3)} onChange={(event) => updateSelectedConfig({ max_agent_call_depth: Number(event.target.value) })} />
                  </Field>
                  <Field label="代码执行工具">
                    <Select value={config.code_execution_enabled ? 'enabled' : 'disabled'} onChange={(event) => updateSelectedConfig({ code_execution_enabled: event.target.value === 'enabled' })}>
                      <option value="disabled">Disabled</option>
                      <option value="enabled">Enabled</option>
                    </Select>
                  </Field>
                  <Field label="最大轮次">
                    <TextInput type="number" min={1} max={50} value={Number(config.max_iterations ?? 8)} onChange={(event) => updateSelectedConfig({ max_iterations: Number(event.target.value) })} />
                  </Field>
                  <Field label="最大工具调用">
                    <TextInput type="number" min={1} max={100} value={Number(config.max_tool_calls ?? 16)} onChange={(event) => updateSelectedConfig({ max_tool_calls: Number(event.target.value) })} />
                  </Field>
                  <Field label="超时毫秒">
                    <TextInput type="number" min={1000} max={600000} step={1000} value={Number(config.max_execution_time_ms ?? 120000)} onChange={(event) => updateSelectedConfig({ max_execution_time_ms: Number(event.target.value) })} />
                  </Field>
                  <Field label="Temperature">
                    <TextInput type="number" min={0} max={2} step={0.1} value={Number(config.temperature ?? 0.2)} onChange={(event) => updateSelectedConfig({ temperature: Number(event.target.value) })} />
                  </Field>
                  <Field label="输出模式">
                    <Select value={String(config.output_mode ?? 'final_answer')} onChange={(event) => updateSelectedConfig({ output_mode: event.target.value, return_intermediate_steps: event.target.value === 'full' })}>
                      <option value="final_answer">Final Answer</option>
                      <option value="full">Full Trace</option>
                    </Select>
                  </Field>
                </>
              )}
              {selected.data.nodeType === 'agent_call' && (
                <>
                  <Field label="目标 Agent">
                    <Select value={Number(config.agent_id ?? 0)} onChange={(event) => updateSelectedConfig({ agent_id: Number(event.target.value) })}>
                      <option value={0}>选择 Agent</option>
                      {callableAgents.map((agent) => <option key={agent.id} value={agent.id}>{agent.name}</option>)}
                    </Select>
                  </Field>
                  <Field label="Flow Version ID">
                    <TextInput type="number" min={0} value={Number(config.flow_version_id ?? 0)} onChange={(event) => updateSelectedConfig({ flow_version_id: Number(event.target.value) })} />
                  </Field>
                  <Field label="输入 JSON" hint="留空 flow_version_id 会使用目标 Agent 当前发布版本">
                    <TextArea value={prettyJson(config.input ?? { query: '{{sys.query}}' })} onChange={(event) => updateSelectedJSON('input', event.target.value)} />
                  </Field>
                  <Field label="最大调用深度">
                    <TextInput type="number" min={1} max={5} value={Number(config.max_depth ?? 3)} onChange={(event) => updateSelectedConfig({ max_depth: Number(event.target.value) })} />
                  </Field>
                </>
              )}
              {selected.data.nodeType === 'code_sandbox' && (
                <>
                  <Field label="语言">
                    <Select value={String(config.language ?? 'python')} onChange={(event) => updateSelectedConfig({ language: event.target.value })}>
                      <option value="python">Python</option>
                    </Select>
                  </Field>
                  <Field label="代码">
                    <TextArea value={String(config.code ?? '')} onChange={(event) => updateSelectedConfig({ code: event.target.value })} />
                  </Field>
                  <Field label="超时毫秒">
                    <TextInput type="number" min={1000} max={30000} step={1000} value={Number(config.timeout_ms ?? 5000)} onChange={(event) => updateSelectedConfig({ timeout_ms: Number(event.target.value) })} />
                  </Field>
                  <Field label="最大输出字节">
                    <TextInput type="number" min={1024} max={1048576} value={Number(config.max_output_bytes ?? 65536)} onChange={(event) => updateSelectedConfig({ max_output_bytes: Number(event.target.value) })} />
                  </Field>
                  <Field label="内存 MB">
                    <TextInput type="number" min={32} max={512} value={Number(config.memory_limit_mb ?? 128)} onChange={(event) => updateSelectedConfig({ memory_limit_mb: Number(event.target.value) })} />
                  </Field>
                  <Field label="网络">
                    <Select value={config.network_enabled ? 'enabled' : 'disabled'} onChange={(event) => updateSelectedConfig({ network_enabled: event.target.value === 'enabled' })}>
                      <option value="disabled">Disabled</option>
                      <option value="enabled">Enabled</option>
                    </Select>
                  </Field>
                </>
              )}
              {selected.data.nodeType === 'message' && (
                <Field label="输出内容">
                  <TextArea value={String(config.content ?? '')} onChange={(event) => updateSelectedConfig({ content: event.target.value })} />
                </Field>
              )}
              {selected.data.nodeType === 'memory_read' && (
                <>
                  <Field label="记忆类型">
                    <TextInput value={Array.isArray(config.memory_types) ? config.memory_types.join(',') : ''} onChange={(event) => updateSelectedConfig({ memory_types: event.target.value.split(',').map((item) => item.trim()).filter(Boolean) })} />
                  </Field>
                  <Field label="Limit">
                    <TextInput type="number" min={1} max={20} value={Number(config.limit ?? 5)} onChange={(event) => updateSelectedConfig({ limit: Number(event.target.value) })} />
                  </Field>
                </>
              )}
              {selected.data.nodeType === 'memory_write' && (
                <>
                  <Field label="记忆类型">
                    <Select value={String(config.memory_type ?? 'summary_memory')} onChange={(event) => updateSelectedConfig({ memory_type: event.target.value })}>
                      <option value="profile_memory">profile_memory</option>
                      <option value="summary_memory">summary_memory</option>
                      <option value="episodic_memory">episodic_memory</option>
                      <option value="task_memory">task_memory</option>
                    </Select>
                  </Field>
                  <Field label="标题"><TextInput value={String(config.title ?? '')} onChange={(event) => updateSelectedConfig({ title: event.target.value })} /></Field>
                  <Field label="内容模板"><TextArea value={String(config.content ?? '')} onChange={(event) => updateSelectedConfig({ content: event.target.value })} /></Field>
                  <Field label="Importance">
                    <TextInput type="number" min={0} max={1} step={0.1} value={Number(config.importance ?? 0.5)} onChange={(event) => updateSelectedConfig({ importance: Number(event.target.value) })} />
                  </Field>
                </>
              )}
              {selected.data.nodeType === 'http_tool' && (
                <>
                  <Field label="Tool">
                    <Select value={Number(config.tool_id ?? 0)} onChange={(event) => updateSelectedConfig({ tool_id: Number(event.target.value) })}>
                      <option value={0}>选择 HTTP Tool</option>
                      {tools.map((tool) => <option key={tool.id} value={tool.id}>{tool.name}</option>)}
                    </Select>
                  </Field>
                  <Field label="输入 JSON" hint="支持 {{sys.query}} 和 {{node_id.field}}">
                    <TextArea value={prettyJson(config.input ?? {})} onChange={(event) => updateSelectedJSON('input', event.target.value)} />
                  </Field>
                </>
              )}
              {selected.data.nodeType === 'switch' && (
                <Field label="Conditions JSON">
                  <TextArea value={prettyJson(config.conditions ?? [])} onChange={(event) => updateSelectedJSON('conditions', event.target.value)} />
                </Field>
              )}
              {selected.data.nodeType === 'json_output' && (
                <>
                  <Field label="Value 模板"><TextArea value={String(config.value ?? '')} onChange={(event) => updateSelectedConfig({ value: event.target.value })} /></Field>
                  <Field label="Schema JSON"><TextArea value={prettyJson(config.schema ?? { type: 'object' })} onChange={(event) => updateSelectedJSON('schema', event.target.value)} /></Field>
                </>
              )}
              {selected.data.nodeType === 'guardrail' && (
                <>
                  <Field label="Source 模板"><TextArea value={String(config.source ?? '')} onChange={(event) => updateSelectedConfig({ source: event.target.value })} /></Field>
                  <Field label="最大长度"><TextInput type="number" min={0} value={Number(config.max_length ?? 4000)} onChange={(event) => updateSelectedConfig({ max_length: Number(event.target.value) })} /></Field>
                  <Field label="敏感词">
                    <TextInput value={Array.isArray(config.banned_terms) ? config.banned_terms.join(',') : ''} onChange={(event) => updateSelectedConfig({ banned_terms: event.target.value.split(',').map((item) => item.trim()).filter(Boolean) })} />
                  </Field>
                  <Field label="输出要求">
                    <Select value={config.require_json ? 'json' : config.require_citation ? 'citation' : 'none'} onChange={(event) => updateSelectedConfig({ require_json: event.target.value === 'json', require_citation: event.target.value === 'citation' })}>
                      <option value="none">无</option>
                      <option value="citation">需要引用</option>
                      <option value="json">需要 JSON</option>
                    </Select>
                  </Field>
                </>
              )}
              {selected.data.nodeType === 'begin' && (
                <EmptyState icon={<Workflow size={22} />} title="Begin 会透传运行输入" description="调试台默认使用 query 字段，后续节点可通过 {{sys.query}} 引用。" />
              )}
              {error ? <p className="error-text">{error}</p> : null}
            </Panel>
          ) : null}

          {mode === 'debug' && (
            <Panel title="调试台" eyebrow={debugRunMode === 'complete' ? '完整结果' : '运行事件'}>
              <Field label="运行输入 JSON">
                <TextArea value={runInput} onChange={(event) => setRunInput(event.target.value)} />
              </Field>
              <div className="debug-run-controls">
                <Segmented value={debugRunMode} onChange={setDebugRunMode} options={debugRunModeOptions} />
                <Button tone="primary" disabled={runningDebugMode !== null} onClick={() => void runDebug()}>
                  <Play size={16} />
                  {runningDebugMode ? '运行中' : '运行'}
                </Button>
              </div>
              <p className="muted">{debugRunMode === 'complete' ? '完整运行会等待整套流程结束，只显示最终返回结果。' : '流式运行会实时展示每个运行事件和 LLM delta。'}</p>
              {error ? <p className="error-text">{error}</p> : null}
              <div className="stack">
                {debugRunMode === 'stream' ? events.map((event, index) => (
                  <div className="card" key={`${event.type}-${index}`}>
                    <div className="card-title">
                      <h3 className="truncate">{event.type}</h3>
                      <StatusBadge tone={event.type.includes('failed') ? 'bad' : 'info'}>{event.node_type || 'workflow'}</StatusBadge>
                    </div>
                    <pre className="code-box">{prettyJson(event.payload ?? {})}</pre>
                  </div>
                )) : null}
                {runOutput ? (
                  <div className="card debug-result-card">
                    <div className="card-title">
                      <h3>{debugRunMode === 'complete' ? '完整运行结果' : '最终输出'}</h3>
                      <StatusBadge tone="good">output</StatusBadge>
                    </div>
                    <pre className="code-box debug-result-content">{runOutputText(runOutput)}</pre>
                  </div>
                ) : null}
                {runSteps.length > 0 ? (
                  <div className="card">
                    <div className="card-title"><h3>Agent Steps</h3><StatusBadge tone="info">{runSteps.length}</StatusBadge></div>
                    <pre className="code-box">{prettyJson(runSteps)}</pre>
                  </div>
                ) : null}
                {memoryLogs.length > 0 ? (
                  <div className="card">
                    <div className="card-title"><h3>记忆写入</h3><StatusBadge tone="info">{memoryLogs.length}</StatusBadge></div>
                    <pre className="code-box">{prettyJson(memoryLogs)}</pre>
                  </div>
                ) : null}
                {toolInvocations.length > 0 ? (
                  <div className="card">
                    <div className="card-title"><h3>工具调用</h3><StatusBadge tone="info">{toolInvocations.length}</StatusBadge></div>
                    <pre className="code-box">{prettyJson(toolInvocations)}</pre>
                  </div>
                ) : null}
              </div>
            </Panel>
          )}

          {mode === 'dsl' && (
            <Panel title="流程结构" eyebrow="定义">
              <pre className="code-box">{prettyJson(dsl)}</pre>
            </Panel>
          )}
        </aside>
      </div>

      <Toast message={message} tone="good" />
    </div>
  );
}
