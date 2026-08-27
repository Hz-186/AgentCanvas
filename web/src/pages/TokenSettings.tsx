import { FormEvent, useEffect, useState } from 'react';
import { KeyRound, ShieldCheck, Trash2 } from 'lucide-react';
import { settingsApi } from '../api/resources';
import { Button, EmptyState, Field, IconButton, Modal, Panel, StatusBadge, TextInput, Toast } from '../components/ui';
import type { ApiToken } from '../types/api';
import { formatDate, friendlyErrorMessage } from '../utils/format';

const scopeOptions = [
  ['agent:read', '读取 Agent 与 Run'], ['agent:write', '创建和控制 Agent Run'],
  ['run:read', '读取 Run 与事件'], ['run:write', '取消、恢复和审批 Run'],
  ['resource:read', '读取 Provider、Tool、Skill、知识库'], ['resource:write', '修改 Provider、Tool、Skill、知识库'],
  ['memory:read', '读取 Memory 审计信息'],
] as const;

function maskSecret(value: string) {
  const token = value.trim();
  return token.length <= 10 ? `${token.slice(0, 4)}********` : `${token.slice(0, 8)}********${token.slice(-6)}`;
}

export function TokenSettings() {
  const [tokens, setTokens] = useState<ApiToken[]>([]);
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [scopes, setScopes] = useState<string[]>(['agent:read', 'run:read', 'resource:read', 'memory:read']);
  const [createdToken, setCreatedToken] = useState('');
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  async function load() {
    try {
      setTokens((await settingsApi.tokens.list()).filter((token) => !token.revoked_at));
      setError('');
    } catch (err) {
      setError(friendlyErrorMessage(err, '加载 API Token 失败'));
    }
  }

  useEffect(() => { void load(); }, []);

  async function createToken(event: FormEvent) {
    event.preventDefault();
    try {
      const created = await settingsApi.tokens.create({ name, scopes });
      setCreatedToken(maskSecret(created.token));
      setName('');
      setOpen(false);
      setMessage('API Token 已创建');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '创建 API Token 失败'));
    }
  }

  async function removeToken(id: number) {
    try {
      await settingsApi.tokens.remove(id);
      setCreatedToken('');
      setMessage('API Token 已撤销');
      await load();
    } catch (err) {
      setError(friendlyErrorMessage(err, '撤销 API Token 失败'));
    }
  }

  return (
    <>
      {error ? <p className="error-text">{error}</p> : null}
      <Panel className="management-panel section-access" title="访问令牌" eyebrow="Access" action={<Button tone="primary" onClick={() => setOpen(true)}><KeyRound size={16} />Create</Button>}>
        <div className="stack">
          {tokens.length === 0 ? <EmptyState title="还没有访问令牌" description="创建令牌后，可用于外部服务访问当前 API。" /> : tokens.map((token) => (
            <article className="card" key={token.id}><div className="card-title"><h3 className="truncate">{token.name}</h3><StatusBadge tone="good">有效</StatusBadge></div><p className="muted">{token.token_prefix}******** · {formatDate(token.created_at)}</p><IconButton label="撤销 Token" onClick={() => void removeToken(token.id)}><Trash2 size={16} /></IconButton></article>
          ))}
          {createdToken ? <pre className="code-box">{createdToken}</pre> : null}
        </div>
      </Panel>
      <Modal open={open} title="创建 API Token" onClose={() => setOpen(false)} footer={<><Button type="button" onClick={() => setOpen(false)}>取消</Button><Button form="create-token-form" tone="primary">创建</Button></>}>
        <form id="create-token-form" className="form-stack" onSubmit={(event) => void createToken(event)}>
          <Field label="Token 名称"><TextInput value={name} onChange={(event) => setName(event.target.value)} required /></Field>
          <fieldset className="form-stack"><legend>作用域</legend>{scopeOptions.map(([scope, label]) => <label className="row" key={scope}><input type="checkbox" checked={scopes.includes(scope)} onChange={(event) => setScopes((current) => event.target.checked ? [...new Set([...current, scope])] : current.filter((item) => item !== scope))} />{scope} · {label}</label>)}</fieldset>
          <div className="row muted"><ShieldCheck size={16} /> 至少选择一个作用域；新 Token 不再默认获得全局权限。</div>
        </form>
      </Modal>
      <Toast message={message} tone="good" />
    </>
  );
}
