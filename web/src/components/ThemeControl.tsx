import { useEffect, useState } from 'react';
import { Laptop, Moon, Sun } from 'lucide-react';

export type ThemePreference = 'system' | 'light' | 'dark';

const STORAGE_KEY = 'agentcanvas-theme';
const systemTheme = () => window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';

function initialPreference(): ThemePreference {
  const stored = localStorage.getItem(STORAGE_KEY);
  return stored === 'light' || stored === 'dark' || stored === 'system' ? stored : 'system';
}

export function useThemePreference() {
  const [preference, setPreference] = useState<ThemePreference>(initialPreference);

  useEffect(() => {
    const query = window.matchMedia('(prefers-color-scheme: dark)');
    const apply = () => {
      document.documentElement.dataset.theme = preference === 'system' ? systemTheme() : preference;
      document.documentElement.dataset.themePreference = preference;
    };
    apply();
    localStorage.setItem(STORAGE_KEY, preference);
    query.addEventListener('change', apply);
    return () => query.removeEventListener('change', apply);
  }, [preference]);

  return [preference, setPreference] as const;
}

export function ThemeControl({ compact = false }: { compact?: boolean }) {
  const [preference, setPreference] = useThemePreference();
  const options = [
    { value: 'system' as const, label: 'Auto', icon: Laptop },
    { value: 'light' as const, label: 'Light', icon: Sun },
    { value: 'dark' as const, label: 'Dark', icon: Moon },
  ];
  const activeIndex = options.findIndex((option) => option.value === preference);
  const active = options[activeIndex] ?? options[0];
  const ActiveIcon = active.icon;

  function cyclePreference() {
    setPreference(options[(activeIndex + 1) % options.length].value);
  }

  return (
    <button
      className={`theme-control ${compact ? 'theme-control-compact' : ''}`}
      type="button"
      title={`Appearance · ${active.label}`}
      aria-label={`Appearance: ${active.label}. Click to switch theme.`}
      onClick={cyclePreference}
    >
      <ActiveIcon size={15} />
      {!compact ? <span>Appearance · {active.label}</span> : null}
    </button>
  );
}
