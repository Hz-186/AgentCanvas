import { describe, expect, it } from 'vitest';
import { AGENT_HOME_PATH } from './App';

describe('Agent-only application shell', () => {
  it('uses the Agent home as the canonical entry point', () => {
    expect(AGENT_HOME_PATH).toBe('/app/agents');
  });
});
