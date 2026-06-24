import { afterEach, describe, expect, it, vi } from 'vitest';
import { SSEParser, streamPost } from './sse';

describe('SSEParser', () => {
  it('parses event and multiline data frames across chunks', () => {
    const parser = new SSEParser();

    expect(parser.push('event: delta\ndata: {"content":"he')).toEqual([]);
    expect(parser.push('llo"}\n\n:data ignored\n')).toEqual([
      { event: 'delta', data: '{"content":"hello"}' },
    ]);
  });

  it('uses message as the default event name', () => {
    const parser = new SSEParser();

    expect(parser.push('data: ready\n\n')).toEqual([{ event: 'message', data: 'ready' }]);
  });

  it('flushes a trailing event that has no terminating blank line', () => {
    const parser = new SSEParser();

    // 没有结尾空行，push 不会产出该事件
    expect(parser.push('event: done\ndata: {"ok":true}\n')).toEqual([]);
    // flush 冲洗最后一个事件
    expect(parser.flush()).toEqual([{ event: 'done', data: '{"ok":true}' }]);
  });

  it('flushes a trailing line that has no newline at all', () => {
    const parser = new SSEParser();

    expect(parser.push('data: tail-without-newline')).toEqual([]);
    expect(parser.flush()).toEqual([{ event: 'message', data: 'tail-without-newline' }]);
  });

  it('flush returns empty when there is no pending event', () => {
    const parser = new SSEParser();
    expect(parser.push('data: a\n\n')).toEqual([{ event: 'message', data: 'a' }]);
    expect(parser.flush()).toEqual([]);
  });
});

function streamResponse(chunks: Uint8Array[]): Response {
  const body = new ReadableStream<Uint8Array>({
    start(controller) {
      for (const chunk of chunks) controller.enqueue(chunk);
      controller.close();
    },
  });
  return new Response(body, { status: 200, headers: { 'Content-Type': 'text/event-stream' } });
}

describe('streamPost', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('does not lose bytes when a multibyte UTF-8 char is split across chunks', async () => {
    // “世界”的 UTF-8 编码被从中间切断，验证 TextDecoder flush 不丢字节。
    const full = new TextEncoder().encode('data: 世界\n\n');
    const splitAt = 8; // 切在多字节字符中间
    const chunks = [full.slice(0, splitAt), full.slice(splitAt)];
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(streamResponse(chunks));

    const messages: string[] = [];
    await streamPost('/test', {
      body: {},
      onMessage: (msg) => messages.push(msg.data),
    });

    expect(messages).toEqual(['世界']);
  });

  it('delivers a final event even without a trailing blank line', async () => {
    const chunks = [new TextEncoder().encode('event: done\ndata: {"ok":true}\n')];
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(streamResponse(chunks));

    const events: Array<{ event: string; data: string }> = [];
    await streamPost('/test', {
      body: {},
      onMessage: (msg) => events.push(msg),
    });

    expect(events).toEqual([{ event: 'done', data: '{"ok":true}' }]);
  });

  it('reports an error when the response is not ok', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(new Response('nope', { status: 500 }));

    const onError = vi.fn();
    await streamPost('/test', { body: {}, onMessage: () => undefined, onError });

    expect(onError).toHaveBeenCalledOnce();
  });

  it('does not surface AbortError through onError', async () => {
    const abortErr = new DOMException('aborted', 'AbortError');
    vi.spyOn(globalThis, 'fetch').mockRejectedValue(abortErr);

    const onError = vi.fn();
    await streamPost('/test', { body: {}, onMessage: () => undefined, onError });

    // fetch 抛 AbortError 时不应作为业务错误上报
    expect(onError).not.toHaveBeenCalled();
  });
});
