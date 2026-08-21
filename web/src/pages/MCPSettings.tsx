import { FormEvent, useEffect, useState } from 'react';
import { PlugZap, Trash2, Zap } from 'lucide-react';
import { settingsApi } from '../api/resources';
import { Button, EmptyState, Field, IconButton, Modal, Panel, Select, StatusBadge, TextInput, Toast } from '../components/ui';
import type { MCPServer, MCPToolCache } from '../types/api';
import { formatDate, friendlyErrorMessage } from '../utils/format';

export function MCPSettings() {
  const [servers, setServers] = useState<MCPServer[]>([]);
  const [tools, setTools] = useState<MCPToolCache[]>([]);
  const [selectedId, setSelectedId] = useState(0);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [transport, setTransport] = useState<'streamable_http' | 'stdio'>('streamable_http');
  const [endpoint, setEndpoint] = useState('');
  const [command, setCommand] = useState('');
  const [args, setArgs] = useState('');
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  async function load() {
    try { setServers(await settingsApi.tools.listMCPServers()); setError(''); }
    catch (err) { setError(friendlyErrorMessage(err, '加载 MCP Server 失败')); }
  }
  useEffect(() => { void load(); }, []);
  useEffect(() => {
    if (!selectedId) { setTools([]); return; }
    void settingsApi.tools.listMCPTools(selectedId).then(setTools).catch((err) => setError(friendlyErrorMessage(err, '加载 MCP 工具缓存失败')));
  }, [selectedId]);

  async function createServer(event: FormEvent) {
    event.preventDefault();
    try {
      const created = await settingsApi.tools.createMCPServer({ name, transport, endpoint_url: transport === 'streamable_http' ? endpoint : undefined, command: transport === 'stdio' ? command : undefined, args_json: transport === 'stdio' ? args.split(/\s+/).map((item) => item.trim()).filter(Boolean) : [], env_json: {} });
      setOpen(false); setName(''); setEndpoint(''); setCommand(''); setArgs(''); setSelectedId(created.id); setMessage('MCP Server 已创建'); await load();
    } catch (err) { setError(friendlyErrorMessage(err, '创建 MCP Server 失败')); }
  }

  async function refreshServer(id: number) {
    try { const response = await settingsApi.tools.refreshMCPServer(id); setTools(response.tools); setSelectedId(id); setMessage(`MCP 工具已刷新：${response.tools.length} 个`); await load(); }
    catch (err) { setError(friendlyErrorMessage(err, '刷新 MCP 工具失败')); }
  }

  async function removeServer(id: number) {
    try { await settingsApi.tools.removeMCPServer(id); if (selectedId === id) setSelectedId(0); setMessage('MCP Server 已删除'); await load(); }
    catch (err) { setError(friendlyErrorMessage(err, '删除 MCP Server 失败')); }
  }

  return (
    <>
      {error ? <p className="error-text">{error}</p> : null}
      <Panel className="management-panel section-mcp" title="MCP Server" eyebrow="External Tools" action={<Button tone="primary" onClick={() => setOpen(true)}><PlugZap size={16} />New</Button>}>
        <div className="stack">{servers.length === 0 ? <EmptyState title="还没有 MCP Server" description="接入 MCP 后，Agent 可以通过统一工具协议扩展能力。" /> : <><Field label="当前 Server"><Select value={selectedId} onChange={(event) => setSelectedId(Number(event.target.value))}><option value={0}>选择 MCP Server</option>{servers.map((server) => <option key={server.id} value={server.id}>{server.name}</option>)}</Select></Field><div className="grid">{servers.map((server) => <article className="card" key={server.id}><div className="card-title"><h3 className="truncate">{server.name}</h3><StatusBadge tone={server.last_error ? 'bad' : server.discovered_at ? 'good' : 'neutral'}>{server.last_error ? '错误' : server.discovered_at ? '已发现' : '未刷新'}</StatusBadge></div><p className="muted truncate">{server.transport === 'streamable_http' ? server.endpoint_url : server.command}</p><p className="muted">发现时间 {formatDate(server.discovered_at)}</p>{server.last_error ? <p className="error-text clamp-2">{server.last_error}</p> : null}<div className="row-wrap"><Button onClick={() => void refreshServer(server.id)}><Zap size={16} />刷新</Button><IconButton label="删除 MCP Server" onClick={() => void removeServer(server.id)}><Trash2 size={16} /></IconButton></div></article>)}</div>{selectedId ? <div className="trace-list">{tools.length === 0 ? <p className="muted">当前 Server 暂无工具缓存，刷新后会显示工具列表。</p> : tools.map((item) => <article className="trace-item" key={item.id}><div className="trace-item-head"><strong>{item.tool_name}</strong><StatusBadge tone="neutral">mcp</StatusBadge></div><p className="muted clamp-2">{item.description || '无描述'}</p></article>)}</div> : null}</>}</div>
      </Panel>
      <Modal open={open} title="新增 MCP Server" onClose={() => setOpen(false)} footer={<><Button type="button" onClick={() => setOpen(false)}>取消</Button><Button form="create-mcp-form" tone="primary">保存</Button></>}><form id="create-mcp-form" className="form-stack" onSubmit={(event) => void createServer(event)}><Field label="名称"><TextInput value={name} onChange={(event) => setName(event.target.value)} required /></Field><Field label="Transport"><Select value={transport} onChange={(event) => setTransport(event.target.value as 'streamable_http' | 'stdio')}><option value="streamable_http">Streamable HTTP</option><option value="stdio">stdio</option></Select></Field>{transport === 'streamable_http' ? <Field label="Endpoint URL"><TextInput value={endpoint} onChange={(event) => setEndpoint(event.target.value)} placeholder="http://localhost:3333" required /></Field> : <><Field label="Command"><TextInput value={command} onChange={(event) => setCommand(event.target.value)} placeholder="node" required /></Field><Field label="Args"><TextInput value={args} onChange={(event) => setArgs(event.target.value)} placeholder="server.js --stdio" /></Field></>}</form></Modal>
      <Toast message={message} tone="good" />
    </>
  );
}
