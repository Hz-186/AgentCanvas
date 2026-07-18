import { type CSSProperties, useCallback, useEffect, useRef, useState } from 'react';
import {
  Bot,
  Database,
  LogOut,
  MessageSquareText,
  Network,
  LibraryBig,
  Settings,
  Sparkles,
  Wrench,
} from 'lucide-react';
import {
  BrowserRouter,
  Navigate,
  NavLink,
  Outlet,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from 'react-router-dom';
import { IconButton } from './components/ui';
import { AmbientLiquidField } from './components/editorial';
import { ThemeControl } from './components/ThemeControl';
import { LoginPage, RegisterPage } from './pages/AuthPages';
import { WorkflowsPage } from './pages/WorkflowsPage';
import { CanvasPage } from './pages/CanvasPage';
import { ChatPage } from './pages/ChatPage';
import { KnowledgePage } from './pages/KnowledgePage';
import { MemoryPage, SettingsPage, SkillsPage, ToolsPage } from './pages/SettingsPage';
import { useAuthStore } from './stores/authStore';

function RequireAuth() {
  const user = useAuthStore((state) => state.user);
  const location = useLocation();
  if (!user) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  return <Outlet />;
}

function PublicOnly({ children }: { children: React.ReactNode }) {
  const user = useAuthStore((state) => state.user);
  if (user) return <Navigate to="/app/workflows" replace />;
  return children;
}

const nav = [
  { to: '/app/workflows', label: 'Workflows', icon: Bot },
  { to: '/app/knowledge', label: 'Knowledge', icon: Database },
  { to: '/app/agents', label: 'Agent Chat', icon: MessageSquareText },
  { to: '/app/memory', label: 'Memory', icon: Network },
  { to: '/app/tools', label: 'Tools', icon: Wrench },
  { to: '/app/skills', label: 'Skills', icon: LibraryBig },
  { to: '/app/settings', label: 'Settings', icon: Settings },
];

const SIDEBAR_ICON_WIDTH = 66;
const SIDEBAR_MIN_WIDTH = 204;
const SIDEBAR_MAX_WIDTH = 320;
const SIDEBAR_COLLAPSE_THRESHOLD = 118;

function storedPanelWidth(key: string, fallback: number) {
  const raw = localStorage.getItem(key);
  if (raw === null) return fallback;
  const value = Number(raw);
  return Number.isFinite(value) ? value : fallback;
}

function normalizeSidebarWidth(value: number) {
  if (value === 0 || value <= SIDEBAR_COLLAPSE_THRESHOLD) return SIDEBAR_ICON_WIDTH;
  return Math.min(SIDEBAR_MAX_WIDTH, Math.max(SIDEBAR_ICON_WIDTH, value));
}

function AppShell() {
  const user = useAuthStore((state) => state.user);
  const logout = useAuthStore((state) => state.logout);
  const navigate = useNavigate();
  const location = useLocation();
  const isCanvas = location.pathname.includes('/canvas');
  const [sidebarWidth, setSidebarWidth] = useState(() => normalizeSidebarWidth(storedPanelWidth('agentcanvas-sidebar-width', 232)));
  const workspaceRef = useRef<HTMLDivElement | null>(null);
  const sidebarCompact = sidebarWidth > 0 && sidebarWidth < SIDEBAR_MIN_WIDTH;

  useEffect(() => {
    localStorage.setItem('agentcanvas-sidebar-width', String(sidebarWidth));
  }, [sidebarWidth]);

  const startWorkspaceResize = useCallback((startX: number) => {
    const workspace = workspaceRef.current;
    if (!workspace) return;
    const element = workspace;
    const rect = element.getBoundingClientRect();
    let latestX = startX;
    let frame = 0;
    let finalWidth = sidebarWidth;
    function applyResize(clientX: number) {
      const next = clientX - rect.left;
      const width = Math.min(SIDEBAR_MAX_WIDTH, Math.max(SIDEBAR_ICON_WIDTH, next));
      finalWidth = width;
      element.style.setProperty('--sidebar-width', `${width}px`);
      element.classList.toggle('sidebar-compact', width < SIDEBAR_MIN_WIDTH);
    }

    function onMove(event: PointerEvent) {
      latestX = event.clientX;
      if (frame) return;
      frame = window.requestAnimationFrame(() => {
        frame = 0;
        applyResize(latestX);
      });
    }

    function onUp() {
      if (frame) {
        window.cancelAnimationFrame(frame);
        frame = 0;
      }
      applyResize(latestX);
      setSidebarWidth(finalWidth);
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      document.body.classList.remove('is-resizing-workspace');
    }

    document.body.classList.add('is-resizing-workspace');
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    applyResize(startX);
  }, [sidebarWidth]);

  return (
    <div className="app-shell">
      <div
        ref={workspaceRef}
        className={`workspace ${isCanvas ? 'workspace-canvas' : ''} ${sidebarCompact ? 'sidebar-compact' : ''}`}
        style={{ '--sidebar-width': `${sidebarWidth}px` } as CSSProperties}
      >
        <aside className="sidebar glass">
          <div className="sidebar-brand">
          <div className="app-logo">
            <Sparkles size={18} />
          </div>
          <div className="min-w-0">
            <strong className="truncate">AgentCanvas</strong>
          </div>
          </div>
          <nav className="nav-list" aria-label="Primary navigation">
            {nav.map((item) => {
              const Icon = item.icon;
              return (
                <NavLink className="nav-link" to={item.to} key={item.to}>
                  <Icon size={18} />
                  <span className="truncate">{item.label}</span>
                </NavLink>
              );
            })}
          </nav>
          <div className="sidebar-footer">
            <ThemeControl compact={sidebarCompact} />
            <div className="user-chip">
              <div className="avatar">{user?.username?.slice(0, 1).toUpperCase() ?? 'A'}</div>
              <div className="min-w-0">
                <strong className="truncate">{user?.username ?? 'Workflow Builder'}</strong>
                <p className="truncate muted">{user?.email ?? 'Local session'}</p>
              </div>
            </div>
            <IconButton className="sidebar-signout" label="退出登录" onClick={() => void logout().then(() => navigate('/login'))}>
              <LogOut size={17} />
              <span>Sign out</span>
            </IconButton>
          </div>
        </aside>
        <div
          className="workspace-resizer workspace-resizer-sidebar"
          role="separator"
          aria-orientation="vertical"
          aria-label="Resize sidebar"
          onPointerDown={(event) => {
            event.preventDefault();
            startWorkspaceResize(event.clientX);
          }}
        />

        <main className="main-view surface">
          <AmbientLiquidField />
          <Outlet />
        </main>

      </div>
    </div>
  );
}

function Boot() {
  const booted = useAuthStore((state) => state.booted);
  const initialize = useAuthStore((state) => state.initialize);

  useEffect(() => {
    void initialize();
  }, [initialize]);

  if (!booted) return <div className="app-loading">Waking Agent Canvas...</div>;

  return (
    <Routes>
      <Route path="/" element={<Navigate to="/app/workflows" replace />} />
      <Route path="/login" element={<PublicOnly><LoginPage /></PublicOnly>} />
      <Route path="/register" element={<PublicOnly><RegisterPage /></PublicOnly>} />
      <Route element={<RequireAuth />}>
        <Route path="/app" element={<AppShell />}>
          <Route index element={<Navigate to="/app/workflows" replace />} />
          <Route path="workflows" element={<WorkflowsPage />} />
          <Route path="workflows/:id/canvas" element={<CanvasPage />} />
          <Route path="knowledge" element={<KnowledgePage />} />
          <Route path="knowledge/:id" element={<KnowledgePage />} />
          <Route path="chat" element={<Navigate to="/app/agents" replace />} />
          <Route path="chat/:conversationId" element={<Navigate to="/app/agents" replace />} />
          <Route path="agents" element={<ChatPage />} />
          <Route path="agents/:agentId/chat" element={<ChatPage />} />
          <Route path="agents/:agentId/chat/:conversationId" element={<ChatPage />} />
          <Route path="dialogs" element={<Navigate to="/app/agents" replace />} />
          <Route path="dialogs/:dialogId/chat" element={<ChatPage />} />
          <Route path="dialogs/:dialogId/chat/:conversationId" element={<ChatPage />} />
          <Route path="memory" element={<MemoryPage />} />
          <Route path="tools" element={<ToolsPage />} />
          <Route path="skills" element={<SkillsPage />} />
          <Route path="settings" element={<SettingsPage />} />
        </Route>
      </Route>
      <Route path="*" element={<Navigate to="/app/workflows" replace />} />
    </Routes>
  );
}

export function App() {
  return (
    <BrowserRouter>
      <Boot />
    </BrowserRouter>
  );
}
