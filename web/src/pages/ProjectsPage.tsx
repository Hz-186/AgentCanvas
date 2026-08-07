import { FormEvent, useEffect, useState } from 'react';
import { FolderGit2, GitBranch, Pencil, Plus, Trash2 } from 'lucide-react';
import { projectApi } from '../api/resources';
import { Button, EmptyState, Field, Modal, StatusBadge, TextInput, Toast } from '../components/ui';
import { EditorialHeader } from '../components/editorial';
import type { GitStatus, GitWorktree, Project } from '../types/api';
import { friendlyErrorMessage } from '../utils/format';

export function ProjectsPage() {
  const [projects, setProjects] = useState<Project[]>([]);
  const [worktrees, setWorktrees] = useState<Record<number, GitWorktree[]>>({});
  const [statuses, setStatuses] = useState<Record<number, GitStatus>>({});
  const [branches, setBranches] = useState<Record<number, string[]>>({});
  const [open, setOpen] = useState(false);
  const [name, setName] = useState('');
  const [path, setPath] = useState('');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [notice, setNotice] = useState('');
  const [folderProject, setFolderProject] = useState<Project | null>(null);
  const [folderPath, setFolderPath] = useState('');
  const [folderLabel, setFolderLabel] = useState('');
  const [folderPrimary, setFolderPrimary] = useState(false);
  const [editProject, setEditProject] = useState<Project | null>(null);
  const [editName, setEditName] = useState('');
  const [editDescription, setEditDescription] = useState('');
  const [editIcon, setEditIcon] = useState('');
  const [editColor, setEditColor] = useState('');

  async function reload() { const items = await projectApi.list(); setProjects(items); return items; }
  useEffect(() => { void reload().catch((cause) => setError(friendlyErrorMessage(cause, '加载项目失败'))); }, []);

  async function create(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError('');
    try { await projectApi.create({ name: name.trim(), primary_path: path.trim(), initialize_git: true }); setOpen(false); setName(''); setPath(''); await reload(); }
    catch (cause) { setError(friendlyErrorMessage(cause, '创建项目失败')); }
    finally { setBusy(false); }
  }

  async function inspect(id: number) {
    try { const [status, value, branchItems] = await Promise.all([projectApi.status(id), projectApi.worktrees(id), projectApi.branches(id)]); setStatuses((current) => ({ ...current, [id]: status })); setWorktrees((current) => ({ ...current, [id]: value })); setBranches((current) => ({ ...current, [id]: branchItems })); }
    catch (cause) { setError(friendlyErrorMessage(cause, '读取 Git Worktree 失败')); }
  }

  function beginEdit(project: Project) {
    setEditProject(project); setEditName(project.name); setEditDescription(project.description); setEditIcon(project.icon); setEditColor(project.color);
  }

  async function updateProject(event: FormEvent) {
    event.preventDefault();
    if (!editProject || !editName.trim()) return;
    setBusy(true); setError('');
    try {
      await projectApi.update(editProject.id, { name: editName.trim(), description: editDescription.trim(), icon: editIcon.trim(), color: editColor.trim() });
      setEditProject(null); await reload();
    } catch (cause) { setError(friendlyErrorMessage(cause, '更新项目失败')); }
    finally { setBusy(false); }
  }

  async function archive(id: number) {
    if (!window.confirm('确认归档这个项目吗？仓库和 Git 分支不会被删除。')) return;
    try { await projectApi.remove(id); await reload(); }
    catch (cause) { setError(friendlyErrorMessage(cause, '归档项目失败')); }
  }

  async function addFolder(event: FormEvent) {
    event.preventDefault();
    if (!folderProject) return;
    setBusy(true); setError('');
    try {
      await projectApi.addFolder(folderProject.id, { path: folderPath.trim(), label: folderLabel.trim(), is_primary: folderPrimary });
      const items = await reload();
      setFolderProject(items.find((item) => item.id === folderProject.id) ?? null);
      setFolderPath(''); setFolderLabel(''); setFolderPrimary(false);
    } catch (cause) { setError(friendlyErrorMessage(cause, '添加项目目录失败')); }
    finally { setBusy(false); }
  }

  async function removeFolder(projectID: number, folderID: number) {
    if (!window.confirm('确认移除这个项目目录吗？磁盘文件不会被删除。')) return;
    try {
      await projectApi.removeFolder(projectID, folderID);
      const items = await reload();
      setFolderProject(items.find((item) => item.id === projectID) ?? null);
    } catch (cause) { setError(friendlyErrorMessage(cause, '移除项目目录失败')); }
  }

  async function copyMergeCommand(project: Project, tree: GitWorktree) {
    if (!tree.branch) return;
    const quote = (value: string) => `'${value.split("'").join(`'"'"'`)}'`;
    const command = `git -C ${quote(project.primary_path)} merge --no-ff ${quote(tree.branch)}`;
    try {
      if (!navigator.clipboard?.writeText) throw new Error('clipboard unavailable');
      await navigator.clipboard.writeText(command);
      setNotice('人工合并命令已复制；AgentCanvas 不会自动执行 merge。');
    } catch (cause) { setError(friendlyErrorMessage(cause, '复制人工合并命令失败')); }
  }

  return <div className="page">
    <EditorialHeader word="Git" script="Projects" kicker="WORKSPACE CONTROL" description="将 Agent 会话绑定到受控仓库，并用独立 worktree 隔离并行子 Agent。" action={<Button tone="primary" onClick={() => setOpen(true)}><Plus size={16} />New Project</Button>} />
    {projects.length === 0 ? <EmptyState icon={<FolderGit2 size={24} />} title="还没有 Git 项目" description="添加允许目录下的仓库后，Agent 才能读取和修改代码。" action={<Button tone="primary" onClick={() => setOpen(true)}>添加项目</Button>} /> : <div className="resource-library-list">
      {projects.map((project) => <article className="resource-library-item" key={project.id}>
        <div className="resource-library-copy"><div className="card-title"><h3>{project.name}</h3><StatusBadge tone={statuses[project.id]?.dirty ? 'warn' : 'good'}>{statuses[project.id] ? `${statuses[project.id].branch || 'detached'}${statuses[project.id].dirty ? ' · dirty' : ''}` : 'Git'}</StatusBadge></div>{project.description ? <p>{project.description}</p> : null}<p className="muted">{project.primary_path}</p><div className="meta-row"><span>{project.slug}</span><span>{project.folders?.length ?? 0} folders</span><span>{statuses[project.id]?.head?.slice(0, 8) || 'status unknown'}</span>{branches[project.id] ? <span>{branches[project.id].length} branches</span> : null}</div>
          {worktrees[project.id]?.map((tree) => <div className="card-title" key={tree.path}><p className="muted"><GitBranch size={13} /> {tree.branch || 'detached'} · {tree.path}{tree.locked ? ' · locked' : ''}</p>{tree.branch ? <Button aria-label={`Copy merge command for ${tree.branch}`} onClick={() => void copyMergeCommand(project, tree)}>Copy merge command</Button> : null}</div>)}
        </div>
        <div className="resource-library-actions"><Button onClick={() => beginEdit(project)}><Pencil size={14} />Edit</Button><Button onClick={() => setFolderProject(project)}>Folders</Button><Button onClick={() => void inspect(project.id)}>Worktrees</Button><Button tone="danger" onClick={() => void archive(project.id)}><Trash2 size={15} />Archive</Button></div>
      </article>)}
    </div>}
    <Modal open={open} title="Create Git Project" onClose={() => setOpen(false)}><form className="stack" onSubmit={(event) => void create(event)}><Field label="Name"><TextInput value={name} onChange={(event) => setName(event.target.value)} /></Field><Field label="Absolute repository path" hint="必须位于服务端 git_workspace.allowed_roots 内"><TextInput value={path} onChange={(event) => setPath(event.target.value)} placeholder="/Users/name/Projects/repo" /></Field><Button tone="primary" type="submit" disabled={busy || !name.trim() || !path.trim()}>Create Project</Button></form></Modal>
    <Modal open={editProject != null} title={`Edit Project · ${editProject?.name ?? ''}`} onClose={() => setEditProject(null)}><form className="stack" onSubmit={(event) => void updateProject(event)}><Field label="Project name"><TextInput value={editName} onChange={(event) => setEditName(event.target.value)} /></Field><Field label="Description"><TextInput value={editDescription} onChange={(event) => setEditDescription(event.target.value)} /></Field><Field label="Icon"><TextInput value={editIcon} onChange={(event) => setEditIcon(event.target.value)} /></Field><Field label="Color"><TextInput value={editColor} onChange={(event) => setEditColor(event.target.value)} /></Field><Button tone="primary" type="submit" disabled={busy || !editName.trim()}>Save Project</Button></form></Modal>
    <Modal open={folderProject != null} title={`Project Folders · ${folderProject?.name ?? ''}`} onClose={() => setFolderProject(null)}><div className="stack">
      {(folderProject?.folders ?? []).map((folder) => <div className="card-title" key={folder.id}><div><strong>{folder.label || 'Folder'}{folder.is_primary ? ' · Primary' : ''}</strong><p className="muted">{folder.path}</p></div><Button tone="danger" disabled={folder.is_primary} onClick={() => void removeFolder(folder.project_id, folder.id)}>Remove</Button></div>)}
      <form className="stack" onSubmit={(event) => void addFolder(event)}><Field label="Folder path"><TextInput value={folderPath} onChange={(event) => setFolderPath(event.target.value)} placeholder="/Users/name/Projects/repo/packages/app" /></Field><Field label="Folder label"><TextInput value={folderLabel} onChange={(event) => setFolderLabel(event.target.value)} placeholder="App" /></Field><label><input type="checkbox" checked={folderPrimary} onChange={(event) => setFolderPrimary(event.target.checked)} /> Set as primary repository path</label><Button tone="primary" type="submit" disabled={busy || !folderPath.trim()}>Add Folder</Button></form>
    </div></Modal>
    {error ? <Toast tone="bad" message={error} onClose={() => setError('')} /> : null}
    {notice ? <Toast tone="good" message={notice} onClose={() => setNotice('')} /> : null}
  </div>;
}
