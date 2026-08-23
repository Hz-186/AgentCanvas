import { describe, expect, it } from 'vitest';
import { formatBytes, formatDate, friendlyErrorMessage, parseJsonObject, prettyJson } from './format';
import { ApiError } from '../api/client';

describe('formatBytes', () => {
  it('returns 0 B for non-positive or invalid input', () => {
    expect(formatBytes(0)).toBe('0 B');
    expect(formatBytes(-1)).toBe('0 B');
    expect(formatBytes(Number.NaN)).toBe('0 B');
  });

  it('formats bytes across units', () => {
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(2048)).toBe('2.0 KB');
    expect(formatBytes(5 * 1024 * 1024)).toBe('5.0 MB');
  });
});

describe('formatDate', () => {
  it('returns a placeholder for empty or invalid values', () => {
    expect(formatDate(undefined)).toBe('-');
    expect(formatDate(null)).toBe('-');
    expect(formatDate('not-a-date')).toBe('-');
  });

  it('formats a valid ISO date', () => {
    expect(formatDate('2024-01-02T03:04:05Z')).not.toBe('-');
  });
});

describe('parseJsonObject', () => {
  it('parses a JSON object', () => {
    expect(parseJsonObject('{"query":"hi"}')).toEqual({ query: 'hi' });
  });

  it('throws for arrays and non-objects', () => {
    expect(() => parseJsonObject('[1,2]')).toThrow();
    expect(() => parseJsonObject('42')).toThrow();
    expect(() => parseJsonObject('"text"')).toThrow();
  });

  it('throws for invalid JSON', () => {
    expect(() => parseJsonObject('{bad')).toThrow();
  });
});

describe('prettyJson', () => {
  it('pretty prints values', () => {
    expect(prettyJson({ a: 1 })).toBe('{\n  "a": 1\n}');
  });

  it('falls back to String for circular structures', () => {
    const obj: Record<string, unknown> = {};
    obj.self = obj;
    expect(typeof prettyJson(obj)).toBe('string');
  });
});

describe('friendlyErrorMessage', () => {
  it('maps missing-table SQL errors to a migration hint', () => {
    const err = new Error("Table 'agentcanvas.users' doesn't exist");
    expect(friendlyErrorMessage(err)).toContain('数据库迁移');
  });

  it('maps missing-column SQL errors to a migration hint', () => {
	const err = new Error("Error 1054 (42S22): Unknown column 'checkpoint_json' in 'field list'");
    expect(friendlyErrorMessage(err)).toContain('数据库迁移');
  });

  it('maps invalid JSON SQL errors to a backend JSON hint', () => {
	const err = new Error('Error 3140 (22032): Invalid JSON text in value for column input_schema_json');
    expect(friendlyErrorMessage(err)).toContain('非法 JSON');
  });

  it('maps network errors to a backend hint', () => {
    expect(friendlyErrorMessage(new Error('Failed to fetch'))).toContain('连接不上服务');
  });

  it('passes through ApiError messages', () => {
    expect(friendlyErrorMessage(new ApiError('名称已存在', 1001, 400))).toBe('名称已存在');
  });

  it('uses fallback for empty input', () => {
    expect(friendlyErrorMessage(undefined, '默认提示')).toBe('默认提示');
  });
});
