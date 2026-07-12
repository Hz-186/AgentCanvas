import { useCallback, useEffect, useRef, useState, type FormEvent, type PointerEvent as ReactPointerEvent } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { ActivitySquare, ArrowRight, Github, LockKeyhole, Mail, Network, Route, Sparkles, Workflow } from 'lucide-react';
import { authApi } from '../api/auth';
import { tokenStorage } from '../api/token';
import { Button, Field, TextInput } from '../components/ui';
import { ThemeControl } from '../components/ThemeControl';
import { useAuthStore } from '../stores/authStore';
import { friendlyErrorMessage } from '../utils/format';

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
        <div className="auth-brand-kicker">
          <span className="brand-mark"><Sparkles size={20} /></span>
          <span>INTELLIGENT WORKFLOW STUDIO</span>
        </div>
        <h1><span>Agent</span> <em>Canvas</em></h1>
        <p>Compose intelligence.<br />Make every decision visible.</p>
        <div className="auth-feature-grid">
          <span>
            <Workflow size={18} />
            FLOW DESIGN
          </span>
          <span>
            <Route size={18} />
            MODEL ROUTING
          </span>
          <span>
            <ActivitySquare size={18} />
            LIVE TRACING
          </span>
        </div>
      </div>
    </aside>
  );
}

function AuthFrame({
  mode,
  title,
  subtitle,
  children,
}: {
  mode: 'login' | 'register';
  title: string;
  subtitle: string;
  children: React.ReactNode;
}) {
  return (
    <main className="auth-page">
      <div className="auth-theme-toggle">
        <ThemeControl />
      </div>
      <section className="auth-shell">
        <AuthBrandPanel />
        <div className="auth-mirror-stage">
          <div className="auth-mirror-edge" aria-hidden="true" />
          <section className="auth-card">
            <nav className="auth-mode-switch" aria-label="Authentication mode">
              <Link className={mode === 'login' ? 'active' : ''} to="/login">Sign in</Link>
              <Link className={mode === 'register' ? 'active' : ''} to="/register">Register</Link>
            </nav>
            <div className="auth-card-heading">
              <span className="auth-step">{mode === 'login' ? '01 / ACCESS' : '01 / ACCOUNT'}</span>
              <h2>{title}</h2>
              <p className="muted">{subtitle}</p>
            </div>
            {children}
          </section>
        </div>
      </section>
    </main>
  );
}

function validateEmail(email: string): string {
  if (!email.trim()) return 'Enter your email address.';
  if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email)) return 'Use a valid address such as name@example.com.';
  return '';
}

function validatePassword(password: string): string {
  if (!password) return 'Enter your password.';
  if (password.length < 8) return 'Password must contain at least 8 characters.';
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
    const oauthCode = searchParams.get('oauth_code');
    const oauthError = searchParams.get('error');

    if (oauthError) {
      setLocalError(`GitHub sign-in failed: ${oauthError}`);
      setSearchParams({}, { replace: true });
    } else if (oauthCode) {
      setSearchParams({}, { replace: true });
      void authApi.exchangeOAuthCode(oauthCode).then((resp) => {
        tokenStorage.setTokens(resp.tokens);
        tokenStorage.setUser(resp.user);
        useAuthStore.setState({ user: resp.user, error: '' });
		navigate('/app/workflows', { replace: true });
      }).catch((err) => {
        setLocalError(friendlyErrorMessage(err, 'GitHub sign-in failed. Please try again.'));
      });
    } else if (accessToken && refreshToken) {
      tokenStorage.setTokens({
        access_token: accessToken,
        refresh_token: refreshToken,
        token_type: 'Bearer',
        expires_at: '',
      });
      setSearchParams({}, { replace: true });
      void useAuthStore.getState().initialize().then(() => {
		navigate('/app/workflows', { replace: true });
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
		navigate('/app/workflows');
    } catch {
      // Error copy is normalized by authStore.
    }
  }

  async function githubLogin() {
    try {
      const { redirect_url } = await authApi.githubRedirect();
      window.location.href = redirect_url;
    } catch {
      setLocalError('GitHub sign-in is unavailable. Check the OAuth configuration and backend status.');
    }
  }

  return (
    <AuthFrame mode="login" title="Welcome back." subtitle="Enter your workspace and continue building.">
      <form className="form-stack" noValidate onSubmit={(event) => void onSubmit(event)}>
        <Field label="EMAIL">
          <div className="auth-input-wrap">
            <Mail size={17} />
            <TextInput autoComplete="email" type="email" inputMode="email" value={email} onChange={(event) => setEmail(event.target.value)} placeholder="name@example.com" />
          </div>
        </Field>
        <Field label="PASSWORD">
          <div className="auth-input-wrap">
            <LockKeyhole size={17} />
            <TextInput autoComplete="current-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder="Enter password" />
          </div>
        </Field>
        {localError || error ? <p className="auth-error">{localError || error}</p> : null}
        <Button tone="primary" disabled={loading}>
          {loading ? 'Signing in...' : 'Continue'}
          <ArrowRight size={17} />
        </Button>
        <Button type="button" onClick={() => void githubLogin()}>
          <Github size={17} />
          Continue with GitHub
        </Button>
      </form>
      <p className="muted">
        New to Agent Canvas? <Link to="/register">Create an account</Link>
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
    const message = (!username.trim() ? 'Enter your name.' : '') || validateEmail(email) || validatePassword(password);
    if (message) {
      setLocalError(message);
      return;
    }
    setLocalError('');
    try {
      await register({ username, email, password });
		navigate('/app/workflows');
    } catch {
      // Error copy is normalized by authStore.
    }
  }

  return (
    <AuthFrame mode="register" title="Begin here." subtitle="Create an identity for your new workspace.">
      <form className="form-stack" noValidate onSubmit={(event) => void onSubmit(event)}>
        <Field label="NAME">
          <div className="auth-input-wrap">
            <Network size={17} />
            <TextInput autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} placeholder="Your name" />
          </div>
        </Field>
        <Field label="EMAIL">
          <div className="auth-input-wrap">
            <Mail size={17} />
            <TextInput autoComplete="email" type="email" inputMode="email" value={email} onChange={(event) => setEmail(event.target.value)} placeholder="name@example.com" />
          </div>
        </Field>
        <Field label="PASSWORD">
          <div className="auth-input-wrap">
            <LockKeyhole size={17} />
            <TextInput autoComplete="new-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder="8 characters minimum" />
          </div>
        </Field>
        {localError || error ? <p className="auth-error">{localError || error}</p> : null}
        <Button tone="primary" disabled={loading}>
          {loading ? 'Creating...' : 'Create workspace'}
          <ArrowRight size={17} />
        </Button>
      </form>
      <p className="muted">
        Already have an account? <Link to="/login">Sign in</Link>
      </p>
    </AuthFrame>
  );
}
