import { type CSSProperties, type MouseEvent as ReactMouseEvent, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useParams } from 'react-router-dom';
import {
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  ReactFlow,
  addEdge,
  applyEdgeChanges,
  applyNodeChanges,
  type Connection,
  type Edge,
  type EdgeChange,
  type NodeChange,
  type ReactFlowInstance,
} from '@xyflow/react';
import {
  Bot,
  Focus,
  History,
  Maximize2,
  Minimize2,
  MessageSquareText,
  Play,
  Plus,
  Save,
  Send,
  Settings,
  Sparkles,
  RotateCcw,
  Workflow as WorkflowIcon,
  X,
} from 'lucide-react';
import { workflowApi, resourceSummaryApi, settingsApi } from '../api/resources';
import { EngineeringAscii } from '../components/editorial';
import { Button, EmptyState, Field, IconButton, Panel, Segmented, Select, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
import type { AgentReflection, ReflectionStatus, Workflow, WorkflowProfile, ApprovalRequest, Conversation, EvalCase, EvalDataset, EvalResult, EvalRun, EvalTrend, FlowVersion, KnowledgeBase, MCPServer, MemoryWriteLog, Message, ModelProvider, Run, RunStep, RunTrace, Skill, ToolDefinition, ToolInvocation, ToolPack, WorkflowMessageResponse } from '../types/api';
import type { NodeType } from '../types/flow';
import type { RuntimeEvent } from '../types/events';
import { formatDate, friendlyErrorMessage, parseJsonObject, prettyJson } from '../utils/format';
import { defaultConfig, DEFAULT_REFLECTION_POLICY, isAgentNodeType, isStaticAgentCallNodeType, nodeMeta, numberArray, paletteNodeTypes } from './canvas/config';
import { nodeTypes } from './canvas/canvas/node-types';
import { canvasDSLKey, defaultNodes, fromDSL, normalizeDSL, toDSL } from './canvas/canvas/hooks/useDslBridge';
import { validateLocal } from './canvas/canvas/hooks/useCanvasValidation';
import type { CanvasNode, NodeRunStatus } from './canvas/types';
import { FormSheet } from './canvas/form-sheet/FormSheet';
import { AgentLoopForm } from './canvas/forms/agent-loop/AgentLoopForm';
import { SwitchConditionsEditor } from './canvas/forms/SwitchConditionsEditor';
import { AgentTraceTimeline } from './canvas/debug/AgentTraceTimeline';
import { ContextRulesTrace } from './canvas/debug/ContextRulesTrace';
import { ToolInvocationList } from './canvas/debug/ToolInvocationList';
import { ApprovalQueue } from './canvas/debug/ApprovalQueue';

type DebugRunMode = 'stream' | 'complete';
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

function objectRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {};
}

function evalSummaryMetrics(run: EvalRun | null): Record<string, unknown> {
  const summary = objectRecord(run?.summary_json);
  return objectRecord(summary.metrics);
}

function latestEvalRunFrom(items: EvalRun[]): EvalRun | null {
  if (items.length === 0) return null;
  const sorted = [...items].sort((a, b) => {
    const startedDelta = Date.parse(a.started_at) - Date.parse(b.started_at);
    if (startedDelta !== 0) return startedDelta;
    return a.id - b.id;
  });
  return sorted[sorted.length - 1] ?? null;
}

function metricNumber(metrics: Record<string, unknown>, key: string): number | null {
  const value = metrics[key];
  return typeof value === 'number' && Number.isFinite(value) ? value : null;
}

function metricPercent(metrics: Record<string, unknown>, key: string): string {
  const value = metricNumber(metrics, key);
  return value === null ? '-' : `${(value * 100).toFixed(1)}%`;
}

function metricFixed(metrics: Record<string, unknown>, key: string, suffix = ''): string {
  const value = metricNumber(metrics, key);
  return value === null ? '-' : `${value.toFixed(1)}${suffix}`;
}

export function CanvasPage() {
  const { id } = useParams();
  const workflowId = Number(id);
  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [profile, setProfile] = useState<WorkflowProfile | null>(null);
  const [version, setVersion] = useState<FlowVersion | null>(null);
  const [flowVersions, setFlowVersions] = useState<FlowVersion[]>([]);
  const [versionHistoryOpen, setVersionHistoryOpen] = useState(false);
  const [restoredFromVersion, setRestoredFromVersion] = useState<number | null>(null);
  const [nodes, setNodes] = useState<CanvasNode[]>([]);
  const [edges, setEdges] = useState<Edge[]>([]);
  const [selectedId, setSelectedId] = useState<string>('begin');
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [callableWorkflows, setCallableWorkflows] = useState<Workflow[]>([]);
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([]);
  const [tools, setTools] = useState<ToolDefinition[]>([]);
  const [skills, setSkills] = useState<Skill[]>([]);
  const [toolPacks, setToolPacks] = useState<ToolPack[]>([]);
  const [mcpServers, setMcpServers] = useState<MCPServer[]>([]);
  const [mode, setMode] = useState<'config' | 'profile' | 'reflections' | 'approvals' | 'eval' | 'chat' | 'debug' | 'dsl'>('config');
  const [paletteWidth, setPaletteWidth] = useState(DEFAULT_PALETTE_WIDTH);
  const [configWidth, setConfigWidth] = useState(DEFAULT_PANEL_WIDTH);
  const [sidePanelOpen, setSidePanelOpen] = useState(true);
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance<CanvasNode, Edge> | null>(null);
  const [canvasFullscreen, setCanvasFullscreen] = useState(false);
  const [pendingConnectId, setPendingConnectId] = useState<string>('');
  const [runInput, setRunInput] = useState('{\n  "query": "请总结知识库内容"\n}');
  const [debugRunMode, setDebugRunMode] = useState<DebugRunMode>('stream');
  const [runningDebugMode, setRunningDebugMode] = useState<DebugRunMode | null>(null);
  const [debugRunId, setDebugRunId] = useState<number | null>(null);
  const [events, setEvents] = useState<RuntimeEvent[]>([]);
  const [runOutput, setRunOutput] = useState<Record<string, unknown> | null>(null);
  const [runSteps, setRunSteps] = useState<RunStep[]>([]);
  const [childRuns, setChildRuns] = useState<Run[]>([]);
  const [memoryLogs, setMemoryLogs] = useState<MemoryWriteLog[]>([]);
  const [toolInvocations, setToolInvocations] = useState<ToolInvocation[]>([]);
  const [runTraceSummary, setRunTraceSummary] = useState<Record<string, unknown> | null>(null);
  const [reflections, setReflections] = useState<AgentReflection[]>([]);
  const [reflectionStatusFilter, setReflectionStatusFilter] = useState<ReflectionStatus | ''>('');
  const [reflectionFeedback, setReflectionFeedback] = useState<Record<number, 'helpful' | 'harmful'>>({});
  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [conversationId, setConversationId] = useState(0);
  const [conversationMessages, setConversationMessages] = useState<Message[]>([]);
  const [chatQuestion, setChatQuestion] = useState('');
  const [pendingChatQuestion, setPendingChatQuestion] = useState('');
  const [pendingChatAnswer, setPendingChatAnswer] = useState('');
  const [chatStreaming, setChatStreaming] = useState(false);
  const [approvalRequests, setApprovalRequests] = useState<ApprovalRequest[]>([]);
  const [evalDatasets, setEvalDatasets] = useState<EvalDataset[]>([]);
  const [evalCases, setEvalCases] = useState<EvalCase[]>([]);
  const [evalDatasetName, setEvalDatasetName] = useState('');
  const [selectedEvalDatasetId, setSelectedEvalDatasetId] = useState(0);
  const [evalCaseName, setEvalCaseName] = useState('');
  const [evalCaseInput, setEvalCaseInput] = useState('{\n  "query": "请回答一个简单问题"\n}');
  const [evalCaseExpected, setEvalCaseExpected] = useState('{\n  "contains": []\n}');
  const [evalRuns, setEvalRuns] = useState<EvalRun[]>([]);
  const [latestEvalRun, setLatestEvalRun] = useState<EvalRun | null>(null);
  const [evalTrend, setEvalTrend] = useState<EvalTrend | null>(null);
  const [latestEvalResults, setLatestEvalResults] = useState<EvalResult[]>([]);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');
  const canvasBodyRef = useRef<HTMLDivElement | null>(null);
  const canvasPageRef = useRef<HTMLDivElement | null>(null);
  const versionControlRef = useRef<HTMLDivElement | null>(null);
  const resizeFrameRef = useRef<number | null>(null);
  const resizeClientXRef = useRef(0);
  // 进行中的调试运行控制器：组件卸载或重新运行时取消，避免“卸载后 setState”。
  const runAbortRef = useRef<AbortController | null>(null);
  const chatAbortRef = useRef<AbortController | null>(null);

  const selected = useMemo(() => nodes.find((node) => node.id === selectedId) ?? null, [nodes, selectedId]);
  const dsl = useMemo(() => toDSL(workflowId, nodes, edges), [workflowId, nodes, edges]);
  const currentCanvasKey = useMemo(() => canvasDSLKey(dsl), [dsl]);
  const savedCanvasKey = useMemo(() => canvasDSLKey(version?.dsl_json), [version]);
  const hasCanvasChanges = !version || currentCanvasKey !== savedCanvasKey;
  const nodesWithRunStatus = useMemo(() => {
    const statusByNode = new Map<string, NodeRunStatus>();
    for (const event of events) {
      if (!event.node_id) continue;
      if (event.type.includes('failed')) statusByNode.set(event.node_id, 'failed');
      else if (event.type.includes('finished')) statusByNode.set(event.node_id, 'succeeded');
      else if (event.type.includes('started') || event.type.includes('delta')) statusByNode.set(event.node_id, 'running');
    }
    for (const approval of approvalRequests) {
      if (approval.status === 'pending' && approval.node_id) statusByNode.set(approval.node_id, 'waiting_human');
    }
    if (!runningDebugMode && runOutput) {
      for (const [nodeId, status] of statusByNode) {
        if (status === 'running') statusByNode.set(nodeId, 'succeeded');
      }
    }
    return nodes.map((node) => ({
      ...node,
      data: { ...node.data, runStatus: statusByNode.get(node.id) ?? 'idle' },
    }));
  }, [approvalRequests, events, nodes, runOutput, runningDebugMode]);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      const [workflowResp, profileResp, reflectionResp, workflowsResp, providersResp, kbResp, toolResp, skillResp, toolPackResp, mcpResp, approvalResp, evalDatasetResp, versionResp, conversationResp] = await Promise.all([
        workflowApi.get(workflowId),
        workflowApi.getProfile(workflowId),
        workflowApi.listReflections(workflowId),
        resourceSummaryApi.list('workflows', { limit: 100 }),
        settingsApi.providers.list(),
        resourceSummaryApi.list('knowledge-bases', { limit: 100 }),
        resourceSummaryApi.list('http-tools', { limit: 100 }),
        resourceSummaryApi.list('skills', { limit: 100 }),
        settingsApi.tools.listPacks(),
        settingsApi.tools.listMCPServers(),
        workflowApi.listApprovalRequests('pending'),
        workflowApi.listEvalDatasets(workflowId),
        workflowApi.listFlowVersions(workflowId),
        workflowApi.listConversations(workflowId),
      ]);
      // 快速切换 workflow 时旧请求可能晚返回，确认仍是当前 workflow 再写入。
      if (cancelled) return;
      setWorkflow(workflowResp);
      setProfile({
        ...profileResp,
        mode: profileResp.mode === 'plan_execute' || profileResp.planning_enabled ? 'plan_execute' : 'react',
      });
      setReflections(reflectionResp);
      setCallableWorkflows(workflowsResp.items.filter((item) => item.id !== workflowId).map((item) => ({
        id: item.id, owner_id: 0, name: item.name, description: item.description ?? '', avatar_url: '',
        current_version_id: item.current_version_id ?? null, status: item.status ?? 1,
        created_at: item.updated_at, updated_at: item.updated_at,
      })));
      setProviders(providersResp);
      setKnowledgeBases(kbResp.items.map((item) => ({ id: item.id, name: item.name, status: item.status ?? 1 } as KnowledgeBase)));
      setTools(toolResp.items.map((item) => ({ id: item.id, name: item.name, tool_type: item.resource_type ?? 'http', status: item.status ?? 1 } as ToolDefinition)));
      setSkills(skillResp.items.map((item) => ({ id: item.id, name: item.name, status: item.status ?? 1 } as Skill)));
      setToolPacks(toolPackResp);
      setMcpServers(mcpResp);
      setApprovalRequests(approvalResp.filter((item) => item.workflow_id === workflowId));
      setEvalDatasets(evalDatasetResp);
      setSelectedEvalDatasetId(evalDatasetResp[0]?.id ?? 0);
      setConversations(conversationResp);
      setConversationId(conversationResp[0]?.id ?? 0);
      const sortedVersions = [...versionResp].sort((left, right) => right.version_no - left.version_no);
      setFlowVersions(sortedVersions);
      setRestoredFromVersion(null);
      const flow = sortedVersions[0];
      if (flow) {
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
    if (workflowId > 0) {
      void load().catch((err) => {
        if (!cancelled) setError(friendlyErrorMessage(err, '加载画布失败'));
      });
    }
    return () => {
      cancelled = true;
    };
  }, [workflowId]);

  async function refreshApprovals() {
    const items = await workflowApi.listApprovalRequests('pending');
    setApprovalRequests(items.filter((item) => item.workflow_id === workflowId));
  }

  async function refreshReflections(status: ReflectionStatus | '' = reflectionStatusFilter) {
    try {
      setReflections(await workflowApi.listReflections(workflowId, status || undefined));
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, '刷新 Reflection 失败'));
    }
  }

  async function updateReflectionStatus(
    reflectionId: number,
    status: 'active' | 'validated' | 'disputed' | 'archived',
  ) {
    try {
      await workflowApi.setReflectionStatus(workflowId, reflectionId, status);
      await refreshReflections();
      setMessage(`Reflection #${reflectionId} 已更新为 ${status}`);
    } catch (err) {
      setError(friendlyErrorMessage(err, '更新 Reflection 状态失败'));
    }
  }

  async function feedbackReflection(reflectionId: number, verdict: 'helpful' | 'harmful') {
    if (!debugRunId) {
      setError('当前没有可提交反馈的 Run');
      return;
    }
    try {
      await workflowApi.feedbackReflection(
        debugRunId,
        reflectionId,
        verdict,
        `Marked ${verdict} from AgentCanvas trace`,
      );
      setReflectionFeedback((current) => ({ ...current, [reflectionId]: verdict }));
      setMessage(`Reflection #${reflectionId} 已标记为 ${verdict}`);
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, '提交 Reflection 反馈失败'));
    }
  }

  async function refreshEvalDatasets() {
    const items = await workflowApi.listEvalDatasets(workflowId);
    setEvalDatasets(items);
    setSelectedEvalDatasetId((current) => current || items[0]?.id || 0);
  }

  async function refreshEvalCases(datasetId = selectedEvalDatasetId) {
    if (!datasetId) {
      setEvalCases([]);
      return;
    }
    setEvalCases(await workflowApi.listEvalCases(datasetId));
  }

  async function refreshEvalRuns(datasetId = selectedEvalDatasetId) {
    if (!datasetId) {
      setEvalRuns([]);
      setLatestEvalRun(null);
      setEvalTrend(null);
      return;
    }
    const [items, trend] = await Promise.all([
      workflowApi.listEvalRuns(datasetId),
      workflowApi.getEvalTrend(datasetId),
    ]);
    const latest = latestEvalRunFrom(items);
    setEvalRuns(items);
    setLatestEvalRun(latest);
    setEvalTrend(trend);
    setLatestEvalResults(latest ? await workflowApi.listEvalResults(latest.id) : []);
  }

  useEffect(() => {
    void refreshEvalCases().catch((err) => setError(friendlyErrorMessage(err, '加载 Eval Case 失败')));
    void refreshEvalRuns().catch((err) => setError(friendlyErrorMessage(err, '加载 Eval Run 失败')));
  }, [selectedEvalDatasetId]);

  // 组件卸载时取消进行中的调试流。
  useEffect(() => () => {
    runAbortRef.current?.abort();
    chatAbortRef.current?.abort();
  }, []);

  useEffect(() => {
    if (!conversationId) {
      setConversationMessages([]);
      return;
    }
    let cancelled = false;
    workflowApi.listConversationMessages(workflowId, conversationId)
      .then((items) => { if (!cancelled) setConversationMessages(items); })
      .catch((err) => { if (!cancelled) setError(friendlyErrorMessage(err, '加载 Workflow 会话失败')); });
    return () => { cancelled = true; };
  }, [conversationId, workflowId]);

  const onNodesChange = useCallback((changes: NodeChange<CanvasNode>[]) => setNodes((current) => applyNodeChanges(changes, current)), []);
  const onEdgesChange = useCallback((changes: EdgeChange[]) => setEdges((current) => applyEdgeChanges(changes, current)), []);
  const onConnect = useCallback((connection: Connection) => {
    if (!connection.source || !connection.target || connection.source === connection.target) return;
    setEdges((current) => addEdge(connection, current));
  }, []);

  const startResize = useCallback((side: 'palette' | 'panel', startX: number) => {
    const body = canvasBodyRef.current;
    if (!body) return;
    const element = body;
    const rect = body.getBoundingClientRect();
    const startsFromClosedPanel = side === 'panel' && !sidePanelOpen;
    let finalPaletteWidth = paletteWidth;
    let finalConfigWidth = configWidth;
    let finalPanelOpen = sidePanelOpen;

    function applyResize(clientX: number) {
      if (side === 'palette') {
        const next = clientX - rect.left;
        const width = Math.min(MAX_PALETTE_WIDTH, Math.max(COLLAPSED_PALETTE_WIDTH, next));
        finalPaletteWidth = width;
        element.style.setProperty('--palette-width', `${width}px`);
        element.classList.toggle('palette-collapsed', width < MIN_PALETTE_WIDTH);
        return;
      }

      const next = startsFromClosedPanel ? MIN_PANEL_WIDTH + (startX - clientX) : rect.right - clientX;
      if (next < PANEL_COLLAPSE_THRESHOLD) {
        finalPanelOpen = false;
        element.classList.remove('side-panel-open');
        element.classList.add('side-panel-closed');
        element.style.setProperty('--config-width', '0px');
      } else {
        const width = Math.min(MAX_PANEL_WIDTH, Math.max(MIN_PANEL_WIDTH, next));
        finalPanelOpen = true;
        finalConfigWidth = width;
        element.classList.add('side-panel-open');
        element.classList.remove('side-panel-closed');
        element.style.setProperty('--config-width', `${width}px`);
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
      if (side === 'palette') setPaletteWidth(finalPaletteWidth);
      else {
        setConfigWidth(finalConfigWidth);
        setSidePanelOpen(finalPanelOpen);
      }
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      document.body.classList.remove('is-resizing-panels');
    }

    document.body.classList.add('is-resizing-panels');
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    resizeClientXRef.current = startX;
    applyResize(startX);
  }, [configWidth, paletteWidth, sidePanelOpen]);

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

  function addReferencedNode(kind: 'http_tool' | 'mcp_tool' | 'knowledge_retrieval' | 'agent_loop', refId: number) {
    const type: NodeType = kind === 'agent_loop' ? 'workflow_call' : kind;
    const nodeId = nodeTitle(type, nodes);
    const baseConfig = defaultConfig(type) as Record<string, unknown>;
    const config = {
      ...baseConfig,
      ...(kind === 'http_tool' ? { tool_id: refId } : {}),
      ...(kind === 'mcp_tool' ? { server_id: refId } : {}),
      ...(kind === 'knowledge_retrieval' ? { kb_ids: [refId] } : {}),
      ...(kind === 'agent_loop' ? { workflow_id: refId } : {}),
    };
    const source = selected;
    const node: CanvasNode = {
      id: nodeId,
      type: 'agentNode',
      position: source?.data.nodeType === 'agent_loop'
        ? { x: (source.position.x ?? 180) + 28, y: (source.position.y ?? 120) + 250 }
        : { x: (source?.position.x ?? 180) + 260, y: (source?.position.y ?? 120) + 80 },
      data: { label: nodeMeta[type].label, nodeType: type, config },
    };
    setNodes((current) => [...current, node]);
    if (source) {
      const dependency = source.data.nodeType === 'agent_loop' && kind !== 'agent_loop';
      setEdges((current) => current.some((edge) => edge.source === source.id && edge.target === nodeId) ? current : [...current, {
        id: `edge-${source.id}-${nodeId}`,
        source: source.id,
        target: nodeId,
        ...(dependency ? { sourceHandle: 'dependency', targetHandle: 'dependency' } : {}),
      }]);
    }
    setSelectedId(nodeId);
    setMessage('已生成独立子模块节点');
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
    if (version && !hasCanvasChanges) {
      setMessage(`Flow v${version.version_no} 无变化，无需保存`);
      setError('');
      return version;
    }
    try {
      const saved = await workflowApi.createFlowVersion(workflowId, { dsl_json: dsl, description: 'Saved from visual canvas' });
      setVersion(saved);
      setRestoredFromVersion(null);
      setFlowVersions((current) => [saved, ...current.filter((item) => item.id !== saved.id)].sort((left, right) => right.version_no - left.version_no));
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
      const target = hasCanvasChanges ? await saveFlow() : version;
      if (!target) return;
      const published = await workflowApi.publishFlowVersion(target.id);
      setVersion(published);
      setFlowVersions((current) => current.map((item) => ({ ...item, is_published: item.id === published.id, is_draft: item.id === published.id ? false : item.is_draft })));
      setMessage(`已发布 v${published.version_no}`);
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, '发布 Flow 失败'));
    }
  }

  function restoreVersionAsDraft(item: FlowVersion) {
    if (hasCanvasChanges && !window.confirm('当前画布有未保存修改。载入历史版本会覆盖这些修改，是否继续？')) return;
    const parsed = normalizeDSL(item.dsl_json);
    if (!parsed) {
      setError(`Flow v${item.version_no} 的 DSL 无法解析`);
      return;
    }
    const canvas = fromDSL(parsed);
    setNodes(canvas.nodes);
    setEdges(canvas.edges);
    setSelectedId(canvas.nodes[0]?.id ?? 'begin');
    setRestoredFromVersion(item.version_no);
    setVersionHistoryOpen(false);
    setMessage(`已载入 Flow v${item.version_no} 作为草稿；保存后会生成新版本`);
    setError('');
    window.requestAnimationFrame(() => void flowInstance?.fitView({ padding: 0.22, duration: 520, maxZoom: 1.2 }));
  }

  async function saveProfile() {
    if (!profile) return;
    try {
      const saved = await workflowApi.updateProfile(workflowId, {
        role: profile.role,
        goal: profile.goal,
        backstory: profile.backstory,
        system_prompt: profile.system_prompt,
        default_provider_id: profile.default_provider_id,
        default_model: profile.default_model,
        max_iterations: profile.max_iterations,
        max_execution_time_ms: profile.max_execution_time_ms,
        memory_enabled: profile.memory_enabled,
        planning_enabled: profile.planning_enabled,
        allow_code_execution: profile.allow_code_execution,
        default_tool_pack_ids: profile.default_tool_pack_ids ?? [],
        default_tool_ids: profile.default_tool_ids ?? [],
        default_skill_ids: profile.default_skill_ids ?? [],
        default_mcp_server_ids: profile.default_mcp_server_ids ?? [],
        default_knowledge_ids: profile.default_knowledge_ids ?? [],
        default_knowledge_top_k: profile.default_knowledge_top_k ?? 5,
        default_knowledge_mode: profile.default_knowledge_mode ?? 'hybrid',
        output_schema_json: profile.output_schema_json ?? {},
        tool_policy_json: profile.tool_policy_json ?? {},
        memory_policy_json: profile.memory_policy_json ?? {},
        reflection_policy_json: profile.reflection_policy_json ?? DEFAULT_REFLECTION_POLICY,
        context_policy_json: profile.context_policy_json ?? {},
        risk_level: profile.risk_level ?? 'medium',
        mode: profile.mode ?? 'react',
      });
      setProfile(saved);
      setMessage('Workflow Profile 已保存');
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, '保存 Workflow Profile 失败'));
    }
  }

  async function decideApproval(item: ApprovalRequest, approve: boolean, optionID?: string) {
    try {
      if (approve) {
        await workflowApi.approveRequest(item.id, optionID ? `choice:${optionID}` : 'approved from AgentCanvas workbench');
      } else {
        await workflowApi.rejectRequest(item.id, 'rejected from AgentCanvas workbench');
      }
      await workflowApi.resumeRun(item.run_id);
      await refreshApprovals();
      setMessage(approve ? '审批已通过，Run 已请求恢复' : '审批已拒绝，Run 已请求恢复');
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, approve ? '审批失败' : '拒绝审批失败'));
    }
  }

  async function createEvalDataset() {
    const name = evalDatasetName.trim();
    if (!name) {
      setError('Eval 数据集名称不能为空');
      return;
    }
    try {
      const created = await workflowApi.createEvalDataset(workflowId, { name });
      setEvalDatasetName('');
      await refreshEvalDatasets();
      setSelectedEvalDatasetId(created.id);
      setMessage('Eval 数据集已创建');
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建 Eval 数据集失败'));
    }
  }

  async function createEvalCase() {
    if (!selectedEvalDatasetId) {
      setError('请先选择 Eval 数据集');
      return;
    }
    let inputJSON: Record<string, unknown>;
    let expectedJSON: unknown | undefined;
    try {
      inputJSON = parseJsonObject(evalCaseInput);
      expectedJSON = evalCaseExpected.trim() ? JSON.parse(evalCaseExpected) as unknown : undefined;
    } catch (err) {
      setError(friendlyErrorMessage(err, 'Eval Case JSON 不合法'));
      return;
    }
    try {
      await workflowApi.createEvalCase(selectedEvalDatasetId, {
        name: evalCaseName.trim() || `case-${evalCases.length + 1}`,
        input_json: inputJSON,
        expected_json: expectedJSON,
      });
      setEvalCaseName('');
      await refreshEvalCases(selectedEvalDatasetId);
      setMessage('Eval Case 已创建');
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建 Eval Case 失败'));
    }
  }

  async function runSelectedEvalDataset() {
    if (!selectedEvalDatasetId) {
      setError('请先选择 Eval 数据集');
      return;
    }
    try {
      const resp = await workflowApi.runEvalDataset(selectedEvalDatasetId, { flow_version_id: version?.id });
      setLatestEvalRun(resp.eval_run);
      setLatestEvalResults(resp.results);
      await refreshEvalRuns(selectedEvalDatasetId);
      setMessage(`Eval 已完成，通过率 ${(resp.eval_run.success_rate * 100).toFixed(1)}%`);
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, '运行 Eval 失败'));
    }
  }

  function applyRunTrace(trace: RunTrace) {
    setRunSteps(trace.steps ?? []);
    setChildRuns(trace.child_runs ?? []);
    setMemoryLogs(trace.memory_write_logs ?? []);
    setToolInvocations(trace.tool_invocations ?? []);
    setRunTraceSummary(trace.replay_summary ?? {});
  }

  async function loadRunTrace(runId: number, signal?: AbortSignal) {
    const trace = await workflowApi.getRunTrace(runId);
    if (signal?.aborted) return;
    applyRunTrace(trace);
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
    if (!target || hasCanvasChanges) {
      target = await saveFlow();
      if (!target) return;
    }
    const controller = new AbortController();
    const selectedRunMode = debugRunMode;
    runAbortRef.current = controller;
    setRunningDebugMode(selectedRunMode);
    setDebugRunId(null);
    setEvents([]);
    setRunOutput(null);
    setRunSteps([]);
    setChildRuns([]);
    setMemoryLogs([]);
    setToolInvocations([]);
    setRunTraceSummary(null);
    setError('');
    try {
      if (selectedRunMode === 'complete') {
        const resp = await workflowApi.run(workflowId, { flow_version_id: target.id, input });
        if (controller.signal.aborted) return;
        setDebugRunId(resp.run.id);
        setRunOutput(resp.output);
        void loadRunTrace(resp.run.id, controller.signal).catch(() => undefined);
        return;
      }
      await workflowApi.streamRun(workflowId, { flow_version_id: target.id, input }, {
        signal: controller.signal,
        onMessage: (msg) => {
          if (controller.signal.aborted) return;
          if (msg.event === 'done') {
            const done = JSON.parse(msg.data) as { run: { id: number }; output: Record<string, unknown> };
            setDebugRunId(done.run.id);
            setRunOutput(done.output);
            void loadRunTrace(done.run.id, controller.signal).catch(() => undefined);
            return;
          }
          if (msg.event === 'error') {
            setError(friendlyErrorMessage(msg.data, '调试运行失败'));
            return;
          }
          const data = JSON.parse(msg.data) as RuntimeEvent;
          if (data.run_id) setDebugRunId(data.run_id);
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

  async function pauseDebugRun() {
    if (!debugRunId) {
      setError('当前调试运行还没有生成 run id');
      return;
    }
    try {
      await workflowApi.pauseRun(debugRunId);
      runAbortRef.current?.abort();
      runAbortRef.current = null;
      setRunningDebugMode(null);
      setMessage(`Run #${debugRunId} 已暂停，可在审批/恢复流程中继续`);
    } catch (err) {
      setError(friendlyErrorMessage(err, '暂停 Run 失败'));
    }
  }

  async function cancelDebugRun() {
    if (!debugRunId) {
      runAbortRef.current?.abort();
      runAbortRef.current = null;
      setRunningDebugMode(null);
      setError('已停止浏览器端调试流；后端 run id 尚未返回，无法调用取消接口');
      return;
    }
    try {
      await workflowApi.cancelRun(debugRunId);
      runAbortRef.current?.abort();
      runAbortRef.current = null;
      setRunningDebugMode(null);
      setMessage(`Run #${debugRunId} 已取消`);
    } catch (err) {
      setError(friendlyErrorMessage(err, '取消 Run 失败'));
    }
  }

  async function createWorkflowConversation() {
    try {
      const created = await workflowApi.createConversation(workflowId);
      setConversations((current) => [created, ...current]);
      setConversationId(created.id);
      setConversationMessages([]);
      setMode('chat');
      setMessage('已创建 Workflow 会话');
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建 Workflow 会话失败'));
    }
  }

  async function sendWorkflowMessage() {
    const question = chatQuestion.trim();
    if (!question || chatStreaming) return;
    let targetConversationId = conversationId;
    if (!targetConversationId) {
      try {
        const created = await workflowApi.createConversation(workflowId, question.slice(0, 48));
        setConversations((current) => [created, ...current]);
        setConversationId(created.id);
        targetConversationId = created.id;
      } catch (err) {
        setError(friendlyErrorMessage(err, '创建 Workflow 会话失败'));
        return;
      }
    }
    let target = version;
    if (!target || hasCanvasChanges) {
      target = await saveFlow();
      if (!target) return;
    }
    chatAbortRef.current?.abort();
    const controller = new AbortController();
    chatAbortRef.current = controller;
    setChatQuestion('');
    setPendingChatQuestion(question);
    setPendingChatAnswer('');
    setChatStreaming(true);
    setEvents([]);
    setRunOutput(null);
    setError('');
    try {
      await workflowApi.streamConversationMessage(workflowId, targetConversationId, {
        question,
        flow_version_id: target.id,
      }, {
        signal: controller.signal,
        onMessage: (msg) => {
          if (controller.signal.aborted) return;
          let data: unknown;
          try {
            data = JSON.parse(msg.data) as unknown;
          } catch {
            setError('Workflow 返回了无法解析的流式事件');
            return;
          }
          if (msg.event === 'done') {
            const result = data as WorkflowMessageResponse;
            setConversationMessages((current) => [...current, result.user_message, result.assistant_message]);
            setDebugRunId(result.run.id);
            setRunOutput(result.output);
            setPendingChatQuestion('');
            setPendingChatAnswer('');
            void loadRunTrace(result.run.id, controller.signal).catch(() => undefined);
            void workflowApi.listConversations(workflowId).then(setConversations).catch(() => undefined);
            return;
          }
          if (msg.event === 'error') {
            const payload = data as { error?: string };
            setError(friendlyErrorMessage(payload.error ?? data, 'Workflow 对话运行失败'));
            return;
          }
          const event = data as RuntimeEvent;
          if (event.run_id) setDebugRunId(event.run_id);
          if (event.type === 'llm_delta' && typeof event.payload?.delta === 'string') {
            setPendingChatAnswer((current) => current + String(event.payload?.delta));
          }
          setEvents((current) => [...current, event]);
        },
        onError: (err) => {
          if (!controller.signal.aborted) setError(friendlyErrorMessage(err, 'Workflow 对话运行失败'));
        },
      });
    } finally {
      if (chatAbortRef.current === controller) {
        chatAbortRef.current = null;
        setChatStreaming(false);
      }
    }
  }

  function stopWorkflowMessage() {
    chatAbortRef.current?.abort();
    chatAbortRef.current = null;
    setChatStreaming(false);
    if (debugRunId) void workflowApi.cancelRun(debugRunId).catch(() => undefined);
  }

  const config = selected?.data.config as Record<string, unknown> | undefined;

  function focusWorkflow() {
    void flowInstance?.fitView({ padding: 0.22, duration: 520, maxZoom: 1.2 });
  }

  function focusSelected() {
    if (!selected || !flowInstance) return;
    void flowInstance.setCenter(selected.position.x + 104, selected.position.y + 70, { zoom: 1.05, duration: 480 });
  }

  async function toggleCanvasFullscreen() {
    if (!document.fullscreenElement) await canvasPageRef.current?.requestFullscreen();
    else await document.exitFullscreen();
  }

  useEffect(() => {
    const syncFullscreen = () => setCanvasFullscreen(document.fullscreenElement === canvasPageRef.current);
    document.addEventListener('fullscreenchange', syncFullscreen);
    return () => document.removeEventListener('fullscreenchange', syncFullscreen);
  }, []);

  useEffect(() => {
    if (!versionHistoryOpen) return undefined;
    const closeOnPointer = (event: PointerEvent) => {
      if (!versionControlRef.current?.contains(event.target as Node)) setVersionHistoryOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setVersionHistoryOpen(false);
    };
    document.addEventListener('pointerdown', closeOnPointer);
    document.addEventListener('keydown', closeOnEscape);
    return () => {
      document.removeEventListener('pointerdown', closeOnPointer);
      document.removeEventListener('keydown', closeOnEscape);
    };
  }, [versionHistoryOpen]);

  return (
    <div ref={canvasPageRef} className="canvas-page">
      <header className="canvas-toolbar glass">
        <div className="min-w-0">
          <h1 className="canvas-editorial-title truncate"><span>{workflow?.name ?? 'Agent'}</span> <span className="canvas-editorial-accent">Canvas</span></h1>
          <p className="muted truncate">WORKFLOW STUDIO · {restoredFromVersion ? `RESTORED FROM v${restoredFromVersion} · UNSAVED DRAFT` : version ? `VERSION ${version.version_no}` : 'UNSAVED DRAFT'}</p>
        </div>
        <EngineeringAscii compact label="FLOW.RUNTIME" className="canvas-ascii" />
        <div className="canvas-tools">
          <Segmented value={mode} onChange={setMode} options={[{ value: 'config', label: 'Build' }, { value: 'profile', label: 'Profile' }, { value: 'reflections', label: 'Reflections' }, { value: 'approvals', label: 'Approvals' }, { value: 'eval', label: 'Evaluate' }, { value: 'chat', label: 'Dialogue' }, { value: 'debug', label: 'Debug' }, { value: 'dsl', label: 'DSL' }]} />
          <div ref={versionControlRef} className="canvas-version-control">
            <Button
              className="canvas-version-trigger"
              aria-expanded={versionHistoryOpen}
              aria-haspopup="dialog"
              onClick={() => setVersionHistoryOpen((open) => !open)}
            >
              <History size={16} />
              <span>History</span>
              <strong>{restoredFromVersion ? `Draft · v${restoredFromVersion}` : version ? `v${version.version_no}` : 'Draft'}</strong>
            </Button>
            {versionHistoryOpen ? (
              <section className="canvas-version-popover glass-strong" role="dialog" aria-label="Flow version history">
                <header>
                  <div>
                    <p className="eyebrow">VERSION CONTROL</p>
                    <h2>Flow History</h2>
                  </div>
                  <StatusBadge tone="info">{flowVersions.length} saved</StatusBadge>
                </header>
                <p className="canvas-version-help">载入历史版本不会覆盖记录；再次保存会生成一个新版本。</p>
                <div className="canvas-version-list">
                  {flowVersions.length === 0 ? <EmptyState icon={<History size={20} />} title="No saved versions" description="保存当前画布后，版本会出现在这里。" /> : null}
                  {flowVersions.map((item) => (
                    <button type="button" key={item.id} className={item.id === version?.id ? 'current' : ''} onClick={() => restoreVersionAsDraft(item)}>
                      <span className="canvas-version-number">v{item.version_no}</span>
                      <span className="canvas-version-copy">
                        <strong>{item.is_published ? 'Published' : item.is_draft ? 'Draft' : 'Saved version'}</strong>
                        <small>{formatDate(item.created_at)} · {item.description || 'Visual canvas snapshot'}</small>
                      </span>
                      {item.id === version?.id ? <StatusBadge tone="good">Current</StatusBadge> : <span className="canvas-version-restore"><RotateCcw size={14} />Load</span>}
                    </button>
                  ))}
                </div>
              </section>
            ) : null}
          </div>
          <Button onClick={() => void saveFlow()}>
            <Save size={16} />
            Save
          </Button>
          <Button tone="primary" onClick={() => void publishFlow()}>
            <Sparkles size={16} />
            Publish
          </Button>
          <IconButton label={canvasFullscreen ? '退出全屏' : '全屏编辑'} onClick={() => void toggleCanvasFullscreen()}>
            {canvasFullscreen ? <Minimize2 size={17} /> : <Maximize2 size={17} />}
          </IconButton>
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
          {paletteNodeTypes.map((type) => {
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
            nodes={nodesWithRunStatus}
            edges={edges}
            nodeTypes={nodeTypes}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onConnect={onConnect}
            onNodeClick={handleNodeClick}
            fitView
            onInit={setFlowInstance}
            minZoom={0.18}
            maxZoom={2}
            translateExtent={nodes.length ? undefined : [[-1200, -900], [1200, 900]]}
            proOptions={{ hideAttribution: true }}
          >
            <Background color="var(--canvas-grid)" gap={48} size={1} variant={BackgroundVariant.Dots} />
            <Controls />
            <MiniMap pannable zoomable nodeStrokeWidth={2} />
          </ReactFlow>
          <div className="canvas-navigation glass-strong">
            <IconButton label="适配全部节点" onClick={focusWorkflow}><Focus size={16} /></IconButton>
            <IconButton label="定位当前节点" disabled={!selected} onClick={focusSelected}><WorkflowIcon size={16} /></IconButton>
            <IconButton label={sidePanelOpen ? '收起配置面板' : '展开配置面板'} onClick={() => setSidePanelOpen((open) => !open)}>
              {sidePanelOpen ? <X size={16} /> : <Settings size={16} />}
            </IconButton>
          </div>
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
            <FormSheet selected={selected} title={selected.data.label} agent={isAgentNodeType(selected.data.nodeType)} onTitleChange={(value) => setNodes((current) => current.map((node) => node.id === selectedId ? { ...node, data: { ...node.data, label: value } } : node))}>
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
              {isAgentNodeType(selected.data.nodeType) && (
                <AgentLoopForm
                  config={config}
                  providers={providers}
                  tools={tools}
                  skills={skills}
                  knowledgeBases={knowledgeBases}
                  mcpServers={mcpServers}
                  updateConfig={updateSelectedConfig}
                  updateJSON={updateSelectedJSON}
                  addReferencedNode={addReferencedNode}
                />
              )}
              {isStaticAgentCallNodeType(selected.data.nodeType) && (
                <>
                  <Field label="目标 Agent">
                    <Select value={Number(config.workflow_id ?? 0)} onChange={(event) => updateSelectedConfig({ workflow_id: Number(event.target.value) })}>
                      <option value={0}>选择 Agent</option>
                      {callableWorkflows.map((workflow) => <option key={workflow.id} value={workflow.id}>{workflow.name}</option>)}
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
              {selected.data.nodeType === 'mcp_tool' && (
                <>
                  <Field label="MCP Server">
                    <Select value={Number(config.server_id ?? 0)} onChange={(event) => updateSelectedConfig({ server_id: Number(event.target.value) })}>
                      <option value={0}>选择 MCP Server</option>
                      {mcpServers.map((server) => <option key={server.id} value={server.id}>{server.name}</option>)}
                    </Select>
                  </Field>
                  <Field label="Tool Name">
                    <TextInput value={String(config.tool_name ?? '')} onChange={(event) => updateSelectedConfig({ tool_name: event.target.value })} />
                  </Field>
                  <Field label="输入 JSON" hint="支持 {{sys.query}} 和 {{node_id.field}}">
                    <TextArea value={prettyJson(config.input ?? {})} onChange={(event) => updateSelectedJSON('input', event.target.value)} />
                  </Field>
                </>
              )}
              {selected.data.nodeType === 'switch' && (
                <Field label="分支条件" hint="按顺序匹配，default 建议放在最后">
                  <SwitchConditionsEditor node={selected} nodes={nodes} onChange={(conditions) => updateSelectedConfig({ conditions })} />
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
                <EmptyState icon={<WorkflowIcon size={22} />} title="Begin 会透传运行输入" description="调试台默认使用 query 字段，后续节点可通过 {{sys.query}} 引用。" />
              )}
              {error ? <p className="error-text">{error}</p> : null}
            </FormSheet>
          ) : null}

          {mode === 'profile' && profile ? (
            <Panel title="Workflow Profile" eyebrow={workflow?.name ?? 'Agent'}>
              <Field label="Role">
                <TextInput value={profile.role} onChange={(event) => setProfile({ ...profile, role: event.target.value })} />
              </Field>
              <Field label="Goal">
                <TextArea value={profile.goal} onChange={(event) => setProfile({ ...profile, goal: event.target.value })} />
              </Field>
              <Field label="Backstory">
                <TextArea value={profile.backstory ?? ''} onChange={(event) => setProfile({ ...profile, backstory: event.target.value })} />
              </Field>
              <Field label="System Prompt">
                <TextArea value={profile.system_prompt ?? ''} onChange={(event) => setProfile({ ...profile, system_prompt: event.target.value })} />
              </Field>
              <Field label="默认 Provider">
                <Select value={profile.default_provider_id ?? 0} onChange={(event) => setProfile({ ...profile, default_provider_id: Number(event.target.value) || null })}>
                  <option value={0}>未设置</option>
                  {providers.map((provider) => <option key={provider.id} value={provider.id}>{provider.name}</option>)}
                </Select>
              </Field>
              <Field label="默认模型">
                <TextInput value={profile.default_model ?? ''} onChange={(event) => setProfile({ ...profile, default_model: event.target.value })} />
              </Field>
              <Field label="默认 Agent Mode">
                <Select value={profile.mode ?? 'react'} onChange={(event) => setProfile({ ...profile, mode: event.target.value as WorkflowProfile['mode'] })}>
                  <option value="react">ReAct</option>
                  <option value="plan_execute">Plan Guided</option>
                </Select>
              </Field>
              <Field label="默认风险等级">
                <Select value={profile.risk_level ?? 'medium'} onChange={(event) => setProfile({ ...profile, risk_level: event.target.value as WorkflowProfile['risk_level'] })}>
                  <option value="low">Low</option>
                  <option value="medium">Medium</option>
                  <option value="high">High</option>
                </Select>
              </Field>
              <Field label="默认 Tool Pack">
                <Select
                  multiple
                  value={numberArray(profile.default_tool_pack_ids).map(String)}
                  onChange={(event) => setProfile({ ...profile, default_tool_pack_ids: Array.from(event.target.selectedOptions).map((option) => Number(option.value)) })}
                >
                  {toolPacks.map((pack) => <option key={pack.id} value={pack.id}>{pack.name}</option>)}
                </Select>
              </Field>
              <Field label="默认工具">
                <Select
                  multiple
                  value={numberArray(profile.default_tool_ids).map(String)}
                  onChange={(event) => setProfile({ ...profile, default_tool_ids: Array.from(event.target.selectedOptions).map((option) => Number(option.value)) })}
                >
                  {tools.map((tool) => <option key={tool.id} value={tool.id}>{tool.name}</option>)}
                </Select>
              </Field>
              <Field label="默认 Skills">
                <Select
                  multiple
                  value={numberArray(profile.default_skill_ids).map(String)}
                  onChange={(event) => setProfile({ ...profile, default_skill_ids: Array.from(event.target.selectedOptions).map((option) => Number(option.value)) })}
                >
                  {skills.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
                </Select>
              </Field>
              <Field label="默认 MCP Server">
                <Select
                  multiple
                  value={numberArray(profile.default_mcp_server_ids).map(String)}
                  onChange={(event) => setProfile({ ...profile, default_mcp_server_ids: Array.from(event.target.selectedOptions).map((option) => Number(option.value)) })}
                >
                  {mcpServers.map((server) => <option key={server.id} value={server.id}>{server.name}</option>)}
                </Select>
              </Field>
              <Field label="默认知识库">
                <Select
                  multiple
                  value={numberArray(profile.default_knowledge_ids).map(String)}
                  onChange={(event) => setProfile({ ...profile, default_knowledge_ids: Array.from(event.target.selectedOptions).map((option) => Number(option.value)) })}
                >
                  {knowledgeBases.map((kb) => <option key={kb.id} value={kb.id}>{kb.name}</option>)}
                </Select>
              </Field>
              <Field label="默认知识库 Top K">
                <TextInput type="number" min={1} max={20} value={profile.default_knowledge_top_k ?? 5} onChange={(event) => setProfile({ ...profile, default_knowledge_top_k: Number(event.target.value) })} />
              </Field>
              <Field label="默认检索模式">
                <Select value={profile.default_knowledge_mode ?? 'hybrid'} onChange={(event) => setProfile({ ...profile, default_knowledge_mode: event.target.value as 'keyword' | 'vector' | 'hybrid' })}>
                  <option value="keyword">Keyword</option>
                  <option value="vector">Vector</option>
                  <option value="hybrid">Hybrid</option>
                </Select>
              </Field>
              <Field label="最大轮次">
                <TextInput type="number" min={1} max={50} value={profile.max_iterations} onChange={(event) => setProfile({ ...profile, max_iterations: Number(event.target.value) })} />
              </Field>
              <Field label="超时毫秒">
                <TextInput type="number" min={1000} max={600000} step={1000} value={profile.max_execution_time_ms} onChange={(event) => setProfile({ ...profile, max_execution_time_ms: Number(event.target.value) })} />
              </Field>
              <Field label="Memory">
                <Select value={profile.memory_enabled ? 'enabled' : 'disabled'} onChange={(event) => setProfile({ ...profile, memory_enabled: event.target.value === 'enabled' })}>
                  <option value="disabled">Disabled</option>
                  <option value="enabled">Enabled</option>
                </Select>
              </Field>
              <Field label="Planning">
                <Select value={profile.planning_enabled ? 'enabled' : 'disabled'} onChange={(event) => setProfile({ ...profile, planning_enabled: event.target.value === 'enabled' })}>
                  <option value="disabled">Disabled</option>
                  <option value="enabled">Enabled</option>
                </Select>
              </Field>
              <Field label="Code Execution">
                <Select value={profile.allow_code_execution ? 'enabled' : 'disabled'} onChange={(event) => setProfile({ ...profile, allow_code_execution: event.target.value === 'enabled' })}>
                  <option value="disabled">Disabled</option>
                  <option value="enabled">Enabled</option>
                </Select>
              </Field>
              <Field label="输出 Schema JSON">
                <TextArea
                  value={prettyJson(profile.output_schema_json ?? {})}
                  onChange={(event) => {
                    try {
                      setProfile({ ...profile, output_schema_json: JSON.parse(event.target.value) as unknown });
                      setError('');
                    } catch {
                      setError('输出 Schema JSON 格式不正确');
                    }
                  }}
                />
              </Field>
              <Field label="默认 Tool Policy JSON">
                <TextArea
                  value={prettyJson(profile.tool_policy_json ?? {})}
                  onChange={(event) => {
                    try {
                      setProfile({ ...profile, tool_policy_json: JSON.parse(event.target.value) as unknown });
                      setError('');
                    } catch {
                      setError('Tool Policy JSON 格式不正确');
                    }
                  }}
                />
              </Field>
              <Field label="默认 Memory Policy JSON">
                <TextArea
                  value={prettyJson(profile.memory_policy_json ?? {})}
                  onChange={(event) => {
                    try {
                      setProfile({ ...profile, memory_policy_json: JSON.parse(event.target.value) as unknown });
                      setError('');
                    } catch {
                      setError('Memory Policy JSON 格式不正确');
                    }
                  }}
                />
              </Field>
              <Field label="默认 Reflection Policy JSON">
                <TextArea
                  value={prettyJson(profile.reflection_policy_json ?? DEFAULT_REFLECTION_POLICY)}
                  onChange={(event) => {
                    try {
                      setProfile({ ...profile, reflection_policy_json: JSON.parse(event.target.value) as unknown });
                      setError('');
                    } catch {
                      setError('Reflection Policy JSON 格式不正确');
                    }
                  }}
                />
              </Field>
              <Field label="默认 Context Policy JSON">
                <TextArea
                  value={prettyJson(profile.context_policy_json ?? {})}
                  onChange={(event) => {
                    try {
                      setProfile({ ...profile, context_policy_json: JSON.parse(event.target.value) as unknown });
                      setError('');
                    } catch {
                      setError('Context Policy JSON 格式不正确');
                    }
                  }}
                />
              </Field>
              <Button tone="primary" onClick={() => void saveProfile()}>
                <Save size={16} />
                保存 Profile
              </Button>
              {error ? <p className="error-text">{error}</p> : null}
            </Panel>
          ) : null}

          {mode === 'reflections' ? (
            <Panel title="Persistent Reflexion" eyebrow={`${reflections.length} memories`}>
              <Field label="状态筛选">
                <div className="toolbar-actions">
                  <Select value={reflectionStatusFilter} onChange={(event) => {
                    const nextStatus = event.target.value as ReflectionStatus | '';
                    setReflectionStatusFilter(nextStatus);
                    void refreshReflections(nextStatus);
                  }}>
                    <option value="">全部状态</option>
                    <option value="candidate">Candidate</option>
                    <option value="active">Active</option>
                    <option value="validated">Validated</option>
                    <option value="disputed">Disputed</option>
                    <option value="superseded">Superseded</option>
                    <option value="archived">Archived</option>
                  </Select>
                  <Button onClick={() => void refreshReflections()}>刷新</Button>
                </div>
              </Field>
              <p className="muted">这里管理 Agent 从历史运行中提炼出的经验。Active 会参与同工作流召回，Validated 可作为可信的全局回退记忆。</p>
              {reflections.length === 0 ? (
                <EmptyState icon={<Bot size={22} />} title="暂无 Reflection" description="Agent 完成运行或遇到失败后，符合重要性与置信度门槛的反思会持久化到这里。" />
              ) : (
                <div className="trace-list">
                  {reflections.map((item) => (
                    <article className="trace-item" key={item.id}>
                      <div className="trace-item-head">
                        <strong>#{item.id} {item.task_summary || item.lesson}</strong>
                        <div className="toolbar-actions">
                          <StatusBadge tone={item.status === 'validated' ? 'good' : item.status === 'disputed' ? 'bad' : item.status === 'active' ? 'info' : 'neutral'}>{item.status}</StatusBadge>
                          <StatusBadge tone={item.kind === 'error_lesson' ? 'warn' : 'info'}>{item.kind}</StatusBadge>
                          <StatusBadge tone="neutral">{item.scope}</StatusBadge>
                        </div>
                      </div>
                      <p><strong>教训：</strong>{item.lesson}</p>
                      {item.root_cause ? <p><strong>根因：</strong>{item.root_cause}</p> : null}
                      {item.corrective_action ? <p><strong>纠正行动：</strong>{item.corrective_action}</p> : null}
                      {item.applicability ? <p><strong>适用条件：</strong>{item.applicability}</p> : null}
                      <p className="muted">
                        importance {item.importance.toFixed(2)} · confidence {item.confidence.toFixed(2)} · recalled {item.recall_count} · helpful {item.successful_use_count} · harmful {item.harmful_count} · {formatDate(item.created_at)}
                      </p>
                      <div className="toolbar-actions">
                        <Button disabled={item.status === 'active'} onClick={() => void updateReflectionStatus(item.id, 'active')}>Activate</Button>
                        <Button disabled={item.status === 'validated'} onClick={() => void updateReflectionStatus(item.id, 'validated')}>Validate</Button>
                        <Button disabled={item.status === 'disputed'} onClick={() => void updateReflectionStatus(item.id, 'disputed')}>Dispute</Button>
                        <Button disabled={item.status === 'archived'} onClick={() => void updateReflectionStatus(item.id, 'archived')}>Archive</Button>
                      </div>
                    </article>
                  ))}
                </div>
              )}
              {error ? <p className="error-text">{error}</p> : null}
            </Panel>
          ) : null}

          {mode === 'approvals' ? (
            <Panel title="人工审批" eyebrow={`${approvalRequests.length} pending`}>
              <ApprovalQueue items={approvalRequests} onDecide={(item, approve, optionID) => void decideApproval(item, approve, optionID)} />
              {error ? <p className="error-text">{error}</p> : null}
            </Panel>
          ) : null}

          {mode === 'eval' ? (
            <Panel title="Eval" eyebrow={latestEvalRun ? `${latestEvalRun.passed_cases}/${latestEvalRun.total_cases} passed` : 'regression'}>
              <Field label="新建数据集">
                <div className="inline-form">
                  <TextInput value={evalDatasetName} onChange={(event) => setEvalDatasetName(event.target.value)} placeholder="Regression smoke" />
                  <Button onClick={() => void createEvalDataset()}>创建</Button>
                </div>
              </Field>
              <Field label="数据集">
                <Select value={selectedEvalDatasetId} onChange={(event) => setSelectedEvalDatasetId(Number(event.target.value))}>
                  <option value={0}>选择数据集</option>
                  {evalDatasets.map((dataset) => <option key={dataset.id} value={dataset.id}>{dataset.name}</option>)}
                </Select>
              </Field>
              <Field label="新增 Case">
                <TextInput value={evalCaseName} onChange={(event) => setEvalCaseName(event.target.value)} placeholder="case name" />
              </Field>
              <Field label="Case 输入 JSON">
                <TextArea value={evalCaseInput} onChange={(event) => setEvalCaseInput(event.target.value)} />
              </Field>
              <Field label="期望 JSON">
                <TextArea value={evalCaseExpected} onChange={(event) => setEvalCaseExpected(event.target.value)} />
              </Field>
              <Button onClick={() => void createEvalCase()}>新增 Case</Button>
              <Button tone="primary" onClick={() => void runSelectedEvalDataset()}>
                <Play size={16} />
                运行 Eval
              </Button>
              {evalCases.length > 0 ? (
                <div className="trace-list">
                  {evalCases.map((item) => (
                    <article className="trace-item" key={item.id}>
                      <div className="trace-item-head">
                        <strong>{item.name}</strong>
                        <StatusBadge tone="neutral">case</StatusBadge>
                      </div>
                      <pre className="code-box">{prettyJson(item.input_json)}</pre>
                    </article>
                  ))}
                </div>
              ) : null}
              {latestEvalRun ? (
                <div className="trace-summary">
                  <StatusBadge tone={latestEvalRun.success_rate >= 1 ? 'good' : 'warn'}>{(latestEvalRun.success_rate * 100).toFixed(1)}%</StatusBadge>
                  <span>{latestEvalRun.passed_cases} passed</span>
                  <span>{latestEvalRun.failed_cases} failed</span>
                </div>
              ) : null}
              {evalTrend?.latest ? (
                <div className="trace-summary">
                  <span>runs {metricFixed(evalTrend.trend_summary, 'run_count')}</span>
                  <span>best {metricPercent(evalTrend.trend_summary, 'best_success_rate')}</span>
                  <span>delta {metricPercent(evalTrend.delta, 'success_rate')}</span>
                  <span>flow #{evalTrend.latest.flow_version_id || '-'}</span>
                </div>
              ) : null}
              {latestEvalRun ? (() => {
                const metrics = evalTrend?.latest?.metrics ?? evalSummaryMetrics(latestEvalRun);
                return (
                  <div className="trace-summary">
                    <span>tool {metricPercent(metrics, 'avg_tool_call_accuracy')}</span>
                    <span>schema {metricPercent(metrics, 'avg_schema_compliance')}</span>
                    <span>refs {metricPercent(metrics, 'avg_reference_hit_rate')}</span>
                    <span>tokens {metricFixed(metrics, 'avg_total_tokens')}</span>
                    <span>latency {metricFixed(metrics, 'avg_latency_ms', 'ms')}</span>
                    <span>approval {metricPercent(metrics, 'human_approval_waiting_rate')}</span>
                  </div>
                );
              })() : null}
              {evalRuns.length > 0 ? (
                <div className="trace-list">
                  {evalRuns.slice(0, 8).map((item) => (
                    <article className="trace-item" key={item.id}>
                      <div className="trace-item-head">
                        <strong>Eval Run #{item.id}</strong>
                        <StatusBadge tone={item.success_rate >= 1 ? 'good' : item.status === 'completed' ? 'warn' : 'info'}>{item.status}</StatusBadge>
                      </div>
                      <p className="muted">
                        {(item.success_rate * 100).toFixed(1)}% · {item.passed_cases}/{item.total_cases} passed · flow #{item.flow_version_id || '-'}
                      </p>
                    </article>
                  ))}
                </div>
              ) : null}
              {latestEvalResults.length > 0 ? (
                <div className="trace-list">
                  {latestEvalResults.map((item) => (
                    <article className="trace-item" key={item.id}>
                      <div className="trace-item-head">
                        <strong>Case #{item.eval_case_id}</strong>
                        <StatusBadge tone={item.status === 'passed' ? 'good' : 'warn'}>{item.status}</StatusBadge>
                      </div>
                      <p>{item.reason || item.error_message || 'No reason'}</p>
                      <p className="muted">score {item.score.toFixed(2)} · {item.latency_ms}ms</p>
                    </article>
                  ))}
                </div>
              ) : null}
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
                <Button disabled={!runningDebugMode} onClick={() => void pauseDebugRun()}>暂停</Button>
                <Button disabled={!runningDebugMode} onClick={() => void cancelDebugRun()}>取消</Button>
              </div>
              <p className="muted">{debugRunMode === 'complete' ? '完整运行会等待整套流程结束，只显示最终返回结果。' : '流式运行会实时展示每个运行事件和 LLM delta。'}{debugRunId ? ` 当前 Run #${debugRunId}` : ''}</p>
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
                {runTraceSummary ? (
                  <div className="card">
                    <div className="card-title">
                      <h3>Trace Replay Summary</h3>
                      <StatusBadge tone="info">{String(runTraceSummary.status ?? 'trace')}</StatusBadge>
                    </div>
                    <pre className="code-box">{prettyJson(runTraceSummary)}</pre>
                  </div>
                ) : null}
                {runOutput && Object.keys(objectRecord(runOutput.context_trace)).length > 0 ? (
                  <div className="card">
                    <div className="card-title">
                      <h3>Rules / Context</h3>
                      <StatusBadge tone="info">runtime</StatusBadge>
                    </div>
                    <ContextRulesTrace trace={runOutput.context_trace} />
                  </div>
                ) : null}
                {runSteps.length > 0 ? (
                  <div className="card">
                    <div className="card-title">
                      <h3>Agent Steps</h3>
                      <StatusBadge tone="info">{runSteps.length}</StatusBadge>
                      {runSteps.some((step) => step.compressed) ? <StatusBadge tone="warn">{runSteps.filter((step) => step.compressed).length} compressed</StatusBadge> : null}
                    </div>
                    <AgentTraceTimeline
                      steps={runSteps}
                      onReflectionFeedback={(reflectionId, verdict) => void feedbackReflection(reflectionId, verdict)}
                      reflectionFeedback={reflectionFeedback}
                    />
                  </div>
                ) : null}
                {childRuns.length > 0 ? (
                  <div className="card">
                    <div className="card-title"><h3>子 Workflow Runs</h3><StatusBadge tone="info">{childRuns.length}</StatusBadge></div>
                    <pre className="code-box">{prettyJson(childRuns)}</pre>
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
                    <ToolInvocationList items={toolInvocations} />
                  </div>
                ) : null}
              </div>
            </Panel>
          )}

          {mode === 'chat' && (
            <Panel title="Workflow 对话" eyebrow={version ? `Flow v${version.version_no}` : '未保存'}>
              <div className="workflow-chat-head">
                <Select value={conversationId} onChange={(event) => setConversationId(Number(event.target.value))}>
                  <option value={0}>选择会话</option>
                  {conversations.map((item) => <option key={item.id} value={item.id}>{item.title || `会话 #${item.id}`}</option>)}
                </Select>
                <Button title="新建会话" onClick={() => void createWorkflowConversation()}>
                  <Plus size={15} />
                  新建
                </Button>
              </div>
              <div className="workflow-chat-messages" role="log" aria-live="polite" aria-busy={chatStreaming}>
                {conversationMessages.length === 0 && !pendingChatQuestion ? (
                  <EmptyState icon={<MessageSquareText size={22} />} title="用这个 Workflow 开始对话" description="每条回答都会关联运行版本和 Trace。" />
                ) : null}
                {conversationMessages.filter((item) => item.role === 'user' || item.role === 'assistant').map((item) => (
                  <article className={`workflow-chat-message ${item.role}`} key={item.id}>
                    <p>{item.content}</p>
                    {item.run_id ? (
                      <button type="button" onClick={() => {
                        setDebugRunId(item.run_id ?? null);
                        if (item.run_id) void loadRunTrace(item.run_id).then(() => setMode('debug')).catch((err) => setError(friendlyErrorMessage(err, '加载 Trace 失败')));
                      }}>Run #{item.run_id} · 查看 Trace</button>
                    ) : null}
                  </article>
                ))}
                {pendingChatQuestion ? <article className="workflow-chat-message user"><p>{pendingChatQuestion}</p></article> : null}
                {chatStreaming ? (
                  <article className="workflow-chat-message assistant streaming">
                    <p>{pendingChatAnswer || 'Workflow 正在执行…'}</p>
                    <span>{debugRunId ? `Run #${debugRunId}` : '正在创建 Run'}</span>
                  </article>
                ) : null}
              </div>
              <div className="workflow-chat-composer">
                <TextArea
                  value={chatQuestion}
                  onChange={(event) => setChatQuestion(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key !== 'Enter' || event.shiftKey || event.nativeEvent.isComposing) return;
                    event.preventDefault();
                    void sendWorkflowMessage();
                  }}
                  placeholder="输入消息，Enter 发送"
                />
                {chatStreaming ? (
                  <Button onClick={stopWorkflowMessage}>停止</Button>
                ) : (
                  <Button tone="primary" disabled={!chatQuestion.trim()} onClick={() => void sendWorkflowMessage()}>
                    <Send size={15} />
                    发送
                  </Button>
                )}
              </div>
              {error ? <p className="error-text" role="alert">{error}</p> : null}
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
