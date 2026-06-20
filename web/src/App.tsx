import { type CSSProperties, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Bot,
  Brain,
  ChevronRight,
  Database,
  LogOut,
  MessageSquareText,
  Moon,
  Settings,
  Sparkles,
  Sun,
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
import { LoginPage, RegisterPage } from './pages/AuthPages';
import { AgentsPage } from './pages/AgentsPage';
import { CanvasPage } from './pages/CanvasPage';
import { ChatPage } from './pages/ChatPage';
import { KnowledgePage } from './pages/KnowledgePage';
import { SettingsPage } from './pages/SettingsPage';
import { useAuthStore } from './stores/authStore';

function RequireAuth() {
  const user = useAuthStore((state) => state.user);
  const location = useLocation();
  if (!user) return <Navigate to="/login" replace state={{ from: location.pathname }} />;
  return <Outlet />;
}

function PublicOnly({ children }: { children: React.ReactNode }) {
  const user = useAuthStore((state) => state.user);
  if (user) return <Navigate to="/app/agents" replace />;
  return children;
}

const nav = [
  { to: '/app/agents', label: '智能体', icon: Bot },
  { to: '/app/knowledge', label: '知识库', icon: Database },
  { to: '/app/chat', label: 'RAG 对话', icon: MessageSquareText },
  { to: '/app/settings', label: '设置', icon: Settings },
];

function storedPanelWidth(key: string, fallback: number) {
  const raw = localStorage.getItem(key);
  if (raw === null) return fallback;
  const value = Number(raw);
  return Number.isFinite(value) ? value : fallback;
}

function AppShell() {
  const user = useAuthStore((state) => state.user);
  const logout = useAuthStore((state) => state.logout);
  const navigate = useNavigate();
  const location = useLocation();
  const isCanvas = location.pathname.includes('/canvas');
  const [theme, setTheme] = useState(() => localStorage.getItem('agentcanvas-theme') ?? document.documentElement.dataset.theme ?? 'light');
  const [inspectorOpen, setInspectorOpen] = useState(() => !location.pathname.includes('/canvas'));
  const [sidebarWidth, setSidebarWidth] = useState(() => storedPanelWidth('agentcanvas-sidebar-width', 232));
  const [inspectorWidth, setInspectorWidth] = useState(() => storedPanelWidth('agentcanvas-inspector-width', 320));
  const workspaceRef = useRef<HTMLDivElement | null>(null);
  const sidebarCollapsed = sidebarWidth === 0;
  const sidebarCompact = sidebarWidth > 0 && sidebarWidth < 168;
  const inspectorCompact = inspectorWidth < 340;

  const pageTitle = useMemo(() => {
    if (isCanvas) return 'Flow Canvas';
    return nav.find((item) => location.pathname.startsWith(item.to))?.label ?? 'Workspace';
  }, [isCanvas, location.pathname]);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem('agentcanvas-theme', theme);
  }, [theme]);

  useEffect(() => {
    if (isCanvas) setInspectorOpen(false);
  }, [isCanvas]);

  useEffect(() => {
    localStorage.setItem('agentcanvas-sidebar-width', String(sidebarWidth));
  }, [sidebarWidth]);

  useEffect(() => {
    localStorage.setItem('agentcanvas-inspector-width', String(inspectorWidth));
  }, [inspectorWidth]);

  const startWorkspaceResize = useCallback((target: 'sidebar' | 'inspector', startX: number) => {
    const workspace = workspaceRef.current;
    if (!workspace) return;
    const rect = workspace.getBoundingClientRect();

    function onMove(event: PointerEvent) {
      if (target === 'sidebar') {
        const next = event.clientX - rect.left;
        setSidebarWidth(next < 96 ? 0 : Math.min(320, Math.max(76, next)));
        return;
      }

      const next = rect.right - event.clientX;
      if (next < 288) {
        setInspectorOpen(false);
        return;
      }
      setInspectorOpen(true);
      setInspectorWidth(Math.min(460, Math.max(300, next)));
    }

    function onUp() {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      document.body.classList.remove('is-resizing-workspace');
    }

    document.body.classList.add('is-resizing-workspace');
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
    onMove({ clientX: startX } as PointerEvent);
  }, []);

  const openInspectorFromRail = useCallback(() => {
    setInspectorWidth((width) => Math.min(460, Math.max(300, width || 320)));
    setInspectorOpen(true);
  }, []);

  return (
    <div className="app-shell">
      <header className="topbar glass">
        <div className="topbar-title">
          <div className="app-logo">
            <Sparkles size={18} />
          </div>
          <div className="min-w-0">
            <strong className="truncate">AgentCanvas</strong>
          </div>
        </div>
        <div className="topbar-actions">
          <IconButton label={theme === 'dark' ? '切换浅色主题' : '切换深色主题'} onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}>
            {theme === 'dark' ? <Sun size={17} /> : <Moon size={17} />}
          </IconButton>
          <IconButton label="退出登录" onClick={() => void logout().then(() => navigate('/login'))}>
            <LogOut size={17} />
          </IconButton>
        </div>
      </header>

      <div
        ref={workspaceRef}
        className={`workspace ${inspectorOpen ? 'inspector-open' : 'inspector-closed'} ${isCanvas ? 'workspace-canvas' : ''} ${sidebarCollapsed ? 'sidebar-collapsed' : ''} ${sidebarCompact ? 'sidebar-compact' : ''} ${inspectorCompact ? 'inspector-compact' : ''}`}
        style={{
          '--sidebar-width': `${sidebarWidth}px`,
          '--inspector-width': `${inspectorWidth}px`,
        } as CSSProperties}
      >
        <aside className="sidebar glass">
          <p className="eyebrow">Workspace</p>
          <nav className="nav-list" aria-label="主导航">
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
            <div className="user-chip">
              <div className="avatar">{user?.username?.slice(0, 1).toUpperCase() ?? 'A'}</div>
              <div className="min-w-0">
                <strong className="truncate">{user?.username ?? 'Agent Builder'}</strong>
                <p className="truncate muted">{user?.email ?? 'Local session'}</p>
              </div>
            </div>
          </div>
        </aside>
        <div
          className="workspace-resizer workspace-resizer-sidebar"
          role="separator"
          aria-orientation="vertical"
          aria-label="拖动调整侧边栏宽度"
          onPointerDown={(event) => {
            event.preventDefault();
            startWorkspaceResize('sidebar', event.clientX);
          }}
        />

        <main className="main-view surface">
          <Outlet />
        </main>

        <div
          className="workspace-resizer workspace-resizer-inspector"
          role="separator"
          aria-orientation="vertical"
          aria-label={inspectorOpen ? '拖动调整右侧信息栏宽度，拖到较窄时收起' : '点击展开右侧信息栏'}
          tabIndex={0}
          onKeyDown={(event) => {
            if (inspectorOpen || (event.key !== 'Enter' && event.key !== ' ')) return;
            event.preventDefault();
            openInspectorFromRail();
          }}
          onPointerDown={(event) => {
            event.preventDefault();
            if (!inspectorOpen) {
              openInspectorFromRail();
              return;
            }
            startWorkspaceResize('inspector', event.clientX);
          }}
        />

        <aside className="inspector glass" aria-hidden={!inspectorOpen}>
          <h2>{pageTitle}</h2>
          <p>这里汇总当前工作区的上下文、连接状态与操作线索。需要专注编辑时，可以收起侧栏，把空间留给主画布。</p>
          <div className="stack" style={{ marginTop: 16 }}>
            <div className="row">
              <Brain size={16} />
              <span className="muted">工作区上下文已同步</span>
            </div>
            <div className="row">
              <ChevronRight size={16} />
              <span className="muted">REST API 与 SSE 通道可用</span>
            </div>
          </div>
        </aside>
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

  if (!booted) return <div className="app-loading">正在唤醒 AgentCanvas...</div>;

  return (
    <Routes>
      <Route path="/" element={<Navigate to="/app/agents" replace />} />
      <Route path="/login" element={<PublicOnly><LoginPage /></PublicOnly>} />
      <Route path="/register" element={<PublicOnly><RegisterPage /></PublicOnly>} />
      <Route element={<RequireAuth />}>
        <Route path="/app" element={<AppShell />}>
          <Route index element={<Navigate to="/app/agents" replace />} />
          <Route path="agents" element={<AgentsPage />} />
          <Route path="agents/:id/canvas" element={<CanvasPage />} />
          <Route path="knowledge" element={<KnowledgePage />} />
          <Route path="chat" element={<ChatPage />} />
          <Route path="settings" element={<SettingsPage />} />
        </Route>
      </Route>
      <Route path="*" element={<Navigate to="/app/agents" replace />} />
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
