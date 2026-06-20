import { api, request } from './client';
import type {
  AuthResponse,
  ApiToken,
  ApiTokenCreated,
  AuditLog,
  TokenPair,
  User,
} from '../types/api';

export const authApi = {
  register: (body: { username: string; email: string; password: string }) =>
    request<AuthResponse>('/auth/register', { method: 'POST', body, auth: false }),
  login: (body: { email: string; password: string }) =>
    request<AuthResponse>('/auth/login', { method: 'POST', body, auth: false }),
  refresh: (refresh_token: string) =>
    api.post<TokenPair>('/auth/refresh', { refresh_token }),
  logout: (refresh_token: string) =>
    api.post<{ success: boolean }>('/auth/logout', { refresh_token }),
  me: () => api.get<User>('/auth/me'),
  githubRedirect: () => request<{ redirect_url: string }>('/auth/github/redirect', { auth: false }),
};

export const tokenApi = {
  list: () => api.get<ApiToken[]>('/api-tokens'),
  create: (body: { name: string; scopes?: string[]; expires_at?: string | null }) =>
    api.post<ApiTokenCreated>('/api-tokens', body),
  remove: (id: number) => api.delete<{ success: boolean }>(`/api-tokens/${id}`),
};

export const auditApi = {
  list: (limit = 20, offset = 0) =>
    api.get<AuditLog[]>('/audit-logs', { limit, offset }),
};
