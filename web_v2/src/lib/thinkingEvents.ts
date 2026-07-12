import { useThinkingStore, type ThinkingMode } from '@/stores/thinkingStore';
import type { RuntimeEvent, RunDoneEvent } from '@/types/events';

/**
 * 把工作流运行的 SSE 事件映射为"活手稿"背景的思考状态。
 * 只负责状态推导，不承载任何视觉细节。
 */
export function applyRuntimeEventToThinking(event: RuntimeEvent | RunDoneEvent | { error: string }): void {
  const store = useThinkingStore.getState();
  if ('error' in event) {
    store.setMode('error');
    return;
  }
  if ('run' in event) {
    // 运行结束：先沉静再回到空闲，避免突然停止。
    store.setMode('settling');
    window.setTimeout(() => {
      if (useThinkingStore.getState().mode === 'settling') store.setMode('idle');
    }, 2600);
    return;
  }
  const type = (event as RuntimeEvent).type;
  const nodeType = (event as RuntimeEvent).node_type;
  const next = mapRuntimeType(type, nodeType);
  if (next) {
    store.setMode(next);
    if (type === 'node_finished' || type === 'llm_finished' || type === 'retrieval_finished') store.pulse();
  }
}

function mapRuntimeType(type: string, nodeType?: string): ThinkingMode | null {
  switch (type) {
    case 'workflow_started':
      return 'running';
    case 'retrieval_started':
    case 'retrieval_finished':
      return 'retrieval';
    case 'llm_started':
    case 'llm_delta':
    case 'llm_finished':
      return 'thinking';
    case 'node_started':
    case 'node_finished':
      return nodeToMode(nodeType);
    case 'node_failed':
    case 'workflow_failed':
      return 'error';
    case 'workflow_finished':
      return 'settling';
    default:
      return null;
  }
}

function nodeToMode(nodeType?: string): ThinkingMode {
  switch (nodeType) {
    case 'knowledge_retrieval':
    case 'memory_read':
      return 'retrieval';
    case 'http_tool':
    case 'mcp_tool':
    case 'code_sandbox':
      return 'tool';
    case 'llm':
    case 'prompt':
    case 'agent_loop':
    case 'switch':
      return 'thinking';
    default:
      return 'running';
  }
}

/** Chat 流式事件（RAG）到思考状态的映射。 */
export function applyChatEventToThinking(type: string): void {
  const store = useThinkingStore.getState();
  switch (type) {
    case 'retrieval':
      store.setMode('retrieval');
      break;
    case 'delta':
      store.setMode('thinking');
      break;
    case 'done':
      store.setMode('settling');
      window.setTimeout(() => {
        if (useThinkingStore.getState().mode === 'settling') store.setMode('idle');
      }, 2200);
      break;
    case 'error':
      store.setMode('error');
      break;
    default:
      break;
  }
}
