import { describe, expect, it } from 'vitest';
import { SSEParser } from './sse';

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
});
