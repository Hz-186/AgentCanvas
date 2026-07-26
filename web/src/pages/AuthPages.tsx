import { useEffect, useState, type FormEvent } from 'react';
import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { ArrowRight, Github, LockKeyhole, Mail, Network } from 'lucide-react';
import { authApi } from '../api/auth';
import { tokenStorage } from '../api/token';
import { AgentCanvasMark } from '../components/editorial';
import { Button, Field, TextInput } from '../components/ui';
import { ThemeControl } from '../components/ThemeControl';
import { useAuthStore } from '../stores/authStore';
import { friendlyErrorMessage } from '../utils/format';

const TERMINAL_FRAMES = [
  [
    '      +--[MEMORY]---+        +--{CACHE}',
    '      |              |        |',
    '[INPUT]-->(01 AGENT)--+-->{TOOL}--+-->{OUT}',
    '      |              |        |',
    '      +--[POLICY]----+-->{TRACE}--+ ',
    '             \\---[REFLEXION]---/    ',
  ],
  [
    '      +==[MEMORY]===+        +=={CACHE}',
    '      :              |        |',
    '[INPUT]==>(01 AGENT)==+==>{TOOL}==+==>{OUT}',
    '      |              :        |',
    '      +==[POLICY]====+==>{TRACE}==+',
    '             \\===[REFLEXION]===/    ',
  ],
  [
    '      +--[MEMORY]---+        +--{CACHE}',
    '      |              :        |',
    '[INPUT]-->(01 AGENT)--+-->{TOOL}==+-->{OUT}',
    '      |              |        :',
    '      +==[POLICY]====+-->{TRACE}--+',
    '             \\---[REFLEXION]===/    ',
  ],
  [
    '      +--[MEMORY]===+        +=={CACHE}',
    '      :              |        |',
    '[INPUT]==>(01 AGENT)==+-->{TOOL}--+==>{OUT}',
    '      |              :        |',
    '      +--[POLICY]----+==>{TRACE}==+',
    '             \\===[REFLEXION]---/    ',
  ],
];

function AgentRuntimeTerminal() {
  const [frame, setFrame] = useState(0);

  useEffect(() => {
    const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
    if (reducedMotion.matches) return undefined;
    const timer = window.setInterval(() => {
      setFrame((value) => (value + 1) % TERMINAL_FRAMES.length);
    }, 760);
    return () => window.clearInterval(timer);
  }, []);

  return (
    <div className={`auth-terminal auth-terminal-frame-${frame}`} aria-label="Animated Agent runtime topology">
      <div className="auth-terminal-bar">
        <span>AC://RUNTIME/GRAPH</span>
        <span className="auth-terminal-state"><i /> LIVE</span>
      </div>
      <pre aria-hidden="true"><code>{TERMINAL_FRAMES[frame].join('\n')}</code></pre>
      <div className="auth-terminal-command">
        <span>$</span> agentcanvas run workflow.ac <b>_</b>
      </div>
    </div>
  );
}

function AuthBrandPanel() {

  return (
    <aside className="auth-brand">
      <div className="auth-brand-content">
        <div className="auth-brand-lockup">
          <span className="brand-mark"><AgentCanvasMark size={38} /></span>
          <span><strong>AGENTCANVAS</strong><small>VISUAL AGENT ENGINEERING</small></span>
        </div>
        <div className="auth-brand-copy">
          <span className="auth-brand-kicker">SYSTEMS / NOT SLIDES</span>
          <h1><span>BUILD AGENTS.</span><span>SEE THE SYSTEM.</span></h1>
          <p>Design the graph. Route the models.<br />Trace every decision.</p>
        </div>
        <AgentRuntimeTerminal />
        <div className="auth-feature-grid">
          <span><b>01</b>FLOW GRAPH</span>
          <span><b>02</b>MODEL ROUTING</span>
          <span><b>03</b>LIVE TRACING</span>
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
        <div className="auth-shell-meta" aria-hidden="true">
          <span>AC / ACCESS NODE</span>
          <span>26.07 / ONLINE</span>
        </div>
        <AuthBrandPanel />
        <div className="auth-mirror-stage">
          <div className="auth-mirror-edge" aria-hidden="true" />
          <section className="auth-card">
            <nav className="auth-mode-switch" aria-label="Authentication mode">
              <Link className={mode === 'login' ? 'active' : ''} to="/login">Sign in</Link>
              <Link className={mode === 'register' ? 'active' : ''} to="/register">Register</Link>
            </nav>
            <div className="auth-card-heading">
              <span className="auth-step">{mode === 'login' ? '01 / ACCESS' : '02 / IDENTITY'}</span>
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
    <AuthFrame mode="login" title="Enter the runtime." subtitle="Authenticate to resume your agent systems.">
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
    <AuthFrame mode="register" title="Create operator ID." subtitle="Provision a new identity for this runtime.">
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
