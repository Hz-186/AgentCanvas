import { RUN_STREAM_VERSION, type RunStreamEvent } from '../types/events';

const textKinds = new Set(['assistant.start', 'assistant.delta', 'assistant.end', 'reasoning.start', 'reasoning.delta', 'reasoning.end']);
const toolKinds = new Set(['tool.start', 'tool.progress', 'tool.complete', 'tool.error']);
const terminalKinds = new Set(['stream.snapshot', 'run.complete', 'run.failed', 'run.paused', 'run.waiting', 'run.cancelled']);
const eventKinds = new Set([
  ...textKinds,
  ...toolKinds,
  ...terminalKinds,
  'status.update',
  'approval.required',
  'usage.update',
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

function validateUsage(data: Record<string, unknown>): void {
  requireNumber(data, 'prompt_tokens');
  requireNumber(data, 'completion_tokens');
  requireNumber(data, 'total_tokens');
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
  } else if (envelope.kind === 'usage.update') {
    validateUsage(data);
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
