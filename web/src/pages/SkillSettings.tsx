import { FormEvent, useEffect, useState } from 'react';
import { Pencil, Sparkles, Trash2, Zap } from 'lucide-react';
import { resourceSummaryApi, settingsApi } from '../api/resources';
import { Button, EmptyState, Field, IconButton, Modal, Panel, Select, StatusBadge, TextArea, TextInput, Toast } from '../components/ui';
import type { Skill } from '../types/api';
import { formatDate, friendlyErrorMessage } from '../utils/format';

const defaultContent = '## When To Use\n\nUse this skill when ...\n\n## Steps\n\n1. Inspect the current context.\n2. Decide whether these instructions apply.\n3. Execute the steps using available tools.\n\n## Safety\n\nDo not perform write or external actions without explicit approval.';

export function SkillSettings() {
  const [skills, setSkills] = useState<Skill[]>([]);
  const [open, setOpen] = useState(false);
  const [editingId, setEditingId] = useState(0);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [sourceType, setSourceType] = useState<'inline' | 'local_path'>('inline');
  const [content, setContent] = useState(defaultContent);
  const [bundlePath, setBundlePath] = useState('');
  const [tags, setTags] = useState('');
  const [message, setMessage] = useState('');
  const [error, setError] = useState('');

  async function load() {
    try {
      const page = await resourceSummaryApi.list('skills', { limit: 100 });
      setSkills(page.items.map((item) => ({ id: item.id, owner_id: 0, name: item.name, description: item.description ?? '', skill_type: item.resource_type === 'bundle' ? 'bundle' : 'instruction', source_type: 'inline', entry_file: 'SKILL.md', status: item.status ?? 1, version: 0, checksum: '', created_at: item.updated_at, updated_at: item.updated_at })));
      setError('');
    } catch (err) { setError(friendlyErrorMessage(err, '加载 Skill 失败')); }
  }
  useEffect(() => { void load(); }, []);

  function openCreate() {
    setEditingId(0); setName(''); setDescription(''); setSourceType('inline'); setContent(defaultContent); setBundlePath(''); setTags(''); setOpen(true);
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    const body = { name, description, source_type: sourceType, content_md: sourceType === 'inline' ? content : undefined, bundle_path: sourceType === 'local_path' ? bundlePath : undefined, tags: tags.split(',').map((item) => item.trim()).filter(Boolean) };
    try {
      if (editingId) await settingsApi.skills.update(editingId, body); else await settingsApi.skills.create(body);
      setOpen(false); setMessage(editingId ? 'Skill 已更新' : 'Skill 已创建'); await load();
    } catch (err) { setError(friendlyErrorMessage(err, '保存 Skill 失败')); }
  }

  async function edit(summary: Skill) {
    try {
      const item = await settingsApi.skills.get(summary.id);
      setEditingId(item.id); setName(item.name); setDescription(item.description ?? ''); setSourceType(item.source_type); setContent(item.content_md ?? ''); setBundlePath(item.bundle_path ?? ''); setTags(Array.isArray(item.tags_json) ? item.tags_json.join(', ') : ''); setOpen(true);
    } catch (err) { setError(friendlyErrorMessage(err, '加载 Skill 详情失败')); }
  }

  async function remove(id: number) {
    try { await settingsApi.skills.remove(id); setMessage('Skill 已删除'); await load(); }
    catch (err) { setError(friendlyErrorMessage(err, '删除 Skill 失败')); }
  }

  async function validate(id: number) {
    try { const result = await settingsApi.skills.validate(id); setMessage(result.valid ? 'Skill 校验通过' : `Skill 校验失败：${result.error ?? '未知错误'}`); await load(); }
    catch (err) { setError(friendlyErrorMessage(err, '校验 Skill 失败')); }
  }

  return <>{error ? <p className="error-text">{error}</p> : null}<Panel className="management-panel section-skills" title="Skills" eyebrow="Capability" action={<Button tone="primary" onClick={openCreate}><Sparkles size={16} />New</Button>}><div className="stack">{skills.length === 0 ? <EmptyState title="还没有 Skill" description="新增 Skill 后，Agent 可以在运行时按需加载这些说明。" /> : skills.map((item) => <article className="card" key={item.id}><div className="card-title"><h3 className="truncate">{item.name}</h3><StatusBadge tone={item.last_validation_error ? 'bad' : item.status === 1 ? 'good' : 'neutral'}>{item.last_validation_error ? 'validation failed' : item.status === 1 ? 'active' : 'disabled'}</StatusBadge></div><p className="muted truncate">{item.description}</p><p className="muted truncate">{item.source_type} · {item.entry_file}</p><p className="muted truncate">{Array.isArray(item.tags_json) ? item.tags_json.join(', ') : '无标签'} · {item.last_validated_at ? formatDate(item.last_validated_at) : '未校验'}</p>{item.last_validation_error ? <p className="error-text clamp-2">{item.last_validation_error}</p> : null}<div className="row-wrap"><Button onClick={() => void edit(item)}><Pencil size={15} />编辑</Button><Button onClick={() => void validate(item.id)}><Zap size={16} />校验</Button><IconButton label="删除 Skill" onClick={() => void remove(item.id)}><Trash2 size={16} /></IconButton></div></article>)}</div></Panel><Modal open={open} title={editingId ? '编辑 Skill' : '新增 Skill'} onClose={() => setOpen(false)} footer={<><Button type="button" onClick={() => setOpen(false)}>取消</Button><Button form="create-skill-form" tone="primary">保存</Button></>}><form id="create-skill-form" className="form-stack" onSubmit={(event) => void save(event)}><Field label="名称"><TextInput value={name} onChange={(event) => setName(event.target.value)} required /></Field><Field label="描述"><TextArea value={description} onChange={(event) => setDescription(event.target.value)} required /></Field><Field label="Source Type"><Select value={sourceType} onChange={(event) => setSourceType(event.target.value as 'inline' | 'local_path')}><option value="inline">inline</option><option value="local_path">local_path</option></Select></Field>{sourceType === 'inline' ? <Field label="SKILL.md 内容"><TextArea value={content} onChange={(event) => setContent(event.target.value)} required /></Field> : <Field label="Bundle Path"><TextInput value={bundlePath} onChange={(event) => setBundlePath(event.target.value)} required /></Field>}<Field label="Tags" hint="多个标签用英文逗号分隔"><TextInput value={tags} onChange={(event) => setTags(event.target.value)} placeholder="review, repo, safety" /></Field></form></Modal><Toast message={message} tone="good" /></>;
}
