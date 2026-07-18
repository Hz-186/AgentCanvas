// SSE over fetch：因为 SSE 需带 Authorization header（EventSource 不支持），
// 这里用 fetch + ReadableStream 手动解析 `event:` / `data:` 帧。

import { API_BASE } from './client';
import { tokenStorage } from './token';

export interface SSEMessage {
  id?: string;
  event: string;
  data: string;
}

// 纯函数式增量解析器：喂入文本块，吐出完整 SSE 消息。可独立单元测试。
export class SSEParser {
  private buffer = '';
  private eventName = 'message';
  private eventId = '';
  private dataLines: string[] = [];

  // 输入一段文本，返回本次解析出的完整消息列表。
  push(chunk: string): SSEMessage[] {
    this.buffer += chunk;
    const messages: SSEMessage[] = [];

    let idx: number;
    // 按行处理，行分隔符兼容 \n 与 \r\n
    while ((idx = this.buffer.indexOf('\n')) !== -1) {
      let line = this.buffer.slice(0, idx);
      this.buffer = this.buffer.slice(idx + 1);
      if (line.endsWith('\r')) line = line.slice(0, -1);

      if (line === '') {
        // 空行：一个事件结束
        if (this.dataLines.length > 0) {
          messages.push(this.message());
        }
        this.eventName = 'message';
        this.eventId = '';
        this.dataLines = [];
        continue;
      }

      if (line.startsWith(':')) {
        // 注释行，忽略
        continue;
      }

      const colon = line.indexOf(':');
      const field = colon === -1 ? line : line.slice(0, colon);
      let value = colon === -1 ? '' : line.slice(colon + 1);
      if (value.startsWith(' ')) value = value.slice(1);

      if (field === 'event') {
        this.eventName = value;
      } else if (field === 'id') {
        this.eventId = value;
      } else if (field === 'data') {
        this.dataLines.push(value);
      }
    }

    return messages;
  }

  // 流结束时调用：先消费 buffer 中残留的最后一行（无换行符结尾的情况），
  // 再冲洗未以空行结尾的最后一个事件，避免末尾消息丢失。
  flush(): SSEMessage[] {
    let line = this.buffer;
    this.buffer = '';
    if (line.endsWith('\r')) line = line.slice(0, -1);
    if (line !== '' && !line.startsWith(':')) {
      const colon = line.indexOf(':');
      const field = colon === -1 ? line : line.slice(0, colon);
      let value = colon === -1 ? '' : line.slice(colon + 1);
      if (value.startsWith(' ')) value = value.slice(1);
      if (field === 'event') {
        this.eventName = value;
      } else if (field === 'id') {
        this.eventId = value;
      } else if (field === 'data') {
        this.dataLines.push(value);
      }
    }

    const messages: SSEMessage[] = [];
    if (this.dataLines.length > 0) {
      messages.push(this.message());
    }
    this.eventName = 'message';
    this.eventId = '';
    this.dataLines = [];
    return messages;
  }

  private message(): SSEMessage {
    const message: SSEMessage = { event: this.eventName, data: this.dataLines.join('\n') };
    if (this.eventId) message.id = this.eventId;
    return message;
  }
}

export interface StreamOptions {
  body: unknown;
  signal?: AbortSignal;
  onMessage: (msg: SSEMessage) => void;
  onError?: (err: Error) => void;
}

export interface StreamGetOptions {
  lastEventId?: string;
  signal?: AbortSignal;
  onMessage: (msg: SSEMessage) => void;
  onError?: (err: Error) => void;
}

// 发起一个 POST SSE 流式请求。
export async function streamPost(path: string, opts: StreamOptions): Promise<void> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    Accept: 'text/event-stream',
  };
  const token = tokenStorage.getAccess();
  if (token) headers['Authorization'] = `Bearer ${token}`;

  let res: Response;
  try {
    res = await fetch(`${API_BASE}${path}`, {
      method: 'POST',
      headers,
      body: JSON.stringify(opts.body),
      signal: opts.signal,
    });
  } catch (err) {
    // 主动取消（卸载/切换）不作为业务错误上报，避免误弹错误提示。
    if ((err as Error).name !== 'AbortError') {
      opts.onError?.(err instanceof Error ? err : new Error(String(err)));
    }
    return;
  }

  if (!res.ok || !res.body) {
    opts.onError?.(new Error(`流式请求失败：HTTP ${res.status}`));
    return;
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  const parser = new SSEParser();

  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      const text = decoder.decode(value, { stream: true });
      for (const msg of parser.push(text)) {
        opts.onMessage(msg);
      }
    }
    // 流结束：flush 解码器残留字节（避免最后一块多字节 UTF-8 字符被截断丢失），
    // 并冲洗 parser 中无尾随空行的最后一个事件。
    const tail = decoder.decode();
    if (tail) {
      for (const msg of parser.push(tail)) {
        opts.onMessage(msg);
      }
    }
    for (const msg of parser.flush()) {
      opts.onMessage(msg);
    }
  } catch (err) {
    if ((err as Error).name !== 'AbortError') {
      opts.onError?.(err instanceof Error ? err : new Error(String(err)));
    }
  } finally {
    reader.releaseLock();
  }
}

export async function streamGet(path: string, opts: StreamGetOptions): Promise<void> {
  const headers: Record<string, string> = { Accept: 'text/event-stream' };
  const token = tokenStorage.getAccess();
  if (token) headers.Authorization = `Bearer ${token}`;
  if (opts.lastEventId) headers['Last-Event-ID'] = opts.lastEventId;

  let res: Response;
  try {
    res = await fetch(`${API_BASE}${path}`, { method: 'GET', headers, signal: opts.signal });
  } catch (err) {
    if ((err as Error).name !== 'AbortError') opts.onError?.(err instanceof Error ? err : new Error(String(err)));
    return;
  }
  if (!res.ok || !res.body) {
    opts.onError?.(new Error(`流式请求失败：HTTP ${res.status}`));
    return;
  }

  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  const parser = new SSEParser();
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      for (const message of parser.push(decoder.decode(value, { stream: true }))) opts.onMessage(message);
    }
    const tail = decoder.decode();
    if (tail) for (const message of parser.push(tail)) opts.onMessage(message);
    for (const message of parser.flush()) opts.onMessage(message);
  } catch (err) {
    if ((err as Error).name !== 'AbortError') opts.onError?.(err instanceof Error ? err : new Error(String(err)));
  } finally {
    reader.releaseLock();
  }
}
