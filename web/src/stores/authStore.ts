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
      set({ user: null, booted: true, error: err instanceof Error ? err.message : '登录状态已失效' });
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
      set({ loading: false, error: err instanceof Error ? err.message : '登录失败' });
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
      set({ loading: false, error: err instanceof Error ? err.message : '注册失败' });
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
