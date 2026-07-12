import { create } from 'zustand';
import { authApi } from '@/api/auth';
import { setUnauthorizedHandler } from '@/api/client';
import { tokenStorage } from '@/api/token';
import type { User } from '@/types/api';

interface AuthState {
  user: User | null;
  booted: boolean;
  loading: boolean;
  error: string;
  initialize: () => Promise<void>;
  login: (email: string, password: string) => Promise<void>;
  register: (payload: { username: string; email: string; password: string }) => Promise<void>;
  logout: () => Promise<void>;
  clear: () => void;
}

function getError(error: unknown): string {
  return error instanceof Error ? error.message : '操作失败';
}

export const useAuthStore = create<AuthState>((set) => ({
  user: tokenStorage.getUser(),
  booted: false,
  loading: false,
  error: '',
  initialize: async () => {
    const access = tokenStorage.getAccess();
    if (!access) {
      set({ user: null, booted: true });
      return;
    }
    try {
      const user = await authApi.me();
      tokenStorage.setUser(user);
      set({ user, booted: true, error: '' });
    } catch {
      tokenStorage.clear();
      set({ user: null, booted: true });
    }
  },
  login: async (email, password) => {
    set({ loading: true, error: '' });
    try {
      const res = await authApi.login({ email, password });
      tokenStorage.setTokens(res.tokens);
      tokenStorage.setUser(res.user);
      set({ user: res.user, loading: false });
    } catch (error) {
      set({ error: getError(error), loading: false });
      throw error;
    }
  },
  register: async (payload) => {
    set({ loading: true, error: '' });
    try {
      const res = await authApi.register(payload);
      tokenStorage.setTokens(res.tokens);
      tokenStorage.setUser(res.user);
      set({ user: res.user, loading: false });
    } catch (error) {
      set({ error: getError(error), loading: false });
      throw error;
    }
  },
  logout: async () => {
    const refresh = tokenStorage.getRefresh();
    if (refresh) await authApi.logout(refresh).catch(() => undefined);
    tokenStorage.clear();
    set({ user: null });
  },
  clear: () => {
    tokenStorage.clear();
    set({ user: null });
  },
}));

setUnauthorizedHandler(() => useAuthStore.getState().clear());
