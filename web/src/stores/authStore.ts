import { create } from 'zustand';
import { authApi } from '../api/auth';
import { ApiError, setUnauthorizedHandler } from '../api/client';
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

function friendlyAuthError(err: unknown, fallback: string, context: 'session' | 'login' | 'register' | 'general' = 'general'): string {
  const raw = err instanceof Error ? err.message : String(err || '');
  const text = raw.toLowerCase();
  const status = err instanceof ApiError ? err.status : 0;
  if (context === 'login' && (status === 401 || text.includes('unauthorized'))) return 'The email or password is incorrect.';
  if (context === 'session' && (status === 401 || text.includes('unauthorized'))) return 'Your session has expired. Sign in again.';
  if (text.includes('invalid') && text.includes('password')) return 'The password is incorrect.';
  if (text.includes('invalid') && text.includes('input')) return 'Check your email and password, then try again.';
  if (text.includes('unauthorized') || text.includes('401')) return 'Your session has expired. Sign in again.';
  if (text.includes('not found')) return 'We could not find that account.';
  if (text.includes('network') || text.includes('fetch')) return 'Unable to reach the server. Check that the backend is running.';
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
      set({ user: null, booted: true, error: friendlyAuthError(err, 'Your session has expired. Sign in again.', 'session') });
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
      set({ loading: false, error: friendlyAuthError(err, 'Sign-in failed. Check your email and password.', 'login') });
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
      set({ loading: false, error: friendlyAuthError(err, 'Registration failed. Check the information and try again.', 'register') });
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
