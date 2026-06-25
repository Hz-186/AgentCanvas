import { useCallback, useEffect, useRef, useState, type FormEvent, type PointerEvent as ReactPointerEvent } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { ActivitySquare, Github, LockKeyhole, Mail, Moon, Network, Route, Sparkles, Sun, Workflow } from 'lucide-react';
import { authApi } from '../api/auth';
import { tokenStorage } from '../api/token';
import { Button, Field, IconButton, TextInput } from '../components/ui';
import { useAuthStore } from '../stores/authStore';

const LIQUID_GLASS_POINTER_QUERY = '(hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference)';

function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value));
}

function canUseLiquidGlassPointer() {
  return typeof window !== 'undefined' && window.matchMedia(LIQUID_GLASS_POINTER_QUERY).matches;
}

function AuthBrandPanel() {
  const panelRef = useRef<HTMLElement>(null);

  const resetPanel = useCallback(() => {
    const panel = panelRef.current;
    if (!panel) return;

    panel.dataset.liquidActive = 'false';
    panel.style.setProperty('--tilt-x', '0deg');
    panel.style.setProperty('--tilt-y', '0deg');
    panel.style.setProperty('--shift-x', '0px');
    panel.style.setProperty('--shift-y', '0px');
    panel.style.setProperty('--glass-shadow-x', '0px');
    panel.style.setProperty('--glass-shadow-y', '26px');
    panel.style.setProperty('--glass-shadow-blur', '66px');
  }, []);

  const updatePanel = useCallback(
    (event: ReactPointerEvent<HTMLElement>) => {
      if (!canUseLiquidGlassPointer()) {
        resetPanel();
        return;
      }

      const panel = panelRef.current;
      if (!panel) return;

      const rect = panel.getBoundingClientRect();
      const x = clamp((event.clientX - rect.left) / rect.width, 0, 1);
      const y = clamp((event.clientY - rect.top) / rect.height, 0, 1);
      const normalizedX = x - 0.5;
      const normalizedY = y - 0.5;

      panel.dataset.liquidActive = 'true';
      panel.style.setProperty('--tilt-x', `${(-normalizedY * 8).toFixed(2)}deg`);
      panel.style.setProperty('--tilt-y', `${(normalizedX * 10).toFixed(2)}deg`);
      panel.style.setProperty('--shift-x', `${(normalizedX * 10).toFixed(2)}px`);
      panel.style.setProperty('--shift-y', `${(normalizedY * 8).toFixed(2)}px`);
      panel.style.setProperty('--glass-shadow-x', `${(-normalizedX * 18).toFixed(2)}px`);
      panel.style.setProperty('--glass-shadow-y', `${(30 + normalizedY * 12).toFixed(2)}px`);
      panel.style.setProperty('--glass-shadow-blur', `${(72 + Math.abs(normalizedX) * 16).toFixed(2)}px`);
    },
    [resetPanel],
  );

  useEffect(() => {
    const panel = panelRef.current;
    if (!panel) return undefined;

    const motionQuery = window.matchMedia(LIQUID_GLASS_POINTER_QUERY);
    const handleMotionChange = () => {
      if (!motionQuery.matches) resetPanel();
    };

    resetPanel();
    motionQuery.addEventListener('change', handleMotionChange);

    return () => {
      motionQuery.removeEventListener('change', handleMotionChange);
    };
  }, [resetPanel]);

  return (
    <aside ref={panelRef} className="auth-brand" onPointerEnter={updatePanel} onPointerMove={updatePanel} onPointerLeave={resetPanel}>
      <div className="auth-brand-content">
        <div className="brand-mark">
          <Sparkles size={26} />
        </div>
        <h1>Agent Canvas</h1>
        <p>构建、路由、观测 Agent Flow。</p>
        <div className="auth-feature-grid">
          <span>
            <Workflow size={18} />
            流图编排
          </span>
          <span>
            <Route size={18} />
            模型路由
          </span>
          <span>
            <ActivitySquare size={18} />
            运行观测
          </span>
        </div>
      </div>
    </aside>
  );
}

function AuthFrame({
  title,
  subtitle,
  children,
}: {
  title: string;
  subtitle: string;
  children: React.ReactNode;
}) {
  const [theme, setTheme] = useState(() => localStorage.getItem('agentcanvas-theme') ?? document.documentElement.dataset.theme ?? 'light');

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    localStorage.setItem('agentcanvas-theme', theme);
  }, [theme]);

  return (
    <main className="auth-page">
      <div className="auth-theme-toggle">
        <IconButton
          label={theme === 'dark' ? '切换浅色主题' : '切换深色主题'}
          onClick={() => setTheme(theme === 'dark' ? 'light' : 'dark')}
        >
          {theme === 'dark' ? <Sun size={17} /> : <Moon size={17} />}
        </IconButton>
      </div>
      <section className="auth-shell">
        <AuthBrandPanel />
        <section className="auth-card">
          <div>
            <h2>{title}</h2>
            <p className="muted">{subtitle}</p>
          </div>
          {children}
        </section>
      </section>
    </main>
  );
}

function validateEmail(email: string): string {
  if (!email.trim()) return '请输入邮箱。';
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) return '邮箱格式不正确，请输入类似 name@example.com 的地址。';
  return '';
}

function validatePassword(password: string): string {
  if (!password) return '请输入密码。';
  if (password.length < 6) return '密码至少需要 6 位。';
  return '';
}

export function LoginPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const navigate = useNavigate();
  const login = useAuthStore((state) => state.login);
  const loading = useAuthStore((state) => state.loading);
  const error = useAuthStore((state) => state.error);
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [localError, setLocalError] = useState('');

  useEffect(() => {
    const accessToken = searchParams.get('access_token');
    const refreshToken = searchParams.get('refresh_token');
    const oauthError = searchParams.get('error');

    if (oauthError) {
      setLocalError(`GitHub 登录失败: ${oauthError}`);
      setSearchParams({}, { replace: true });
    } else if (accessToken && refreshToken) {
      tokenStorage.setTokens({
        access_token: accessToken,
        refresh_token: refreshToken,
        token_type: 'Bearer',
        expires_at: '',
      });
      setSearchParams({}, { replace: true });
      void useAuthStore.getState().initialize().then(() => {
        navigate('/app/agents', { replace: true });
      });
    }
  }, [searchParams, setSearchParams, navigate]);

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    const message = validateEmail(email) || validatePassword(password);
    if (message) {
      setLocalError(message);
      return;
    }
    setLocalError('');
    try {
      await login(email, password);
      navigate('/app/agents');
    } catch {
      // 错误文案由 authStore 统一转为中文。
    }
  }

  async function githubLogin() {
    try {
      const { redirect_url } = await authApi.githubRedirect();
      window.location.href = redirect_url;
    } catch {
      setLocalError('暂时无法打开 GitHub 登录，请确认后端服务已启动。');
    }
  }

  return (
    <AuthFrame title="欢迎回来" subtitle="继续进入 Agent Canvas 工作台">
      <form className="form-stack" noValidate onSubmit={(event) => void onSubmit(event)}>
        <Field label="邮箱">
          <div className="auth-input-wrap">
            <Mail size={17} />
            <TextInput autoComplete="email" type="email" inputMode="email" value={email} onChange={(event) => setEmail(event.target.value)} placeholder="请输入邮箱地址" />
          </div>
        </Field>
        <Field label="密码">
          <div className="auth-input-wrap">
            <LockKeyhole size={17} />
            <TextInput autoComplete="current-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder="请输入密码" />
          </div>
        </Field>
        {localError || error ? <p className="auth-error">{localError || error}</p> : null}
        <Button tone="primary" disabled={loading}>
          <LockKeyhole size={17} />
          {loading ? '登录中...' : '登录'}
        </Button>
        <Button type="button" onClick={() => void githubLogin()}>
          <Github size={17} />
          GitHub 登录
        </Button>
      </form>
      <p className="muted">
        还没有账号？ <Link to="/register">创建账号</Link>
      </p>
    </AuthFrame>
  );
}

export function RegisterPage() {
  const navigate = useNavigate();
  const register = useAuthStore((state) => state.register);
  const loading = useAuthStore((state) => state.loading);
  const error = useAuthStore((state) => state.error);
  const [username, setUsername] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [localError, setLocalError] = useState('');

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    const message = (!username.trim() ? '请输入用户名。' : '') || validateEmail(email) || validatePassword(password);
    if (message) {
      setLocalError(message);
      return;
    }
    setLocalError('');
    try {
      await register({ username, email, password });
      navigate('/app/agents');
    } catch {
      // 错误文案由 authStore 统一转为中文。
    }
  }

  return (
    <AuthFrame title="创建工作台" subtitle="初始化你的 Agent Canvas 工作台">
      <form className="form-stack" noValidate onSubmit={(event) => void onSubmit(event)}>
        <Field label="用户名">
          <div className="auth-input-wrap">
            <Network size={17} />
            <TextInput autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} placeholder="你的名字" />
          </div>
        </Field>
        <Field label="邮箱">
          <div className="auth-input-wrap">
            <Mail size={17} />
            <TextInput autoComplete="email" type="email" inputMode="email" value={email} onChange={(event) => setEmail(event.target.value)} placeholder="请输入邮箱地址" />
          </div>
        </Field>
        <Field label="密码">
          <div className="auth-input-wrap">
            <LockKeyhole size={17} />
            <TextInput autoComplete="new-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder="至少 6 位" />
          </div>
        </Field>
        {localError || error ? <p className="auth-error">{localError || error}</p> : null}
        <Button tone="primary" disabled={loading}>
          <LockKeyhole size={17} />
          {loading ? '创建中...' : '创建账号'}
        </Button>
      </form>
      <p className="muted">
        已有账号？ <Link to="/login">回到登录</Link>
      </p>
    </AuthFrame>
  );
}
