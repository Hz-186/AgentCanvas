import type {
	ApprovalPayload,
	RunStreamEvent,
	StatusPayload,
	TerminalSnapshotPayload,
	UsagePayload,
	WorkspaceUpdatePayload,
} from '../types/events';
import type { RunEvent } from '../types/api';

export type RunSegmentKind = 'assistant' | 'reasoning' | 'tool' | 'status';

export interface RunSegment {
	id: string;
	kind: RunSegmentKind;
	text: string;
	callId?: string;
	toolName?: string;
	status?: string;
	errorCode?: string;
	truncated?: boolean;
}

export type RunLifecycle = 'idle' | 'running' | 'waiting' | 'paused' | 'succeeded' | 'failed' | 'cancelled';

export interface RunState {
	runId: number | null;
	generation: number;
	lastSeq: number;
	lifecycle: RunLifecycle;
	segments: RunSegment[];
	approval: ApprovalPayload | null;
	usage: UsagePayload | null;
	status: StatusPayload | null;
	workspace: WorkspaceUpdatePayload | null;
	terminalSnapshot: TerminalSnapshotPayload | null;
}

export type RunAction =
	| { type: 'reset'; runId: number | null; generation: number }
	| { type: 'event'; event: RunStreamEvent | RunEvent; generation?: number };

export const emptyRunState = (runId: number | null = null, generation = 0): RunState => ({
	runId,
	generation,
	lastSeq: 0,
	lifecycle: runId == null ? 'idle' : 'running',
	segments: [],
	approval: null,
	usage: null,
	status: null,
	workspace: null,
	terminalSnapshot: null,
});

function isV1Event(event: RunStreamEvent | RunEvent): event is RunStreamEvent {
	return 'kind' in event && event.version === 1;
}

function eventSequence(event: RunStreamEvent | RunEvent): number {
	if (isV1Event(event)) return event.seq;
	return event.id;
}

function eventRunID(event: RunStreamEvent | RunEvent): number {
	return event.run_id;
}

function legacyPayload(event: RunEvent): Record<string, unknown> {
	if (!event.payload_json) return {};
	try {
		const parsed = JSON.parse(event.payload_json) as unknown;
		return parsed && typeof parsed === 'object' ? parsed as Record<string, unknown> : {};
	} catch {
		return {};
	}
}

function stringValue(value: unknown): string {
	return typeof value === 'string' ? value : value == null ? '' : JSON.stringify(value);
}

function upsertSegment(segments: RunSegment[], next: RunSegment, append = false): RunSegment[] {
	const index = segments.findIndex((segment) => segment.id === next.id);
	if (index < 0) return [...segments, next];
	const current = segments[index];
	const merged = {
		...current,
		...next,
		text: append ? current.text + next.text : next.text || current.text,
	};
	const result = [...segments];
	result[index] = merged;
	return result;
}

function beginActivity(state: RunState): void {
	if (state.terminalSnapshot) {
		state.terminalSnapshot = null;
		state.segments = [];
	}
	state.approval = null;
	state.lifecycle = 'running';
}

function terminalLifecycle(kind: string): RunLifecycle | null {
	switch (kind) {
	case 'run.complete': return 'succeeded';
	case 'run.failed': return 'failed';
	case 'run.cancelled': return 'cancelled';
	case 'run.paused': return 'paused';
	case 'run.waiting': return 'waiting';
	default: return null;
	}
}

function snapshotSegments(snapshot: TerminalSnapshotPayload): RunSegment[] {
	const message = snapshot?.message;
	if (!message || message.role !== 'assistant' || !message.content) return [];
	return [{
		id: `terminal:${message.id}`,
		kind: 'assistant',
		text: message.content,
	}];
}

function lifecycleFromRunStatus(status: string | undefined): RunLifecycle | null {
	switch (status) {
	case 'succeeded': return 'succeeded';
	case 'failed':
	case 'timeout': return 'failed';
	case 'cancelled': return 'cancelled';
	case 'paused': return 'paused';
	case 'waiting_human': return 'waiting';
	case 'queued':
	case 'running':
	case 'resuming': return 'running';
	default: return null;
	}
}

function normalizeLegacy(event: RunEvent): RunStreamEvent | null {
	const payload = legacyPayload(event);
	const seq = event.id;
	const base = { version: 1 as const, run_id: event.run_id, seq, created_at: event.created_at };
	const segmentID = typeof payload.segment_id === 'string' ? payload.segment_id : `legacy:${seq}`;
	const text = typeof payload.text === 'string' ? payload.text : typeof payload.content === 'string' ? payload.content : '';
	switch (event.event_type) {
	case 'llm_delta':
		return { ...base, kind: 'assistant.delta', data: { segment_id: segmentID, text } };
	case 'tool_started':
		return { ...base, kind: 'tool.start', data: { call_id: String(payload.tool_call_id ?? ''), segment_id: segmentID, name: String(payload.tool_name ?? ''), status: 'running' } };
	case 'tool_finished':
		return { ...base, kind: 'tool.complete', data: { call_id: String(payload.tool_call_id ?? ''), segment_id: segmentID, name: String(payload.tool_name ?? ''), status: 'succeeded', output: payload.output ?? text } };
	case 'tool_failed':
		return { ...base, kind: 'tool.error', data: { call_id: String(payload.tool_call_id ?? ''), segment_id: segmentID, name: String(payload.tool_name ?? ''), status: 'failed', output: payload.error ?? text, error_code: typeof payload.error_code === 'string' ? payload.error_code : undefined } };
	case 'agent_finished':
		return { ...base, kind: 'run.complete', data: payload as never };
	case 'agent_failed':
		return { ...base, kind: 'run.failed', data: payload as never };
	default:
		return null;
	}
}

export function runReducer(state: RunState, action: RunAction): RunState {
	if (action.type === 'reset') return emptyRunState(action.runId, action.generation);
	if (action.generation != null && action.generation !== state.generation) return state;
	const incoming = isV1Event(action.event) ? action.event : normalizeLegacy(action.event);
	if (!incoming || eventRunID(incoming) !== state.runId) return state;
	if (['succeeded', 'failed', 'cancelled'].includes(state.lifecycle) && incoming.kind !== 'stream.snapshot') return state;
	const seq = eventSequence(incoming);
	if (seq <= state.lastSeq) return state;
	const next: RunState = { ...state, lastSeq: seq };
	const terminal = terminalLifecycle(incoming.kind);
	if (terminal) {
		next.lifecycle = terminal;
		next.terminalSnapshot = incoming.data as TerminalSnapshotPayload;
		next.segments = snapshotSegments(next.terminalSnapshot);
		const snapshotUsage = next.terminalSnapshot?.usage;
		if (snapshotUsage) next.usage = snapshotUsage;
		return next;
	}
	switch (incoming.kind) {
	case 'assistant.start':
	case 'assistant.delta':
	case 'assistant.end': {
		const data = incoming.data;
		beginActivity(next);
		next.segments = upsertSegment(next.segments, { id: data.segment_id, kind: 'assistant', text: data.text ?? '' }, incoming.kind === 'assistant.delta');
		break;
	}
	case 'reasoning.start':
	case 'reasoning.delta':
	case 'reasoning.end': {
		const data = incoming.data;
		beginActivity(next);
		next.segments = upsertSegment(next.segments, { id: data.segment_id, kind: 'reasoning', text: data.text ?? '' }, incoming.kind === 'reasoning.delta');
		break;
	}
	case 'tool.start':
	case 'tool.progress':
	case 'tool.complete':
	case 'tool.error': {
		const data = incoming.data;
		beginActivity(next);
		next.segments = upsertSegment(next.segments, {
			id: data.segment_id,
			kind: 'tool',
			text: stringValue(data.output),
			callId: data.call_id,
			toolName: data.name,
			status: data.status,
			errorCode: data.error_code,
			truncated: data.truncated,
		});
		break;
	}
	case 'status.update':
		next.status = incoming.data;
		next.segments = upsertSegment(next.segments, { id: `status:${seq}`, kind: 'status', text: incoming.data.message });
		break;
	case 'workspace.update':
		next.workspace = incoming.data;
		next.segments = upsertSegment(next.segments, { id: `workspace:${seq}`, kind: 'status', text: 'Workspace 状态已更新' });
		break;
	case 'approval.required':
		next.approval = incoming.data;
		next.lifecycle = 'waiting';
		break;
	case 'request_user_input':
		beginActivity(next);
		next.segments = upsertSegment(next.segments, {
			id: `request_user_input:${seq}`, kind: 'status',
			text: incoming.data.reason || 'Agent 请求补充信息',
		});
		break;
	case 'usage.update':
		next.usage = incoming.data;
		break;
	case 'stream.snapshot':
		next.terminalSnapshot = incoming.data;
		next.usage = incoming.data.usage;
		next.segments = snapshotSegments(incoming.data);
		next.lifecycle = lifecycleFromRunStatus(incoming.data.run?.status) ?? next.lifecycle;
		break;
	}
	return next;
}
