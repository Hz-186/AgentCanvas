import { create } from 'zustand';
import { authApi } from '../api/auth';
import { setUnauthorizedHandler } from '../api/client';
import { tokenStorage } from '../api/token';
import type { User } from '../types/api';

interface AuthState {
  user: User | null;
  booted: boolean;
  loading: boolean;
  error: string;
  initialize: () => Promise<void>;
  login: (email: string, password: string) => Promise<void>;
  register: (body: { username: string; email: string; password: string }) => Promise<void>;
  logout: () => Promise<void>;
  clear: () => void;
}

function friendlyAuthError(err: unknown, fallback: string): string {
  const raw = err instanceof Error ? err.message : String(err || '');
  const text = raw.toLowerCase();
  if (text.includes('invalid') && text.includes('password')) return '密码不正确，请重新输入。';
  if (text.includes('invalid') && text.includes('input')) return '输入内容不符合要求，请检查邮箱和密码后重试。';
  if (text.includes('unauthorized') || text.includes('401')) return '登录状态已失效，请重新登录。';
  if (text.includes('not found')) return '账号不存在，请检查邮箱或先创建账号。';
  if (text.includes('network') || text.includes('fetch')) return '无法连接服务器，请确认后端服务已经启动。';
  if (text.includes('failed')) return fallback;
  return raw && raw.length < 80 && !/[a-z]{4,}\s+[a-z]{4,}/i.test(raw) ? raw : fallback;
}

export const useAuthStore = create<AuthState>((set, get) => ({
  user: tokenStorage.getUser(),
  booted: false,
  loading: false,
  error: '',
  initialize: async () => {
    const token = tokenStorage.getAccess();
    if (!token) {
      set({ user: null, booted: true });
      return;
    }
    try {
      const user = await authApi.me();
      tokenStorage.setUser(user);
      set({ user, booted: true, error: '' });
    } catch (err) {
      tokenStorage.clear();
      set({ user: null, booted: true, error: friendlyAuthError(err, '登录状态已失效，请重新登录。') });
    }
  },
  login: async (email, password) => {
    set({ loading: true, error: '' });
    try {
      const resp = await authApi.login({ email, password });
      tokenStorage.setTokens(resp.tokens);
      tokenStorage.setUser(resp.user);
      set({ user: resp.user, loading: false, error: '' });
    } catch (err) {
      set({ loading: false, error: friendlyAuthError(err, '登录失败，请检查邮箱和密码。') });
      throw err;
    }
  },
  register: async (body) => {
    set({ loading: true, error: '' });
    try {
      const resp = await authApi.register(body);
      tokenStorage.setTokens(resp.tokens);
      tokenStorage.setUser(resp.user);
      set({ user: resp.user, loading: false, error: '' });
    } catch (err) {
      set({ loading: false, error: friendlyAuthError(err, '注册失败，请检查填写的信息。') });
      throw err;
    }
  },
  logout: async () => {
    const refresh = tokenStorage.getRefresh();
    if (refresh) {
      try {
        await authApi.logout(refresh);
      } catch {
        // 本地退出优先，服务端会话失效失败不阻塞界面。
      }
    }
    get().clear();
  },
  clear: () => {
    tokenStorage.clear();
    set({ user: null, error: '' });
  },
}));

setUnauthorizedHandler(() => {
  useAuthStore.getState().clear();
});
