import { Moon, Monitor, Sun } from 'lucide-react';
import { IconButton } from './ui';
import { useThemeStore, type ThemePreference } from '@/stores/themeStore';

const order: ThemePreference[] = ['system', 'light', 'dark'];

export function ThemeSwitch() {
  const preference = useThemeStore((s) => s.preference);
  const setPreference = useThemeStore((s) => s.setPreference);
  const next = () => setPreference(order[(order.indexOf(preference) + 1) % order.length]);
  const Icon = preference === 'light' ? Sun : preference === 'dark' ? Moon : Monitor;
  return <IconButton aria-label="切换主题" title={`主题: ${preference}`} onClick={next}><Icon size={18} /></IconButton>;
}
