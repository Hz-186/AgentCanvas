import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { GitStatus, GitWorktree, Project } from '../types/api';
import { ProjectsPage } from './ProjectsPage';

const apiMocks = vi.hoisted(() => ({
  list: vi.fn(),
  create: vi.fn(),
  update: vi.fn(),
  status: vi.fn(),
  branches: vi.fn(),
  worktrees: vi.fn(),
  remove: vi.fn(),
  addFolder: vi.fn(),
  removeFolder: vi.fn(),
}));

vi.mock('../api/resources', () => ({
  projectApi: apiMocks,
}));

const project: Project = {
  id: 11,
  owner_id: 7,
  slug: 'agent-canvas',
  name: 'AgentCanvas',
  description: '',
  icon: '',
  color: '',
  primary_path: '/Users/test/AgentCanvas',
  archived: false,
  folders: [{ id: 91, owner_id: 7, project_id: 11, path: '/Users/test/AgentCanvas', label: 'Primary', is_primary: true, added_at: '2026-08-07T00:00:00Z' }],
  created_at: '2026-08-07T00:00:00Z',
  updated_at: '2026-08-07T00:00:00Z',
};

const status: GitStatus = {
  root: project.primary_path,
  branch: 'main',
  head: '0123456789abcdef',
  dirty: true,
  unpushed: false,
  changed: ['README.md'],
};

const worktree: GitWorktree = {
  path: `${project.primary_path}/.worktrees/42-update-readme`,
  branch: 'agent-canvas/42-update-readme',
  head: 'fedcba9876543210',
  detached: false,
  bare: false,
  locked: true,
  lock_reason: 'run:42 pid:1234',
};

beforeEach(() => {
  vi.clearAllMocks();
  apiMocks.list.mockResolvedValue([project]);
  apiMocks.create.mockResolvedValue(project);
  apiMocks.update.mockResolvedValue(project);
  apiMocks.status.mockResolvedValue(status);
  apiMocks.branches.mockResolvedValue(['main', worktree.branch!]);
  apiMocks.worktrees.mockResolvedValue([worktree]);
  apiMocks.remove.mockResolvedValue({ success: true });
  apiMocks.addFolder.mockResolvedValue({ id: 92, owner_id: 7, project_id: 11, path: '/Users/test/AgentCanvas/web', label: 'Web', is_primary: false, added_at: '2026-08-07T00:00:00Z' });
  apiMocks.removeFolder.mockResolvedValue({ success: true });
  Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: vi.fn().mockResolvedValue(undefined) } });
});

describe('Projects page', () => {
  it('creates a Git-backed project with initialization enabled', async () => {
    render(<ProjectsPage />);
    await screen.findByText('AgentCanvas');

    fireEvent.click(screen.getByRole('button', { name: /New Project/i }));
    fireEvent.change(screen.getByLabelText('Name'), { target: { value: '  Hermes Port  ' } });
    fireEvent.change(screen.getByPlaceholderText('/Users/name/Projects/repo'), { target: { value: '  /Users/test/Hermes Port  ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create Project' }));

    await waitFor(() => expect(apiMocks.create).toHaveBeenCalledWith({
      name: 'Hermes Port',
      primary_path: '/Users/test/Hermes Port',
      initialize_git: true,
    }));
    await waitFor(() => expect(apiMocks.list).toHaveBeenCalledTimes(2));
  });

  it('loads Git status and worktrees on demand', async () => {
    render(<ProjectsPage />);
    await screen.findByText(project.primary_path);

    fireEvent.click(screen.getByRole('button', { name: 'Worktrees' }));

    await waitFor(() => {
      expect(apiMocks.status).toHaveBeenCalledWith(project.id);
      expect(apiMocks.branches).toHaveBeenCalledWith(project.id);
      expect(apiMocks.worktrees).toHaveBeenCalledWith(project.id);
    });
    expect(await screen.findByText(/main · dirty/)).toBeInTheDocument();
    expect(screen.getByText(new RegExp(worktree.branch!))).toHaveTextContent(`${worktree.path} · locked`);
    expect(screen.getByText('01234567')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: `Copy merge command for ${worktree.branch}` }));
    await waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledWith(
      `git -C '${project.primary_path}' merge --no-ff '${worktree.branch}'`,
    ));
    expect(await screen.findByText(/不会自动执行 merge/)).toBeInTheDocument();
  });

  it('updates Project metadata without changing its repository binding', async () => {
    render(<ProjectsPage />);
    await screen.findByText(project.primary_path);

    fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
    fireEvent.change(screen.getByLabelText('Project name'), { target: { value: '  AgentCanvas Next  ' } });
    fireEvent.change(screen.getByLabelText('Description'), { target: { value: '  Git workspace control  ' } });
    fireEvent.change(screen.getByLabelText('Icon'), { target: { value: '  git-branch  ' } });
    fireEvent.change(screen.getByLabelText('Color'), { target: { value: '  #3355ff  ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Save Project' }));

    await waitFor(() => expect(apiMocks.update).toHaveBeenCalledWith(project.id, {
      name: 'AgentCanvas Next',
      description: 'Git workspace control',
      icon: 'git-branch',
      color: '#3355ff',
    }));
  });

  it('adds a configured Project folder and can promote it to primary', async () => {
    render(<ProjectsPage />);
    await screen.findByText(project.primary_path);
    fireEvent.click(screen.getByRole('button', { name: 'Folders' }));

    expect(await screen.findByText('Primary · Primary')).toBeInTheDocument();
    fireEvent.change(screen.getByPlaceholderText('/Users/name/Projects/repo/packages/app'), { target: { value: '  /Users/test/AgentCanvas/web  ' } });
    fireEvent.change(screen.getByPlaceholderText('App'), { target: { value: '  Web  ' } });
    fireEvent.click(screen.getByLabelText('Set as primary repository path'));
    fireEvent.click(screen.getByRole('button', { name: 'Add Folder' }));

    await waitFor(() => expect(apiMocks.addFolder).toHaveBeenCalledWith(project.id, {
      path: '/Users/test/AgentCanvas/web',
      label: 'Web',
      is_primary: true,
    }));
  });
});
