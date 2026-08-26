import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Agent, AgentTurn, Conversation, Message, MessageSearchResult, Run, RunEvent } from '../types/api';
import type { RunStreamEvent } from '../types/events';
import { ChatPage, deduplicateSearchResults, mergeMessages, visibleChatMessages } from './ChatPage';

const apiMocks = vi.hoisted(() => ({
  listAgents: vi.fn(),
  listProviders: vi.fn(),
  listKnowledge: vi.fn(),
  listProjects: vi.fn(),
  listConversations: vi.fn(),
  listMessages: vi.fn(),
  latestTurn: vi.fn(),
  getTurn: vi.fn(),
  streamRunEvents: vi.fn(),
  streamRunEventsV1: vi.fn(),
  searchSessions: vi.fn(),
  updateConversationMode: vi.fn(),
  getRun: vi.fn(),
  listRunEvents: vi.fn(),
  listChildRuns: vi.fn(),
  listApprovalRequests: vi.fn(),
  createConversation: vi.fn(),
  startTurn: vi.fn(),
  workspace: vi.fn(),
  gitStatus: vi.fn(),
  gitDiff: vi.fn(),
  gitLog: vi.fn(),
  gitCommit: vi.fn(),
  refreshWorkspace: vi.fn(),
  cleanupWorkspace: vi.fn(),
  getGoal: vi.fn(),
}));

vi.mock('../api/resources', () => ({
  agentApi: {
    list: apiMocks.listAgents,
    listConversations: apiMocks.listConversations,
    listMessages: apiMocks.listMessages,
    getTurn: apiMocks.getTurn,
    getLatestTurn: apiMocks.latestTurn,
    streamRunEvents: apiMocks.streamRunEvents,
    streamRunEventsV1: apiMocks.streamRunEventsV1,
    searchSessions: apiMocks.searchSessions,
    updateConversationMode: apiMocks.updateConversationMode,
    createConversation: apiMocks.createConversation,
    startTurn: apiMocks.startTurn,
  },
  settingsApi: { providers: { list: apiMocks.listProviders } },
  knowledgeApi: { list: apiMocks.listKnowledge },
  projectApi: { list: apiMocks.listProjects },
  goalApi: { get: apiMocks.getGoal },
  runApi: {
    getRun: apiMocks.getRun,
    listRunEvents: apiMocks.listRunEvents,
    listChildRuns: apiMocks.listChildRuns,
    listApprovalRequests: apiMocks.listApprovalRequests,
    workspace: apiMocks.workspace,
    gitStatus: apiMocks.gitStatus,
    gitDiff: apiMocks.gitDiff,
    gitLog: apiMocks.gitLog,
    gitCommit: apiMocks.gitCommit,
    refreshWorkspace: apiMocks.refreshWorkspace,
    cleanupWorkspace: apiMocks.cleanupWorkspace,
  },
}));

const agent: Agent = {
  id: 1,
  owner_id: 7,
  name: 'Research Agent',
  description: '',
  avatar_url: '',
  status: 'active',
  settings: { provider_id: 4, model: 'gpt-test', system_prompt: 'Answer carefully', knowledge_base_ids: [], temperature: 0.3 },
  created_at: '2026-07-26T00:00:00Z',
  updated_at: '2026-07-26T00:00:00Z',
};

const conversation: Conversation = {
  id: 2,
  owner_id: 7,
  title: 'First task',
  agent_id: 1,
	  agent_mode: 'default',
  last_message_at: '2026-07-26T00:00:03Z',
  created_at: '2026-07-26T00:00:00Z',
  updated_at: '2026-07-26T00:00:03Z',
};

const messages: Message[] = [
  { id: 10, owner_id: 7, conversation_id: 2, role: 'system', content: 'internal state', token_count: 0, created_at: '2026-07-26T00:00:00Z' },
  { id: 11, owner_id: 7, conversation_id: 2, role: 'user', content: 'hello agent', token_count: 2, created_at: '2026-07-26T00:00:01Z' },
  { id: 12, owner_id: 7, conversation_id: 2, role: 'assistant', content: 'hello human', token_count: 2, created_at: '2026-07-26T00:00:02Z' },
];

const activeRun: Run = {
  id: 20,
  owner_id: 7,
  agent_id: 1,
  conversation_id: 2,
  run_type: 'turn',
  delegation_depth: 0,
  status: 'running',
  input_json: '{}',
  output_json: '',
  error_message: '',
  total_tokens: 0,
  latency_ms: 0,
  started_at: '2026-07-26T00:00:03Z',
  created_at: '2026-07-26T00:00:03Z',
  updated_at: '2026-07-26T00:00:03Z',
};

const activeTurn: AgentTurn = {
  id: 30,
  owner_id: 7,
  agent_id: 1,
  conversation_id: 2,
  run_id: activeRun.id,
  user_message_id: 11,
  idempotency_key: 'turn-30',
  status: 'running',
  error_message: '',
  started_at: '2026-07-26T00:00:03Z',
  created_at: '2026-07-26T00:00:03Z',
  updated_at: '2026-07-26T00:00:03Z',
};

const project = {
  id: 44,
  owner_id: 7,
  slug: 'agent-canvas',
  name: 'AgentCanvas',
  description: '',
  repository_root: '/Users/test/AgentCanvas',
  archived: false,
  folders: [],
  created_at: '2026-07-26T00:00:00Z',
  updated_at: '2026-07-26T00:00:00Z',
};

const readyWorkspace = {
  id: 70,
  owner_id: 7,
  project_id: project.id,
  run_id: activeRun.id,
  kind: 'worktree' as const,
  repository_root: project.repository_root,
  workspace_path: `${project.repository_root}/.worktrees/20-first-task`,
  branch_name: 'agent-canvas/20-first-task',
  base_ref: 'origin/main',
  base_sha: 'aaaaaaaaaaaaaaaa',
  head_sha: 'bbbbbbbbbbbbbbbb',
  status: 'ready' as const,
  dirty: false,
  has_unpushed_commits: false,
  locked: true,
  lock_reason: 'run:20 pid:1234',
  cleanup_reason: '',
  error_message: '',
  created_at: '2026-07-26T00:00:03Z',
  updated_at: '2026-07-26T00:00:03Z',
};

function runStreamEvent(runID: number, seq: number, kind: RunStreamEvent['kind'], data: unknown): RunStreamEvent {
  return {
    version: 1,
    run_id: runID,
    conversation_id: 2,
    seq,
    kind,
    created_at: `2026-07-26T00:00:${String(seq).padStart(2, '0')}Z`,
    data,
  } as RunStreamEvent;
}

function renderChat() {
  return render(
    <MemoryRouter initialEntries={['/app/agents/1/chat/2']}>
      <Routes><Route path="/app/agents/:agentId/chat/:conversationId" element={<ChatPage />} /></Routes>
    </MemoryRouter>,
  );
}

function renderNewChat() {
  return render(
    <MemoryRouter initialEntries={['/app/agents/1/chat/new']}>
      <Routes><Route path="/app/agents/:agentId/chat/:conversationId" element={<ChatPage />} /></Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  apiMocks.listAgents.mockResolvedValue([agent]);
  apiMocks.listProviders.mockResolvedValue([{ id: 4, name: 'OpenAI', default_chat_model: 'gpt-test', status: 1 }]);
  apiMocks.listKnowledge.mockResolvedValue([]);
  apiMocks.listProjects.mockResolvedValue([]);
  apiMocks.listConversations.mockResolvedValue([conversation]);
  apiMocks.listMessages.mockResolvedValue(messages);
  apiMocks.latestTurn.mockRejectedValue(new Error('no turn'));
  apiMocks.getGoal.mockResolvedValue(null);
  apiMocks.getTurn.mockResolvedValue(activeTurn);
  apiMocks.streamRunEvents.mockImplementation(() => new Promise<void>(() => undefined));
  apiMocks.streamRunEventsV1.mockImplementation(() => new Promise<void>(() => undefined));
  apiMocks.searchSessions.mockResolvedValue([]);
  apiMocks.updateConversationMode.mockImplementation((_agentID: number, _conversationID: number, mode: Conversation['agent_mode']) => Promise.resolve({ ...conversation, agent_mode: mode }));
  apiMocks.getRun.mockResolvedValue(activeRun);
  apiMocks.listRunEvents.mockResolvedValue([]);
  apiMocks.listChildRuns.mockResolvedValue([]);
  apiMocks.listApprovalRequests.mockResolvedValue([]);
  apiMocks.createConversation.mockResolvedValue({ ...conversation, id: 50, title: '', project_id: project.id, workspace_mode: 'worktree' });
  apiMocks.startTurn.mockResolvedValue({
    turn: { ...activeTurn, id: 51, conversation_id: 50 },
    run: { ...activeRun, id: 52, conversation_id: 50 },
    user_message: { ...messages[1], id: 53, conversation_id: 50, content: 'edit README' },
  });
  apiMocks.workspace.mockResolvedValue(readyWorkspace);
  apiMocks.gitStatus.mockResolvedValue({ root: readyWorkspace.workspace_path, branch: readyWorkspace.branch_name, head: readyWorkspace.head_sha, dirty: false, has_unpushed_commits: false });
  apiMocks.gitDiff.mockResolvedValue({ diff: '' });
  apiMocks.gitLog.mockResolvedValue({ log: 'abc123 feat: update' });
  apiMocks.gitCommit.mockResolvedValue({ hash: 'cccccccc', message: 'feat: update README', paths: ['README.md'] });
  apiMocks.refreshWorkspace.mockResolvedValue(readyWorkspace);
  apiMocks.cleanupWorkspace.mockResolvedValue({
    ...readyWorkspace,
    status: 'cleaned',
    locked: false,
    cleanup_reason: 'checkout removed; branch retained',
  });
  Object.defineProperty(HTMLElement.prototype, 'scrollTo', { configurable: true, value: vi.fn() });
});

describe('Agent chat helpers', () => {
  it('filters non-chat roles before empty-state decisions', () => {
    expect(visibleChatMessages(messages).map((item) => item.id)).toEqual([11, 12]);
  });

  it('keeps the first relevant result for each conversation', () => {
    const results = [
      { conversation_id: 2, content: 'best' },
      { conversation_id: 2, content: 'second' },
      { conversation_id: 3, content: 'another' },
    ] as MessageSearchResult[];
    expect(deduplicateSearchResults(results).map((item) => item.content)).toEqual(['best', 'another']);
  });

  it('reconciles optimistic messages with persistent IDs in timestamp order', () => {
    const optimistic = { ...messages[1], id: -1, content: 'pending' };
    expect(mergeMessages([optimistic].filter((item) => item.id !== optimistic.id), [{ ...messages[1], id: 99, content: 'persisted' }])).toEqual([
      { ...messages[1], id: 99, content: 'persisted' },
    ]);
    expect(mergeMessages([], [{ ...messages[1], id: 99 }, { ...messages[1], id: 99 }])).toHaveLength(1);
  });
});

describe('Agent chat page', () => {
  it('binds a new conversation to the selected Project and worktree mode', async () => {
    apiMocks.listProjects.mockResolvedValue([project]);
    renderNewChat();

    fireEvent.change(await screen.findByLabelText('项目工作区'), { target: { value: String(project.id) } });
    fireEvent.change(screen.getByLabelText('工作区模式'), { target: { value: 'worktree' } });
    fireEvent.change(screen.getByPlaceholderText('交给 Agent 一个任务…'), { target: { value: 'edit README' } });
    fireEvent.click(screen.getByRole('button', { name: '发送' }));

    await waitFor(() => expect(apiMocks.createConversation).toHaveBeenCalledWith(1, undefined, 'default', project.id, 'worktree'));
    await waitFor(() => expect(apiMocks.startTurn).toHaveBeenCalledWith(1, 50, 'edit README', expect.any(String)));
  });

  it('renders user and Agent on opposite message roles without a false empty state', async () => {
    const { container } = renderChat();
    expect(await screen.findByText('hello agent')).toBeInTheDocument();
    expect(screen.getByText('hello human')).toBeInTheDocument();
    expect(screen.queryByText('internal state')).not.toBeInTheDocument();
    expect(screen.queryByText('开始对话')).not.toBeInTheDocument();
    expect(container.querySelector('.message.user')).toHaveTextContent('hello agent');
    expect(container.querySelector('.message.assistant')).toHaveTextContent('hello human');
    expect(screen.queryByText('Goal')).not.toBeInTheDocument();
    expect(screen.queryByText('Role')).not.toBeInTheDocument();
    expect(screen.getByText('全局 System Prompt')).toBeInTheDocument();
    expect(HTMLElement.prototype.scrollTo).toHaveBeenCalled();
  });

  it('submits search from the unified form and de-duplicates conversations', async () => {
    apiMocks.searchSessions.mockResolvedValue([
      { conversation_id: 2, role: 'user', content: 'best summary', created_at: '2026-07-26T00:00:00Z' },
      { conversation_id: 2, role: 'assistant', content: 'duplicate summary', created_at: '2026-07-26T00:00:01Z' },
    ]);
    renderChat();
    const input = await screen.findByLabelText('搜索历史会话');
    fireEvent.change(input, { target: { value: 'summary' } });
    fireEvent.submit(screen.getByRole('search'));
    expect(await screen.findByText('best summary')).toBeInTheDocument();
    expect(screen.queryByText('duplicate summary')).not.toBeInTheDocument();
    expect(apiMocks.searchSessions).toHaveBeenCalledWith(1, 'summary', 30);
    fireEvent.click(screen.getByLabelText('清空搜索'));
    expect(await screen.findByText('First task')).toBeInTheDocument();

    fireEvent.change(input, { target: { value: 'icon' } });
    fireEvent.click(screen.getByLabelText('搜索'));
    await waitFor(() => expect(apiMocks.searchSessions).toHaveBeenCalledWith(1, 'icon', 30));
  });

  it('opens slash commands with the keyboard, persists selection, and remembers inspector collapse', async () => {
    const { container } = renderChat();
    const composer = await screen.findByPlaceholderText('交给 Agent 一个任务…');
    fireEvent.change(composer, { target: { value: '/' } });
    const menu = screen.getByRole('listbox', { name: '选择 Agent 模式' });
    expect(within(menu).getByText(/计划模式/)).toBeInTheDocument();
    fireEvent.keyDown(composer, { key: 'Enter' });
    await waitFor(() => expect(apiMocks.updateConversationMode).toHaveBeenCalledWith(1, 2, 'plan'));
    expect(screen.getByRole('button', { name: '当前模式：计划模式 Plan' })).toBeInTheDocument();

    fireEvent.change(composer, { target: { value: '/default' } });
    fireEvent.keyDown(composer, { key: 'Enter' });
    await waitFor(() => expect(apiMocks.updateConversationMode).toHaveBeenCalledWith(1, 2, 'default'));
    expect(screen.getByRole('button', { name: '当前模式：默认模式 Default' })).toBeInTheDocument();

    fireEvent.change(composer, { target: { value: '/' } });
    fireEvent.keyDown(composer, { key: 'ArrowDown' });
    fireEvent.keyDown(composer, { key: 'Enter' });
    await waitFor(() => expect(apiMocks.updateConversationMode).toHaveBeenLastCalledWith(1, 2, 'default'));

    fireEvent.change(composer, { target: { value: '/' } });
    fireEvent.keyDown(composer, { key: 'Escape' });
    expect(screen.queryByRole('listbox', { name: '选择 Agent 模式' })).not.toBeInTheDocument();
    expect(composer).toHaveValue('');

    fireEvent.click(screen.getByLabelText('收起设置面板'));
    expect(container.querySelector('.chat-shell')).toHaveStyle({ '--dialog-inspector-width': '0px' });
    expect(localStorage.getItem('agentcanvas-agent-inspector-width')).toBe('0');
    fireEvent.click(screen.getByLabelText('打开全局设置'));
    expect(container.querySelector('.chat-shell')).toHaveStyle({ '--dialog-inspector-width': '380px' });
  });

	  it('restores the persisted Todo snapshot with Codex fields', async () => {
    apiMocks.latestTurn.mockResolvedValue({ ...activeTurn, status: 'succeeded' });
    apiMocks.getRun.mockResolvedValue({ ...activeRun, status: 'succeeded' });
    const todoEvent: RunEvent = {
      id: 1,
      owner_id: 7,
      run_id: activeRun.id,
      event_type: 'todo.updated',
	      payload_json: JSON.stringify({
	        explanation: 'working',
	        plan: [
	          { step: 'pending step', status: 'pending' },
	          { step: 'active step', status: 'in_progress' },
	          { step: 'done step', status: 'completed' },
	        ],
	      }),
      created_at: '2026-07-26T00:00:04Z',
    };
    apiMocks.listRunEvents.mockResolvedValue([todoEvent]);

    const { container } = renderChat();

	    expect(await screen.findByText('Updated Plan')).toBeInTheDocument();
	    expect(screen.getByText('working')).toBeInTheDocument();
	    for (const status of ['pending', 'in_progress', 'completed']) {
	      expect(container.querySelector(`.todo-item.${status}`)).not.toBeNull();
	    }
	    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
  });

  it('renders ordered v1 segments and reconciles the terminal snapshot without duplicating the answer', async () => {
    apiMocks.latestTurn.mockResolvedValue(activeTurn);
    let animationFrame: FrameRequestCallback | null = null;
    let animationFrameID = 0;
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      animationFrame = callback;
      animationFrameID += 1;
      return animationFrameID;
    });
    const { container } = renderChat();
    await screen.findByText('hello agent');
    await waitFor(() => expect(apiMocks.streamRunEventsV1).toHaveBeenCalledWith(activeRun.id, undefined, expect.any(Object)));
    const handlers = apiMocks.streamRunEventsV1.mock.calls[0][2] as {
      onMessage: (message: { id?: string; event: string; data: string }) => void;
    };
    const emit = (event: RunStreamEvent) => handlers.onMessage({
      id: String(event.seq),
      event: event.kind,
      data: JSON.stringify(event),
    });
    const flushStreamFrame = () => {
      const callback = animationFrame;
      animationFrame = null;
      if (!callback) throw new Error('expected a pending animation frame');
      callback(performance.now());
    };

    act(() => {
      emit(runStreamEvent(activeRun.id, 1, 'assistant.start', { segment_id: 'answer-before' }));
      emit(runStreamEvent(activeRun.id, 2, 'assistant.delta', { segment_id: 'answer-before', text: 'before tool' }));
      emit(runStreamEvent(activeRun.id, 3, 'tool.start', { call_id: 'call-1', segment_id: 'tool-1', name: 'search', status: 'running' }));
      emit(runStreamEvent(activeRun.id, 4, 'tool.complete', { call_id: 'call-1', segment_id: 'tool-1', name: 'search', status: 'succeeded', output: 'tool result' }));
      emit(runStreamEvent(activeRun.id, 5, 'assistant.start', { segment_id: 'answer-after' }));
      emit(runStreamEvent(activeRun.id, 6, 'assistant.delta', { segment_id: 'answer-after', text: 'after tool' }));
      flushStreamFrame();
    });

    const streamed = Array.from(container.querySelectorAll<HTMLElement>('[data-run-segment]'));
    expect(streamed).toHaveLength(3);
    expect(streamed.map((item) => item.className)).toEqual([
      expect.stringContaining('message assistant pending'),
      expect.stringContaining('chat-trace-row'),
      expect.stringContaining('message assistant pending'),
    ]);
    expect(streamed.map((item) => item.querySelector('p')?.textContent)).toEqual(['before tool', 'tool result', 'after tool']);

    const finalMessage: Message = {
      id: 13,
      owner_id: 7,
      conversation_id: 2,
      role: 'assistant',
      content: 'final answer',
      token_count: 3,
      created_at: '2026-07-26T00:00:10Z',
    };
    const finalRun: Run = { ...activeRun, status: 'succeeded', output_json: '{"answer":"final answer"}', total_tokens: 3 };
    const finalTurn: AgentTurn = { ...activeTurn, status: 'succeeded', assistant_message_id: finalMessage.id };
    act(() => {
      emit(runStreamEvent(activeRun.id, 7, 'run.complete', {
        run: finalRun,
        turn: finalTurn,
        message: finalMessage,
        usage: { prompt_tokens: 1, completion_tokens: 2, total_tokens: 3 },
      }));
      flushStreamFrame();
    });

    await waitFor(() => expect(screen.getAllByText('final answer')).toHaveLength(1));
    expect(screen.getByText('hello agent')).toBeInTheDocument();
    expect(screen.getByText('hello human')).toBeInTheDocument();
    expect(container.querySelectorAll('[data-run-segment]')).toHaveLength(0);
    expect(apiMocks.streamRunEventsV1).toHaveBeenCalled();
    expect(apiMocks.streamRunEvents).not.toHaveBeenCalled();
  });

  it('refreshes the Workspace card after a workspace.update stream event', async () => {
    apiMocks.latestTurn.mockResolvedValue(activeTurn);
    let animationFrame: FrameRequestCallback | null = null;
    vi.spyOn(window, 'requestAnimationFrame').mockImplementation((callback) => {
      animationFrame = callback;
      return 1;
    });
    renderChat();

    await screen.findByText(readyWorkspace.branch_name);
    await waitFor(() => expect(apiMocks.streamRunEventsV1).toHaveBeenCalled());
    const initialWorkspaceLoads = apiMocks.workspace.mock.calls.length;
    const updatedWorkspace = {
      ...readyWorkspace,
      head_sha: 'dddddddddddddddd',
      dirty: true,
      updated_at: '2026-07-26T00:00:08Z',
    };
    apiMocks.workspace.mockResolvedValue(updatedWorkspace);
    apiMocks.gitStatus.mockResolvedValue({
      root: updatedWorkspace.workspace_path,
      branch: updatedWorkspace.branch_name,
      head: updatedWorkspace.head_sha,
      dirty: true,
      has_unpushed_commits: false,
    });
    const handlers = apiMocks.streamRunEventsV1.mock.calls[0][2] as {
      onMessage: (message: { id?: string; event: string; data: string }) => void;
    };

    act(() => {
      const event = runStreamEvent(activeRun.id, 8, 'workspace.update', {
        workspace_id: updatedWorkspace.id,
        run_id: activeRun.id,
        project_id: project.id,
		repository_root: updatedWorkspace.repository_root,
		workspace_path: updatedWorkspace.workspace_path,
		branch_name: updatedWorkspace.branch_name,
        base_sha: updatedWorkspace.base_sha,
        head_sha: updatedWorkspace.head_sha,
        dirty: true,
      has_unpushed_commits: false,
        status: 'ready',
        locked: true,
      });
      handlers.onMessage({ id: String(event.seq), event: event.kind, data: JSON.stringify(event) });
      const callback = animationFrame;
      animationFrame = null;
      if (!callback) throw new Error('expected a pending animation frame');
      callback(performance.now());
    });

    await waitFor(() => expect(apiMocks.workspace.mock.calls.length).toBeGreaterThan(initialWorkspaceLoads));
    expect(await screen.findByText('ready · dirty')).toBeInTheDocument();
    expect(screen.getByText('dddddddd')).toBeInTheDocument();
  });

  it('prefers live Git status over the persisted Workspace snapshot', async () => {
    apiMocks.latestTurn.mockResolvedValue(activeTurn);
    apiMocks.gitStatus.mockResolvedValue({
      root: readyWorkspace.workspace_path,
      branch: readyWorkspace.branch_name,
      head: readyWorkspace.head_sha,
      dirty: true,
      has_unpushed_commits: true,
    });
    renderChat();

    expect(await screen.findByText('ready · dirty · unpushed')).toBeInTheDocument();
  });

  it('keeps the persisted Workspace visible when live Git status fails', async () => {
    apiMocks.latestTurn.mockResolvedValue(activeTurn);
    apiMocks.gitStatus.mockRejectedValue(new Error('Git status unavailable'));
    renderChat();

    expect(await screen.findByText(readyWorkspace.branch_name)).toBeInTheDocument();
    expect(screen.getByText(readyWorkspace.workspace_path)).toBeInTheDocument();
    expect(screen.getByText('ready')).toBeInTheDocument();
  });

  it('loads Git log for the current Workspace', async () => {
    apiMocks.latestTurn.mockResolvedValue(activeTurn);
    renderChat();

    await screen.findByText(readyWorkspace.branch_name);
    fireEvent.click(screen.getByRole('button', { name: 'Log' }));

    await waitFor(() => expect(apiMocks.gitLog).toHaveBeenCalledWith(activeRun.id, 20));
    expect(await screen.findByLabelText('Git log')).toHaveTextContent('abc123 feat: update');
  });

  it('requires an explicit modal confirmation before creating a Git commit', async () => {
    apiMocks.latestTurn.mockResolvedValue(activeTurn);
    renderChat();

    await screen.findByText(readyWorkspace.branch_name);
    fireEvent.click(screen.getByRole('button', { name: 'Commit…' }));
    expect(await screen.findByText(/不会 push、merge 或删除分支/)).toBeInTheDocument();
    expect(apiMocks.gitCommit).not.toHaveBeenCalled();
    fireEvent.change(screen.getByPlaceholderText('feat: update implementation'), { target: { value: '  feat: update README  ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Approve commit' }));

    await waitFor(() => expect(apiMocks.gitCommit).toHaveBeenCalledWith(activeRun.id, 'feat: update README'));
    expect(await screen.findByText(/Commit 已创建，分支仍保留供人工合并/)).toBeInTheDocument();
  });

  it('cleans a safe Worktree checkout while retaining its branch', async () => {
    apiMocks.latestTurn.mockResolvedValue(activeTurn);
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    renderChat();

    await screen.findByText(readyWorkspace.branch_name);
    fireEvent.click(screen.getByRole('button', { name: 'Cleanup' }));

    await waitFor(() => expect(apiMocks.cleanupWorkspace).toHaveBeenCalledWith(readyWorkspace.id));
    expect(await screen.findByText(/Git branch 仍保留供人工审查/)).toBeInTheDocument();
    expect(screen.getByText('cleaned')).toBeInTheDocument();
  });

  it('shows why an unsafe Worktree was preserved instead of removed', async () => {
    apiMocks.latestTurn.mockResolvedValue(activeTurn);
    apiMocks.cleanupWorkspace.mockResolvedValue({
      ...readyWorkspace,
      status: 'preserved',
      dirty: true,
      cleanup_reason: 'workspace contains dirty or unpushed work',
    });
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    renderChat();

    await screen.findByText(readyWorkspace.branch_name);
    fireEvent.click(screen.getByRole('button', { name: 'Cleanup' }));

    expect(await screen.findByText(/Workspace 已保留：workspace contains dirty or unpushed work/)).toBeInTheDocument();
    expect(screen.getByText(/保留原因：workspace contains dirty or unpushed work/)).toBeInTheDocument();
  });
});
