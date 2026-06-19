// SSE over fetch：因为 SSE 需带 Authorization header（EventSource 不支持），
// 这里用 fetch + ReadableStream 手动解析 `event:` / `data:` 帧。

import { API_BASE } from './client';
import { tokenStorage } from './token';

export interface SSEMessage {
  event: string;
  data: string;
}

// 纯函数式增量解析器：喂入文本块，吐出完整 SSE 消息。可独立单元测试。
export class SSEParser {
  private buffer = '';
  private eventName = 'message';
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
          messages.push({ event: this.eventName, data: this.dataLines.join('\n') });
        }
        this.eventName = 'message';
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
      } else if (field === 'data') {
        this.dataLines.push(value);
      }
      // id / retry 字段当前不需要
    }

    return messages;
  }
}

export interface StreamOptions {
  body: unknown;
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
    opts.onError?.(err instanceof Error ? err : new Error(String(err)));
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
  } catch (err) {
    if ((err as Error).name !== 'AbortError') {
      opts.onError?.(err instanceof Error ? err : new Error(String(err)));
    }
  }
}
