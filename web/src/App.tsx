import { useEffect, useMemo, useState } from 'react';
import {
  Bot,
  Brain,
  ChevronRight,
  Database,
  LogOut,
  MessageSquareText,
  Moon,
  Search,
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
  { to: '/app/agents', label: 'Agents', icon: Bot },
  { to: '/app/knowledge', label: 'Knowledge', icon: Database },
  { to: '/app/chat', label: 'RAG Chat', icon: MessageSquareText },
  { to: '/app/settings', label: 'Settings', icon: Settings },
];

function AppShell() {
  const user = useAuthStore((state) => state.user);
  const logout = useAuthStore((state) => state.logout);
  const navigate = useNavigate();
  const location = useLocation();
  const [theme, setTheme] = useState(() => document.documentElement.dataset.theme ?? 'light');

  const pageTitle = useMemo(() => {
    if (location.pathname.includes('/canvas')) return 'Flow Canvas';
    return nav.find((item) => location.pathname.startsWith(item.to))?.label ?? 'Workspace';
  }, [location.pathname]);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  return (
    <div className="app-shell">
      <header className="topbar glass">
        <div className="topbar-title">
          <div className="app-logo">
            <Sparkles size={18} />
          </div>
          <div className="min-w-0">
            <strong className="truncate">AgentCanvas</strong>
            <span className="truncate">Visual Agent Platform</span>
          </div>
        </div>
        <div className="topbar-center">
          <label className="toolbar-search">
            <Search size={16} />
            <input aria-label="全局搜索" placeholder="搜索 Agent、知识库或会话" />
          </label>
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

      <div className="workspace">
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

        <main className="main-view surface">
          <Outlet />
        </main>

        <aside className="inspector glass">
          <h2>{pageTitle}</h2>
          <p>当前工作区保持桌面软件式布局。可在窄窗口中自动收起侧栏与检查器，核心操作仍保留在主区域。</p>
          <div className="stack" style={{ marginTop: 16 }}>
            <div className="row">
              <Brain size={16} />
              <span className="muted">Phase 5 Visual Workspace</span>
            </div>
            <div className="row">
              <ChevronRight size={16} />
              <span className="muted">REST + SSE Connected</span>
            </div>
          </div>
        </aside>
      </div>

      <footer className="statusbar glass">
        <span className="truncate">AgentCanvas Phase 5</span>
        <span className="truncate">macOS glass workspace · {pageTitle}</span>
      </footer>
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
