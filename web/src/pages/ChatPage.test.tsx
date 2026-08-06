import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Agent, AgentTurn, Conversation, Message, MessageSearchResult, Run } from '../types/api';
import type { RunStreamEvent } from '../types/events';
import { ChatPage, deduplicateSearchResults, mergeMessages, visibleChatMessages } from './ChatPage';

const apiMocks = vi.hoisted(() => ({
  listAgents: vi.fn(),
  listProviders: vi.fn(),
  listKnowledge: vi.fn(),
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
  },
  settingsApi: { providers: { list: apiMocks.listProviders } },
  knowledgeApi: { list: apiMocks.listKnowledge },
  runApi: {
    getRun: apiMocks.getRun,
    listRunEvents: apiMocks.listRunEvents,
    listChildRuns: apiMocks.listChildRuns,
    listApprovalRequests: apiMocks.listApprovalRequests,
  },
}));

const agent: Agent = {
  id: 1,
  owner_id: 7,
  name: 'Research Agent',
  description: '',
  avatar_url: '',
  status: 'active',
  settings: { provider_id: 4, model: 'gpt-test', system_prompt: 'Answer carefully', knowledge_ids: [], temperature: 0.3 },
  current_release_id: 9,
  created_at: '2026-07-26T00:00:00Z',
  updated_at: '2026-07-26T00:00:00Z',
};

const conversation: Conversation = {
  id: 2,
  owner_id: 7,
  title: 'First task',
  source: 'agent',
  agent_id: 1,
  agent_release_id: 9,
  agent_mode: 'react',
  last_message_at: '2026-07-26T00:00:03Z',
  created_at: '2026-07-26T00:00:00Z',
  updated_at: '2026-07-26T00:00:03Z',
};

const messages: Message[] = [
  { id: 10, owner_id: 7, conversation_id: 2, role: 'system', content: 'internal state', content_type: 'text', token_count: 0, created_at: '2026-07-26T00:00:00Z' },
  { id: 11, owner_id: 7, conversation_id: 2, role: 'user', content: 'hello agent', content_type: 'text', token_count: 2, created_at: '2026-07-26T00:00:01Z' },
  { id: 12, owner_id: 7, conversation_id: 2, role: 'assistant', content: 'hello human', content_type: 'text', token_count: 2, created_at: '2026-07-26T00:00:02Z' },
];

const activeRun: Run = {
  id: 20,
  owner_id: 7,
  agent_id: 1,
  agent_release_id: 9,
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
  agent_release_id: 9,
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

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  apiMocks.listAgents.mockResolvedValue([agent]);
  apiMocks.listProviders.mockResolvedValue([{ id: 4, name: 'OpenAI', default_chat_model: 'gpt-test', status: 1 }]);
  apiMocks.listKnowledge.mockResolvedValue([]);
  apiMocks.listConversations.mockResolvedValue([conversation]);
  apiMocks.listMessages.mockResolvedValue(messages);
  apiMocks.latestTurn.mockRejectedValue(new Error('no turn'));
  apiMocks.getTurn.mockResolvedValue(activeTurn);
  apiMocks.streamRunEvents.mockImplementation(() => new Promise<void>(() => undefined));
  apiMocks.streamRunEventsV1.mockImplementation(() => new Promise<void>(() => undefined));
  apiMocks.searchSessions.mockResolvedValue([]);
  apiMocks.updateConversationMode.mockResolvedValue({ ...conversation, agent_mode: 'plan_execute' });
  apiMocks.getRun.mockResolvedValue(activeRun);
  apiMocks.listRunEvents.mockResolvedValue([]);
  apiMocks.listChildRuns.mockResolvedValue([]);
  apiMocks.listApprovalRequests.mockResolvedValue([]);
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

  it('opens /mode with the keyboard, persists selection, and remembers inspector collapse', async () => {
    const { container } = renderChat();
    const composer = await screen.findByPlaceholderText('交给 Agent 一个任务…');
    fireEvent.change(composer, { target: { value: '/' } });
    const menu = screen.getByRole('listbox', { name: '选择 Agent 模式' });
    expect(within(menu).getByText('ReAct')).toBeInTheDocument();
    fireEvent.keyDown(composer, { key: 'ArrowDown' });
    fireEvent.keyDown(composer, { key: 'Enter' });
    await waitFor(() => expect(apiMocks.updateConversationMode).toHaveBeenCalledWith(1, 2, 'plan_execute'));
    expect(screen.getByRole('button', { name: '当前模式：Plan Guided' })).toBeInTheDocument();

    fireEvent.click(screen.getByLabelText('收起设置面板'));
    expect(container.querySelector('.chat-shell')).toHaveStyle({ '--dialog-inspector-width': '0px' });
    expect(localStorage.getItem('agentcanvas-agent-inspector-width')).toBe('0');
    fireEvent.click(screen.getByLabelText('打开全局设置'));
    expect(container.querySelector('.chat-shell')).toHaveStyle({ '--dialog-inspector-width': '380px' });
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
      content_type: 'text',
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
});
