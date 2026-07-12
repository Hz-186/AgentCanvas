import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useParams } from 'react-router-dom';
import { ReactFlow, Background, Controls, MiniMap, addEdge, useEdgesState, useNodesState, type Connection, type NodeMouseHandler, type ReactFlowInstance } from '@xyflow/react';
import { Copy, Focus, Maximize2, Minimize2, Play, Save, Search, Send, Trash2 } from 'lucide-react';
import { Button, IconButton, Modal, Select } from '@/components/ui';
import { knowledgeApi, settingsApi, workflowApi } from '@/api/resources';
import type { FlowVersion, KnowledgeBase, MCPServer, ModelProvider, ToolDefinition, Workflow, WorkflowTeam } from '@/types/api';
import type { RuntimeEvent, RunDoneEvent } from '@/types/events';
import type { NodeType } from '@/types/flow';
import { parseJsonObject } from '@/utils/format';
import { createCanvasNode, emptyWorkflow, fromDSL, toDSL } from './canvas/hooks/useDslBridge';
import { nodeTypes } from './canvas/nodes/CanvasNode';
import { NodePalette } from './canvas/panels/NodePalette';
import { InspectorPanel } from './canvas/panels/InspectorPanel';
import { RunObservatory } from './canvas/debug/RunObservatory';
import type { CanvasEdge, CanvasNode } from './canvas/types';

function CanvasWorkspace() {
  const workflowId = Number(useParams<{ id: string }>().id ?? 0);
  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [versions, setVersions] = useState<FlowVersion[]>([]);
  const [providers, setProviders] = useState<ModelProvider[]>([]);
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([]);
  const [tools, setTools] = useState<ToolDefinition[]>([]);
  const [mcpServers, setMcpServers] = useState<MCPServer[]>([]);
  const [workflows, setWorkflows] = useState<Workflow[]>([]);
  const [teams, setTeams] = useState<WorkflowTeam[]>([]);
  const initial = useMemo(() => emptyWorkflow(workflowId), [workflowId]);
  const [nodes, setNodes, onNodesChange] = useNodesState<CanvasNode>(initial.nodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState<CanvasEdge>(initial.edges);
  const [selectedId, setSelectedId] = useState<string | null>('agent_loop');
  const [instance, setInstance] = useState<ReactFlowInstance<CanvasNode, CanvasEdge> | null>(null);
  const [focusMode, setFocusMode] = useState(false);
  const [leftCollapsed, setLeftCollapsed] = useState(false);
  const [rightCollapsed, setRightCollapsed] = useState(false);
  const [leftWidth, setLeftWidth] = useState(244);
  const [rightWidth, setRightWidth] = useState(360);
  const [dragging, setDragging] = useState<'left' | 'right' | null>(null);
  const [inspectorFullscreen, setInspectorFullscreen] = useState(false);
  const [saving, setSaving] = useState(false);
  const [runOpen, setRunOpen] = useState(false);
  const runInput = '{\n  "query": "帮我总结这个工作流"\n}';
  const [runEvents, setRunEvents] = useState<Array<RuntimeEvent | RunDoneEvent | { error: string }>>([]);
  const [running, setRunning] = useState(false);
  const [locatorId, setLocatorId] = useState('');
  const abortRef = useRef<AbortController | null>(null);

  const selected = nodes.find((node) => node.id === selectedId) ?? null;
  const displayEdges = useMemo(() => edges.map((edge) => {
    const source = nodes.find((node) => node.id === edge.source);
    const dependency = source ? ['knowledge_retrieval', 'memory_read', 'memory_write', 'http_tool', 'mcp_tool'].includes(source.data.nodeType) : false;
    return { ...edge, className: source?.data.nodeType === 'switch' ? 'edge-switch' : dependency ? 'edge-dependency' : 'edge-flow' };
  }), [edges, nodes]);
  const issues = useMemo(() => {
    const result: string[] = [];
    const beginCount = nodes.filter((node) => node.data.nodeType === 'begin').length;
    if (beginCount !== 1) result.push('需要且只能有一个 Begin 节点');
    const incoming = new Set(edges.map((edge) => edge.target));
    nodes.filter((node) => node.data.nodeType !== 'begin' && !incoming.has(node.id)).forEach((node) => result.push(`${node.data.label} 没有入口连线`));
    nodes.filter((node) => node.data.nodeType === 'switch').forEach((node) => {
      const outgoing = new Set(edges.filter((edge) => edge.source === node.id).map((edge) => edge.target));
      const conditions = Array.isArray((node.data.config as Record<string, unknown>).conditions) ? (node.data.config as Record<string, unknown>).conditions as Array<{ expr?: string; target?: string }> : [];
      if (conditions.length === 0) result.push(`${node.data.label} 至少需要一个条件`);
      conditions.forEach((condition, index) => {
        if (!condition.expr) result.push(`${node.data.label} 第 ${index + 1} 条条件缺少表达式`);
        if (!condition.target) result.push(`${node.data.label} 第 ${index + 1} 条条件缺少目标节点`);
        if (condition.target && !outgoing.has(condition.target)) result.push(`${node.data.label} 条件目标 ${condition.target} 需要一条真实连线`);
      });
    });
    return result.slice(0, 6);
  }, [nodes, edges]);

  useEffect(() => {
    let mounted = true;
    async function load() {
      const [wf, list] = await Promise.all([workflowApi.get(workflowId), workflowApi.listFlowVersions(workflowId).catch(() => [])]);
      if (!mounted) return;
      setWorkflow(wf);
      setVersions(list);
      const latest = list.find((item) => item.is_published) ?? list[0];
      const graph = latest ? fromDSL(latest.dsl_json, workflowId) : emptyWorkflow(workflowId);
      setNodes(graph.nodes);
      setEdges(graph.edges);
      setSelectedId(graph.nodes[1]?.id ?? graph.nodes[0]?.id ?? null);
      requestAnimationFrame(() => instance?.fitView({ padding: 0.22, duration: 520 }));
    }
    if (workflowId) void load();
    return () => { mounted = false; };
  }, [workflowId, setEdges, setNodes]);

  useEffect(() => {
    let mounted = true;
    async function loadResources() {
      const [providerList, kbList, toolList, mcpList, workflowList, teamList] = await Promise.all([
        settingsApi.providers.list().catch(() => []),
        knowledgeApi.list().catch(() => []),
        settingsApi.tools.list().catch(() => []),
        settingsApi.tools.listMCPServers().catch(() => []),
        workflowApi.list().catch(() => []),
        workflowApi.listTeams().catch(() => []),
      ]);
      if (!mounted) return;
      setProviders(providerList);
      setKnowledgeBases(kbList);
      setTools(toolList);
      setMcpServers(mcpList);
      setWorkflows(workflowList);
      setTeams(teamList);
    }
    void loadResources();
    return () => { mounted = false; };
  }, []);

  const onConnect = useCallback((connection: Connection) => {
    const sourceNode = nodes.find((node) => node.id === connection.source);
    const sourceHandle = connection.sourceHandle ?? undefined;
    let label: string | undefined;
    if (sourceNode?.data.nodeType === 'switch' && sourceHandle?.startsWith('branch-') && connection.target) {
      const branchIndex = Number(sourceHandle.replace('branch-', ''));
      const conditions = Array.isArray((sourceNode.data.config as Record<string, unknown>).conditions) ? (sourceNode.data.config as Record<string, unknown>).conditions as Array<{ expr?: string; target?: string }> : [];
      label = conditions[branchIndex]?.expr;
      setNodes((current) => current.map((node) => {
        if (node.id !== sourceNode.id) return node;
        const next = conditions.map((condition, index) => index === branchIndex ? { ...condition, target: connection.target ?? '' } : condition);
        return { ...node, data: { ...node.data, config: { ...node.data.config, conditions: next } } };
      }));
    }
    setEdges((current) => addEdge({ ...connection, animated: true, label }, current));
  }, [nodes, setEdges, setNodes]);

  const onNodeClick: NodeMouseHandler<CanvasNode> = (_, node) => setSelectedId(node.id);

  const addNode = (type: NodeType) => {
    const center = instance?.screenToFlowPosition({ x: window.innerWidth / 2, y: window.innerHeight / 2 }) ?? { x: 260, y: 260 };
    const node = createCanvasNode(type, center, nodes.length + 1);
    setNodes((current) => [...current, node]);
    setSelectedId(node.id);
  };

  const renameSelected = (label: string) => {
    if (!selected) return;
    setNodes((current) => current.map((node) => node.id === selected.id ? { ...node, data: { ...node.data, label } } : node));
  };

  const updateSelectedConfig = (patch: Record<string, unknown>) => {
    if (!selected) return;
    setNodes((current) => current.map((node) => node.id === selected.id ? { ...node, data: { ...node.data, config: { ...node.data.config, ...patch } } } : node));
  };

  const focusWorkflow = () => instance?.fitView({ padding: 0.24, duration: 620 });

  const focusNode = (id: string) => {
    const node = nodes.find((item) => item.id === id);
    if (!node) return;
    setSelectedId(node.id);
    instance?.setCenter(node.position.x + 115, node.position.y + 60, { zoom: 1.05, duration: 520 });
  };

  const deleteSelected = () => {
    if (!selected || selected.data.nodeType === 'begin') return;
    setNodes((current) => current.filter((node) => node.id !== selected.id));
    setEdges((current) => current.filter((edge) => edge.source !== selected.id && edge.target !== selected.id));
    setSelectedId(null);
  };

  const duplicateSelected = () => {
    if (!selected) return;
    const copy: CanvasNode = { ...selected, id: `${selected.id}_copy_${Date.now()}`, selected: false, position: { x: selected.position.x + 46, y: selected.position.y + 46 }, data: { ...selected.data, label: `${selected.data.label} Copy`, config: { ...selected.data.config, _ui: { x: selected.position.x + 46, y: selected.position.y + 46 } } } };
    setNodes((current) => [...current, copy]);
    setSelectedId(copy.id);
  };

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      if (target?.tagName === 'INPUT' || target?.tagName === 'TEXTAREA' || target?.tagName === 'SELECT') return;
      if (event.key === 'Escape' && focusMode) setFocusMode(false);
      else if (event.key === 'f') focusWorkflow();
      if (event.key === 'Backspace' || event.key === 'Delete') deleteSelected();
      if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === 'd') { event.preventDefault(); duplicateSelected(); }
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [selected, nodes, edges, instance, focusMode]);

  useEffect(() => {
    if (!dragging) return;
    const move = (event: PointerEvent) => {
      const page = document.querySelector('.canvas-page')?.getBoundingClientRect();
      if (!page) return;
      if (dragging === 'left') setLeftWidth(Math.min(360, Math.max(190, event.clientX - page.left)));
      else setRightWidth(Math.min(520, Math.max(290, page.right - event.clientX)));
    };
    const stop = () => setDragging(null);
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', stop, { once: true });
    return () => { window.removeEventListener('pointermove', move); window.removeEventListener('pointerup', stop); };
  }, [dragging]);

  const save = async (publish = false) => {
    setSaving(true);
    try {
      const version = await workflowApi.createFlowVersion(workflowId, { dsl_json: toDSL(workflowId, nodes, edges), description: publish ? 'Web v2 published version' : 'Web v2 draft' });
      await workflowApi.validateFlowVersion(version.id);
      if (publish) await workflowApi.publishFlowVersion(version.id);
      setVersions(await workflowApi.listFlowVersions(workflowId));
    } finally {
      setSaving(false);
    }
  };

  const run = async () => {
    setRunOpen(true);
    setRunEvents([]);
    setRunning(true);
    setNodes((current) => current.map((node) => ({ ...node, data: { ...node.data, runtimeStatus: 'idle' } })));
    abortRef.current?.abort();
    abortRef.current = new AbortController();
    try {
      const input = parseJsonObject(runInput);
      const version = await workflowApi.createFlowVersion(workflowId, { dsl_json: toDSL(workflowId, nodes, edges), description: 'Web v2 debug run' });
      await workflowApi.streamRun(workflowId, { flow_version_id: version.id, input }, {
        signal: abortRef.current.signal,
        onMessage: (msg) => {
          try {
            const data = JSON.parse(msg.data) as RuntimeEvent | RunDoneEvent | { error: string };
            setRunEvents((current) => [...current, data]);
            if ('type' in data && data.node_id) {
              const status = data.type === 'node_started' ? 'running' : data.type === 'node_failed' ? 'failed' : data.type === 'node_finished' ? 'success' : null;
              if (status) setNodes((current) => current.map((node) => node.id === data.node_id ? { ...node, data: { ...node.data, runtimeStatus: status } } : node));
            }
          } catch {
            setRunEvents((current) => [...current, { error: msg.data }]);
          }
        },
        onError: (error) => setRunEvents((current) => [...current, { error: error.message }]),
      });
    } catch (error) {
      setRunEvents((current) => [...current, { error: error instanceof Error ? error.message : String(error) }]);
    } finally {
      setRunning(false);
      abortRef.current = null;
    }
  };

  const stopRun = () => {
    abortRef.current?.abort();
    abortRef.current = null;
    setRunning(false);
  };

  return (
    <div className={`canvas-page${focusMode ? ' focus-mode' : ''}${leftCollapsed ? ' left-collapsed' : ''}${rightCollapsed ? ' right-collapsed' : ''}`} style={{ '--left-w': `${leftWidth}px`, '--right-w': `${rightWidth}px` } as React.CSSProperties}>
      <NodePalette onAdd={addNode} onCollapse={() => setLeftCollapsed(true)} />
      <button className={`resize-handle${dragging === 'left' ? ' dragging' : ''}`} onPointerDown={() => setDragging('left')} aria-label="调整节点库宽度" />
      <section className={`canvas-panel canvas-board deckle-paper${focusMode ? ' fullscreen' : ''}`}>
        <div className="canvas-toolbar">
          <div className="toolbar-cluster glass">
            <span className="badge">{workflow?.name ?? 'Workflow'}</span>
            <span className="badge badge-info">v{versions[0]?.version_no ?? 0}</span>
            <span className={`badge ${issues.length ? 'badge-warn' : 'badge-good'}`}>{issues.length ? `${issues.length} issues` : 'valid'}</span>
            <Search size={15} style={{ color: 'var(--text-subtle)' }} />
            <Select className="canvas-locator" value={locatorId} onChange={(event) => { setLocatorId(event.target.value); focusNode(event.target.value); }}>
              <option value="">定位节点</option>
              {nodes.map((node) => <option key={node.id} value={node.id}>{node.data.label} · {node.id}</option>)}
            </Select>
          </div>
          <div className="toolbar-cluster glass">
            <IconButton aria-label="回到工作流" title="回到工作流" onClick={focusWorkflow}><Focus size={17} /></IconButton>
            <IconButton aria-label="复制节点" title="复制节点" onClick={duplicateSelected} disabled={!selected}><Copy size={17} /></IconButton>
            <IconButton aria-label="删除节点" title="删除节点" onClick={deleteSelected} disabled={!selected || selected.data.nodeType === 'begin'}><Trash2 size={17} /></IconButton>
            <IconButton aria-label="焦点模式" title="焦点模式" onClick={() => setFocusMode((value) => !value)}>{focusMode ? <Minimize2 size={17} /> : <Maximize2 size={17} />}</IconButton>
            {leftCollapsed ? <Button size="small" onClick={() => setLeftCollapsed(false)}>展开节点库</Button> : null}
            {rightCollapsed ? <Button size="small" onClick={() => setRightCollapsed(false)}>展开检查器</Button> : null}
            <Button size="small" onClick={() => void save(false)} disabled={saving}><Save size={16} /> 保存</Button>
            <Button size="small" tone="primary" onClick={() => void save(true)} disabled={saving}><Send size={16} /> 发布</Button>
            <Button size="small" tone="ghost" onClick={() => void run()} disabled={running}><Play size={16} /> {running ? '运行中' : '运行'}</Button>
          </div>
        </div>
        {issues.length ? <div className="validation-pop glass"><strong>Validation</strong>{issues.map((issue) => <span key={issue}>{issue}</span>)}</div> : null}
        <ReactFlow
          nodes={nodes}
          edges={displayEdges}
          nodeTypes={nodeTypes}
          onInit={setInstance}
          onNodesChange={onNodesChange}
          onEdgesChange={onEdgesChange}
          onConnect={onConnect}
          onNodeClick={onNodeClick}
          fitView
          minZoom={0.18}
          maxZoom={1.7}
          proOptions={{ hideAttribution: true }}
        >
          <Background gap={32} size={1.2} />
          <MiniMap pannable zoomable />
          <Controls showInteractive={false} />
        </ReactFlow>
      </section>
      <button className={`resize-handle${dragging === 'right' ? ' dragging' : ''}`} onPointerDown={() => setDragging('right')} aria-label="调整检查器宽度" />
      <InspectorPanel node={selected} nodes={nodes} resources={{ providers, knowledgeBases, tools, mcpServers, workflows, teams }} onRename={renameSelected} onUpdateConfig={updateSelectedConfig} onFocus={() => setInspectorFullscreen(true)} onCollapse={() => setRightCollapsed(true)} />
      <Modal open={inspectorFullscreen} title="节点配置" onClose={() => setInspectorFullscreen(false)}>
        <InspectorPanel node={selected} nodes={nodes} resources={{ providers, knowledgeBases, tools, mcpServers, workflows, teams }} onRename={renameSelected} onUpdateConfig={updateSelectedConfig} onFocus={() => setInspectorFullscreen(false)} />
      </Modal>
      <RunObservatory open={runOpen} running={running} events={runEvents} onClose={() => setRunOpen(false)} onStop={stopRun} />
    </div>
  );
}

export function CanvasPage() {
  return <CanvasWorkspace />;
}
