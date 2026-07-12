import { useEffect } from 'react';
import { BrowserRouter, Navigate, Outlet, Route, Routes, useLocation } from 'react-router-dom';
import { AnimatePresence } from 'framer-motion';
import { ParchmentBackground } from '@/components/ParchmentBackground';
import { AppShell } from '@/components/layout/AppShell';
import { LoginPage, RegisterPage } from '@/pages/AuthPages';
import { CanvasPage } from '@/pages/CanvasPage';
import { ChatPage } from '@/pages/ChatPage';
import { KnowledgePage } from '@/pages/KnowledgePage';
import { MemoryPage } from '@/pages/MemoryPage';
import { SettingsPage } from '@/pages/SettingsPage';
import { WorkflowsPage } from '@/pages/WorkflowsPage';
import { useAuthStore } from '@/stores/authStore';
import { initializeThemeListener } from '@/stores/themeStore';

function Boot() {
  const initialize = useAuthStore((s) => s.initialize);
  const booted = useAuthStore((s) => s.booted);

  useEffect(() => {
    const cleanupTheme = initializeThemeListener();
    void initialize();
    return cleanupTheme;
  }, [initialize]);

  if (!booted) {
    return (
      <div className="app-root">
        <div className="empty-state" style={{ minHeight: '100vh' }}>
          <strong>AgentCanvas</strong>
          <em>正在展开羊皮纸…</em>
        </div>
      </div>
    );
  }
  return <Outlet />;
}

function RequireAuth() {
  const user = useAuthStore((s) => s.user);
  const location = useLocation();
  if (!user) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  return <AppShell />;
}

function PublicOnly() {
  const user = useAuthStore((s) => s.user);
  if (user) return <Navigate to="/app/workflows" replace />;
  return <Outlet />;
}

function RoutedParchment() {
  const { pathname } = useLocation();
  const scene = pathname === '/login' || pathname === '/register'
    ? 'auth'
    : pathname.includes('/canvas') ? 'canvas' : pathname === '/app/workflows' ? 'workflows' : 'general';
  return <ParchmentBackground scene={scene} />;
}

export function App() {
  return (
    <div className="app-root">
      <BrowserRouter>
        <RoutedParchment />
        <AnimatePresence mode="wait">
          <Routes>
            <Route element={<Boot />}>
              <Route element={<PublicOnly />}>
                <Route path="/login" element={<LoginPage />} />
                <Route path="/register" element={<RegisterPage />} />
              </Route>
              <Route path="/app" element={<RequireAuth />}>
                <Route index element={<Navigate to="workflows" replace />} />
                <Route path="workflows" element={<WorkflowsPage />} />
                <Route path="workflows/:id/canvas" element={<CanvasPage />} />
                <Route path="knowledge" element={<KnowledgePage />} />
                <Route path="dialogs" element={<ChatPage />} />
                <Route path="memory" element={<MemoryPage />} />
                <Route path="settings" element={<SettingsPage />} />
              </Route>
              <Route path="/" element={<Navigate to="/app/workflows" replace />} />
              <Route path="*" element={<Navigate to="/app/workflows" replace />} />
            </Route>
          </Routes>
        </AnimatePresence>
      </BrowserRouter>
    </div>
  );
}
