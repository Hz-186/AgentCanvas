import { useCallback, useEffect, useMemo, useState } from 'react';
import { useParams } from 'react-router-dom';
import {
  Background,
  Controls,
  Handle,
  MiniMap,
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
import { Bot, BrainCircuit, Database, MessageSquare, Play, Save, Send, Sparkles, Workflow } from 'lucide-react';
import { agentApi, knowledgeApi, settingsApi } from '../api/resources';
import { Button, EmptyState, Field, Panel, Segmented, Select, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
import type { Agent, FlowVersion, KnowledgeBase, ModelProvider } from '../types/api';
import type { DSLEdge, DSLNode, FlowDSL, NodeConfig, NodeType } from '../types/flow';
import type { RuntimeEvent } from '../types/events';
import { parseJsonObject, prettyJson } from '../utils/format';

interface CanvasNodeData extends Record<string, unknown> {
  label: string;
  nodeType: NodeType;
  config: NodeConfig & { _ui?: { x: number; y: number } };
}

type CanvasNode = Node<CanvasNodeData>;

const nodeMeta: Record<NodeType, { label: string; icon: React.ElementType; description: string }> = {
  begin: { label: 'Begin', icon: Sparkles, description: '读取运行输入' },
  knowledge_retrieval: { label: 'Retrieval', icon: Database, description: '从知识库检索上下文' },
  prompt: { label: 'Prompt', icon: MessageSquare, description: '组装提示词' },
  llm: { label: 'LLM', icon: BrainCircuit, description: '调用模型生成内容' },
  message: { label: 'Message', icon: Send, description: '输出或写入会话消息' },
};

function defaultConfig(type: NodeType): CanvasNodeData['config'] {
  if (type === 'begin') return { input_schema: { query: 'string' } };
  if (type === 'knowledge_retrieval') return { kb_ids: [], top_k: 5, mode: 'keyword', query: '{{sys.query}}' };
  if (type === 'prompt') return { template: '请根据以下上下文回答用户问题：\n\n{{retrieve.context}}\n\n问题：{{sys.query}}' };
  if (type === 'llm') return { provider_id: 0, model: '', temperature: 0.7, stream: true };
  return { content: '{{llm.content}}', with_citation: true };
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

function normalizeDSL(raw: unknown): FlowDSL | null {
  if (!raw) return null;
  const value = typeof raw === 'string' ? JSON.parse(raw) : raw;
  if (!value || typeof value !== 'object') return null;
  const maybe = value as Partial<FlowDSL>;
  if (!Array.isArray(maybe.nodes) || !Array.isArray(maybe.edges)) return null;
  return maybe as FlowDSL;
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
    if (node.data.nodeType === 'message' && !String(config.content ?? '').trim()) return 'Message 节点需要内容';
  }
  return '';
}

function nodeTitle(type: NodeType, nodes: CanvasNode[]) {
  const base = nodeMeta[type].label.toLowerCase();
  if (type === 'begin') return 'begin';
  let i = 1;
  let id = base;
  while (nodes.some((node) => node.id === id)) {
    i += 1;
    id = `${base}_${i}`;
  }
  return id;
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
  const [knowledgeBases, setKnowledgeBases] = useState<KnowledgeBase[]>([]);
  const [mode, setMode] = useState<'config' | 'debug' | 'dsl'>('config');
  const [runInput, setRunInput] = useState('{\n  "query": "请总结知识库内容"\n}');
  const [events, setEvents] = useState<RuntimeEvent[]>([]);
  const [runOutput, setRunOutput] = useState<Record<string, unknown> | null>(null);
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  const selected = useMemo(() => nodes.find((node) => node.id === selectedId) ?? null, [nodes, selectedId]);
  const dsl = useMemo(() => toDSL(agentId, nodes, edges), [agentId, nodes, edges]);

  useEffect(() => {
    async function load() {
      const [agentResp, providersResp, kbResp] = await Promise.all([
        agentApi.get(agentId),
        settingsApi.providers.list(),
        knowledgeApi.list(),
      ]);
      setAgent(agentResp);
      setProviders(providersResp);
      setKnowledgeBases(kbResp);
      if (agentResp.current_version_id) {
        const flow = await agentApi.getFlowVersion(agentResp.current_version_id);
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
    if (agentId > 0) void load().catch((err) => setError(err instanceof Error ? err.message : '加载画布失败'));
  }, [agentId]);

  const onNodesChange = useCallback((changes: NodeChange<CanvasNode>[]) => setNodes((current) => applyNodeChanges(changes, current)), []);
  const onEdgesChange = useCallback((changes: EdgeChange[]) => setEdges((current) => applyEdgeChanges(changes, current)), []);
  const onConnect = useCallback((connection: Connection) => setEdges((current) => addEdge(connection, current)), []);

  function addNode(type: NodeType) {
    if (type === 'begin' && nodes.some((node) => node.data.nodeType === 'begin')) {
      setError('Begin 节点已经存在');
      return;
    }
    const nodeId = nodeTitle(type, nodes);
    const node: CanvasNode = {
      id: nodeId,
      type: 'agentNode',
      position: { x: 180 + nodes.length * 38, y: 120 + nodes.length * 34 },
      data: { label: nodeMeta[type].label, nodeType: type, config: defaultConfig(type) },
    };
    setNodes((current) => [...current, node]);
    if (selectedId) setEdges((current) => [...current, { id: `edge-${selectedId}-${nodeId}`, source: selectedId, target: nodeId }]);
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

  async function saveFlow(): Promise<FlowVersion | null> {
    const localError = validateLocal(nodes, edges);
    if (localError) {
      setError(localError);
      return null;
    }
    const saved = await agentApi.createFlowVersion(agentId, { dsl_json: dsl, description: 'Saved from visual canvas' });
    setVersion(saved);
    setMessage(`已保存 Flow v${saved.version_no}`);
    setError('');
    return saved;
  }

  async function publishFlow() {
    const target = version ?? await saveFlow();
    if (!target) return;
    const published = await agentApi.publishFlowVersion(target.id);
    setVersion(published);
    setMessage(`已发布 v${published.version_no}`);
  }

  async function runDebug() {
    try {
      const input = parseJsonObject(runInput);
      setEvents([]);
      setRunOutput(null);
      setError('');
      await agentApi.streamRun(agentId, { flow_version_id: version?.id ?? 0, input }, {
        onMessage: (msg) => {
          if (msg.event === 'done') {
            const done = JSON.parse(msg.data) as { output: Record<string, unknown> };
            setRunOutput(done.output);
            return;
          }
          if (msg.event === 'error') {
            setError(msg.data);
            return;
          }
          const data = JSON.parse(msg.data) as RuntimeEvent;
          setEvents((current) => [...current, data]);
        },
        onError: (err) => setError(err.message),
      });
    } catch (err) {
      setError(err instanceof Error ? err.message : '调试失败');
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

      <div className="canvas-body">
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

        <section className="flow-surface">
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            onNodesChange={onNodesChange}
            onEdgesChange={onEdgesChange}
            onConnect={onConnect}
            onNodeClick={(_, node) => setSelectedId(node.id)}
            fitView
          >
            <Background />
            <MiniMap pannable zoomable />
            <Controls />
          </ReactFlow>
        </section>

        <aside className="config-panel">
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
              {selected.data.nodeType === 'message' && (
                <Field label="输出内容">
                  <TextArea value={String(config.content ?? '')} onChange={(event) => updateSelectedConfig({ content: event.target.value })} />
                </Field>
              )}
              {selected.data.nodeType === 'begin' && (
                <EmptyState icon={<Workflow size={22} />} title="Begin 会透传运行输入" description="调试台默认使用 query 字段，后续节点可通过 {{sys.query}} 引用。" />
              )}
              {error ? <p className="error-text">{error}</p> : null}
            </Panel>
          ) : null}

          {mode === 'debug' && (
            <Panel title="调试台" eyebrow="SSE Runtime">
              <Field label="运行输入 JSON">
                <TextArea value={runInput} onChange={(event) => setRunInput(event.target.value)} />
              </Field>
              <Button tone="primary" onClick={() => void runDebug()}>
                <Play size={16} />
                运行
              </Button>
              {error ? <p className="error-text">{error}</p> : null}
              <div className="stack">
                {events.map((event, index) => (
                  <div className="card" key={`${event.type}-${index}`}>
                    <div className="card-title">
                      <h3 className="truncate">{event.type}</h3>
                      <StatusBadge tone={event.type.includes('failed') ? 'bad' : 'info'}>{event.node_type || 'workflow'}</StatusBadge>
                    </div>
                    <pre className="code-box">{prettyJson(event.payload ?? {})}</pre>
                  </div>
                ))}
                {runOutput ? <pre className="code-box">{prettyJson(runOutput)}</pre> : null}
              </div>
            </Panel>
          )}

          {mode === 'dsl' && (
            <Panel title="Flow DSL" eyebrow="schema v1">
              <pre className="code-box">{prettyJson(dsl)}</pre>
            </Panel>
          )}
        </aside>
      </div>

      <Toast message={message} tone="good" />
      {!selected && nodes.length === 0 ? <EmptyState icon={<Bot size={24} />} title="画布为空" /> : null}
    </div>
  );
}
