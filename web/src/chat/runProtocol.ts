import { RUN_STREAM_VERSION, type RunStreamEvent } from '../types/events';

const textKinds = new Set(['assistant.start', 'assistant.delta', 'assistant.end', 'reasoning.start', 'reasoning.delta', 'reasoning.end', 'plan.start', 'plan.delta', 'plan.end']);
const toolKinds = new Set(['tool.start', 'tool.progress', 'tool.complete', 'tool.error']);
const terminalKinds = new Set(['stream.snapshot', 'run.complete', 'run.failed', 'run.paused', 'run.waiting', 'run.cancelled']);
const eventKinds = new Set([
  ...textKinds,
  ...toolKinds,
  ...terminalKinds,
  'status.update',
  'workspace.update',
  'approval.required',
  'usage.update',
	'todo.updated',
	'goal.updated',
	'goal.cleared',
]);

export class RunStreamProtocolError extends Error {
  constructor(message: string) {
    super(message);
    this.name = 'RunStreamProtocolError';
  }
}

function objectValue(value: unknown, path: string): Record<string, unknown> {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new RunStreamProtocolError(`${path} must be an object`);
  return value as Record<string, unknown>;
}

function requireString(value: Record<string, unknown>, key: string): void {
  if (typeof value[key] !== 'string' || value[key] === '') throw new RunStreamProtocolError(`data.${key} must be a non-empty string`);
}

function requireNumber(value: Record<string, unknown>, key: string): void {
  if (typeof value[key] !== 'number' || !Number.isFinite(value[key])) throw new RunStreamProtocolError(`data.${key} must be a number`);
}

function requireBoolean(value: Record<string, unknown>, key: string): void {
  if (typeof value[key] !== 'boolean') throw new RunStreamProtocolError(`data.${key} must be a boolean`);
}

function requireStringValue(value: Record<string, unknown>, key: string): void {
  if (typeof value[key] !== 'string') throw new RunStreamProtocolError(`data.${key} must be a string`);
}

function validateUsage(data: Record<string, unknown>): void {
  requireNumber(data, 'prompt_tokens');
  requireNumber(data, 'completion_tokens');
  requireNumber(data, 'total_tokens');
}

function validateTodo(data: Record<string, unknown>): void {
	if (!Array.isArray(data.plan)) throw new RunStreamProtocolError('data.plan must be an array');
	for (const item of data.plan) {
		const value = objectValue(item, 'data.plan[]');
		requireString(value, 'step');
		if (!['pending', 'in_progress', 'completed'].includes(String(value.status))) throw new RunStreamProtocolError('data.plan[].status is invalid');
	}
	if (data.explanation != null && typeof data.explanation !== 'string') throw new RunStreamProtocolError('data.explanation must be a string');
}

function validateGoal(data: Record<string, unknown>): void {
	if (data.goal == null) return;
	const goal = objectValue(data.goal, 'data.goal');
	if (typeof goal.status !== 'string' || !['active', 'paused', 'blocked', 'usage_limited', 'budget_limited', 'complete'].includes(goal.status)) {
		throw new RunStreamProtocolError('data.goal.status is invalid');
	}
	if (data.message != null && typeof data.message !== 'string') throw new RunStreamProtocolError('data.message must be a string');
}

export function parseRunStreamEvent(input: string | unknown): RunStreamEvent {
  let parsed = input;
  if (typeof input === 'string') {
    try {
      parsed = JSON.parse(input) as unknown;
    } catch {
      throw new RunStreamProtocolError('event must be valid JSON');
    }
  }
  const envelope = objectValue(parsed, 'event');
  if (envelope.version !== RUN_STREAM_VERSION) throw new RunStreamProtocolError('unsupported run stream version');
  if (typeof envelope.run_id !== 'number' || envelope.run_id <= 0) throw new RunStreamProtocolError('run_id must be positive');
  if (typeof envelope.seq !== 'number' || !Number.isSafeInteger(envelope.seq) || envelope.seq <= 0) throw new RunStreamProtocolError('seq must be a positive integer');
  if (typeof envelope.kind !== 'string' || !eventKinds.has(envelope.kind)) throw new RunStreamProtocolError('unknown run stream kind');
  if (typeof envelope.created_at !== 'string' || Number.isNaN(Date.parse(envelope.created_at))) throw new RunStreamProtocolError('created_at must be an ISO timestamp');
  if (envelope.conversation_id != null && (typeof envelope.conversation_id !== 'number' || envelope.conversation_id <= 0)) {
    throw new RunStreamProtocolError('conversation_id must be positive when present');
  }
  const data = objectValue(envelope.data, 'data');
  if (textKinds.has(envelope.kind)) {
    requireString(data, 'segment_id');
    if (data.text != null && typeof data.text !== 'string') throw new RunStreamProtocolError('data.text must be a string');
  } else if (toolKinds.has(envelope.kind)) {
    for (const key of ['call_id', 'segment_id', 'name', 'status']) requireString(data, key);
  } else if (envelope.kind === 'status.update') {
    requireString(data, 'message');
    if (!['info', 'warning', 'error'].includes(String(data.level))) throw new RunStreamProtocolError('data.level is invalid');
	} else if (envelope.kind === 'approval.required') {
		requireNumber(data, 'request_id');
		for (const key of ['call_id', 'tool_name', 'reason']) requireString(data, key);
		if (data.is_blocking != null && typeof data.is_blocking !== 'boolean') throw new RunStreamProtocolError('data.is_blocking must be a boolean');
		if (data.questions != null && !Array.isArray(data.questions)) throw new RunStreamProtocolError('data.questions must be an array');
  } else if (envelope.kind === 'usage.update') {
    validateUsage(data);
	} else if (envelope.kind === 'workspace.update') {
	for (const key of ['workspace_id', 'run_id']) requireNumber(data, key);
	for (const key of ['repository_root', 'workspace_path', 'branch_name', 'base_sha', 'head_sha']) requireStringValue(data, key);
	for (const key of ['dirty', 'has_unpushed_commits']) requireBoolean(data, key);
	} else if (envelope.kind === 'todo.updated') {
		validateTodo(data);
	} else if (envelope.kind === 'goal.updated' || envelope.kind === 'goal.cleared') {
		validateGoal(data);
  } else if (terminalKinds.has(envelope.kind)) {
    objectValue(data.run, 'data.run');
    validateUsage(objectValue(data.usage, 'data.usage'));
  }
  return envelope as unknown as RunStreamEvent;
}

export function isTerminalRunEvent(event: RunStreamEvent): boolean {
  if (event.kind === 'run.complete' || event.kind === 'run.failed' || event.kind === 'run.cancelled') return true;
  if (event.kind !== 'stream.snapshot') return false;
  return ['succeeded', 'failed', 'cancelled', 'timeout'].includes(event.data.run.status);
}
