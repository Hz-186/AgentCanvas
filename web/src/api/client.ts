// 统一 fetch 封装：自动带 Bearer、解包 {code,message,data}、401 触发 refresh 轮换重试。

import type { ApiEnvelope, TokenPair } from '../types/api';
import { tokenStorage } from './token';

export const API_BASE = '/api/v1';

export class ApiError extends Error {
  code: number;
  status: number;
  constructor(message: string, code: number, status: number) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    this.status = status;
  }
}

// 未授权时通知上层（authStore 监听以跳转登录）。
type UnauthorizedHandler = () => void;
let onUnauthorized: UnauthorizedHandler | null = null;
export function setUnauthorizedHandler(fn: UnauthorizedHandler): void {
  onUnauthorized = fn;
}

let refreshPromise: Promise<boolean> | null = null;

async function doRefresh(): Promise<boolean> {
  const refresh = tokenStorage.getRefresh();
  if (!refresh) return false;
  try {
    const res = await fetch(`${API_BASE}/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ refresh_token: refresh }),
    });
    if (!res.ok) return false;
    const env = (await res.json()) as ApiEnvelope<TokenPair>;
    if (env.code !== 0) return false;
    tokenStorage.setTokens(env.data);
    return true;
  } catch {
    return false;
  }
}

// 串行化 refresh，避免并发请求重复刷新。
function refreshOnce(): Promise<boolean> {
  if (!refreshPromise) {
    refreshPromise = doRefresh().finally(() => {
      refreshPromise = null;
    });
  }
  return refreshPromise;
}

export interface RequestOptions {
  method?: string;
  body?: unknown;
  // 是否需要认证（默认 true）
  auth?: boolean;
  signal?: AbortSignal;
  // multipart 时传 FormData，并跳过 JSON 序列化
  formData?: FormData;
  query?: Record<string, string | number | undefined>;
}

function buildUrl(path: string, query?: RequestOptions['query']): string {
  const url = `${API_BASE}${path}`;
  if (!query) return url;
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined && v !== null) params.set(k, String(v));
  }
  const qs = params.toString();
  return qs ? `${url}?${qs}` : url;
}

async function rawRequest(path: string, opts: RequestOptions, retry: boolean): Promise<Response> {
  const headers: Record<string, string> = {};
  const auth = opts.auth !== false;
  if (auth) {
    const token = tokenStorage.getAccess();
    if (token) headers['Authorization'] = `Bearer ${token}`;
  }

  let body: BodyInit | undefined;
  if (opts.formData) {
    body = opts.formData;
  } else if (opts.body !== undefined) {
    headers['Content-Type'] = 'application/json';
    body = JSON.stringify(opts.body);
  }

  const res = await fetch(buildUrl(path, opts.query), {
    method: opts.method ?? 'GET',
    headers,
    body,
    signal: opts.signal,
  });

  // access token 过期：刷新后重试一次
  if (res.status === 401 && auth && retry) {
    const ok = await refreshOnce();
    if (ok) {
      return rawRequest(path, opts, false);
    }
    onUnauthorized?.();
  }
  return res;
}

export async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const res = await rawRequest(path, opts, true);

  let env: ApiEnvelope<T> | null = null;
  const text = await res.text();
  if (text) {
    try {
      env = JSON.parse(text) as ApiEnvelope<T>;
    } catch {
      env = null;
    }
  }

  if (!res.ok || (env && env.code !== 0)) {
    const message = env?.message || res.statusText || '请求失败';
    const code = env?.code ?? res.status;
    throw new ApiError(message, code, res.status);
  }

  return (env ? env.data : (undefined as T)) as T;
}

export const api = {
  get: <T>(path: string, query?: RequestOptions['query']) => request<T>(path, { query }),
  post: <T>(path: string, body?: unknown) => request<T>(path, { method: 'POST', body }),
  patch: <T>(path: string, body?: unknown) => request<T>(path, { method: 'PATCH', body }),
  delete: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
  upload: <T>(path: string, formData: FormData) =>
    request<T>(path, { method: 'POST', formData }),
};
