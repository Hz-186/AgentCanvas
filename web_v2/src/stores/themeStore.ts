import { create } from 'zustand';

export type ThemePreference = 'system' | 'light' | 'dark';

const STORAGE_KEY = 'agentcanvas-v2-theme';

function resolveTheme(pref: ThemePreference): 'light' | 'dark' {
  if (pref === 'light' || pref === 'dark') return pref;
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
}

interface ThemeState {
  preference: ThemePreference;
  theme: 'light' | 'dark';
  setPreference: (preference: ThemePreference) => void;
  syncSystem: () => void;
}

function readPreference(): ThemePreference {
  const value = localStorage.getItem(STORAGE_KEY);
  return value === 'light' || value === 'dark' || value === 'system' ? value : 'system';
}

export const useThemeStore = create<ThemeState>((set, get) => ({
  preference: readPreference(),
  theme: resolveTheme(readPreference()),
  setPreference: (preference) => {
    localStorage.setItem(STORAGE_KEY, preference);
    const theme = resolveTheme(preference);
    document.documentElement.dataset.theme = theme;
    set({ preference, theme });
  },
  syncSystem: () => {
    const preference = get().preference;
    const theme = resolveTheme(preference);
    document.documentElement.dataset.theme = theme;
    set({ theme });
  },
}));

export function initializeThemeListener(): () => void {
  const media = window.matchMedia('(prefers-color-scheme: dark)');
  const listener = () => useThemeStore.getState().syncSystem();
  media.addEventListener('change', listener);
  useThemeStore.getState().syncSystem();
  return () => media.removeEventListener('change', listener);
}
