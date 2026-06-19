import { useEffect, useMemo, useState } from 'react';
import {
  Bot,
  Brain,
  ChevronRight,
  Database,
  LogOut,
  MessageSquareText,
  Moon,
  PanelRightClose,
  PanelRightOpen,
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
  { to: '/app/agents', label: '智能体', icon: Bot },
  { to: '/app/knowledge', label: '知识库', icon: Database },
  { to: '/app/chat', label: 'RAG 对话', icon: MessageSquareText },
  { to: '/app/settings', label: '设置', icon: Settings },
];

function AppShell() {
  const user = useAuthStore((state) => state.user);
  const logout = useAuthStore((state) => state.logout);
  const navigate = useNavigate();
  const location = useLocation();
  const isCanvas = location.pathname.includes('/canvas');
  const [theme, setTheme] = useState(() => document.documentElement.dataset.theme ?? 'light');
  const [inspectorOpen, setInspectorOpen] = useState(() => !location.pathname.includes('/canvas'));

  const pageTitle = useMemo(() => {
    if (isCanvas) return 'Flow Canvas';
    return nav.find((item) => location.pathname.startsWith(item.to))?.label ?? 'Workspace';
  }, [isCanvas, location.pathname]);

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);

  useEffect(() => {
    if (isCanvas) setInspectorOpen(false);
  }, [isCanvas]);

  return (
    <div className="app-shell">
      <header className="topbar glass">
        <div className="topbar-title">
          <div className="app-logo">
            <Sparkles size={18} />
          </div>
          <div className="min-w-0">
            <strong className="truncate">AgentCanvas</strong>
            <span className="truncate">可视化 Agent 工作台</span>
          </div>
        </div>
        <div className="topbar-center">
          <label className="toolbar-search">
            <Search size={16} />
            <input aria-label="全局搜索" placeholder="搜索 Agent、知识库或会话" />
          </label>
        </div>
        <div className="topbar-actions">
          <IconButton
            label={inspectorOpen ? '收起右侧信息栏' : '展开右侧信息栏'}
            onClick={() => setInspectorOpen((value) => !value)}
          >
            {inspectorOpen ? <PanelRightClose size={17} /> : <PanelRightOpen size={17} />}
          </IconButton>
          <IconButton label={theme === 'dark' ? '切换浅色主题' : '切换深色主题'} onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}>
            {theme === 'dark' ? <Sun size={17} /> : <Moon size={17} />}
          </IconButton>
          <IconButton label="退出登录" onClick={() => void logout().then(() => navigate('/login'))}>
            <LogOut size={17} />
          </IconButton>
        </div>
      </header>

      <div className={`workspace ${inspectorOpen ? 'inspector-open' : 'inspector-closed'} ${isCanvas ? 'workspace-canvas' : ''}`}>
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

        <aside className="inspector glass" aria-hidden={!inspectorOpen}>
          <h2>{pageTitle}</h2>
          <p>右侧信息栏可以随时收起；在画布页会默认隐藏，把空间优先留给核心工作区。</p>
          <div className="stack" style={{ marginTop: 16 }}>
            <div className="row">
              <Brain size={16} />
              <span className="muted">第五阶段可视化工作台</span>
            </div>
            <div className="row">
              <ChevronRight size={16} />
              <span className="muted">REST 与 SSE 已接入</span>
            </div>
          </div>
        </aside>
      </div>

      <footer className="statusbar glass">
        <span className="truncate">AgentCanvas 第五阶段</span>
        <span className="truncate">macOS 玻璃质感工作台 · {pageTitle}</span>
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
