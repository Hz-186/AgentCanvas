export function formatDate(value?: string | null): string {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '-';
  return new Intl.DateTimeFormat('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date);
}

export function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return '0 B';
  const units = ['B', 'KB', 'MB', 'GB'];
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < units.length - 1) {
    size /= 1024;
    unit += 1;
  }
  return `${size.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

export function prettyJson(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

export function parseJsonObject(input: string): Record<string, unknown> {
  const parsed = JSON.parse(input) as unknown;
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('请输入 JSON object，例如 {"query":"..."}');
  }
  return parsed as Record<string, unknown>;
}

export function friendlyErrorMessage(error: unknown, fallback = '操作没有完成，请稍后再试'): string {
  const raw = error instanceof Error ? error.message : String(error || fallback);
  if (/Table 'agentcanvas\.[^']+' doesn't exist|Unknown column/i.test(raw)) {
    return '当前功能还没有完成数据表初始化，请先运行数据库迁移后刷新页面。';
  }
  if (/Invalid JSON text/i.test(raw)) {
    return '服务端写入了非法 JSON，请检查后端字段默认值后重试。';
  }
  if (/SQLSTATE|Error\s+\d+\s+\([A-Z0-9]+\)|agentcanvas\./i.test(raw)) {
    return '服务端数据暂时没有准备好，请检查后端服务和数据库状态后重试。';
  }
  if (/Failed to fetch|NetworkError|Load failed/i.test(raw)) {
    return '暂时连接不上服务，请确认后端正在运行。';
  }
  return raw || fallback;
}
