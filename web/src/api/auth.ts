import { api } from './client';
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
    api.post<AuthResponse>('/auth/register', body),
  login: (body: { email: string; password: string }) =>
    api.post<AuthResponse>('/auth/login', body),
  refresh: (refresh_token: string) =>
    api.post<TokenPair>('/auth/refresh', { refresh_token }),
  logout: (refresh_token: string) =>
    api.post<{ success: boolean }>('/auth/logout', { refresh_token }),
  me: () => api.get<User>('/auth/me'),
  githubRedirect: () => api.get<{ redirect_url: string }>('/auth/github/redirect'),
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
