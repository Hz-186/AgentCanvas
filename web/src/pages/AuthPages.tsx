import { FormEvent, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Github, LockKeyhole, Sparkles, Workflow } from 'lucide-react';
import { authApi } from '../api/auth';
import { Button, Field, TextInput } from '../components/ui';
import { useAuthStore } from '../stores/authStore';

function AuthFrame({
  title,
  subtitle,
  children,
}: {
  title: string;
  subtitle: string;
  children: React.ReactNode;
}) {
  return (
    <main className="auth-page">
      <section className="auth-shell glass">
        <aside className="auth-brand">
          <div className="traffic" aria-hidden="true">
            <span />
            <span />
            <span />
          </div>
          <div>
            <div className="brand-mark">
              <Sparkles size={26} />
            </div>
            <div className="auth-kicker">
              <Workflow size={14} />
              macOS 风格可视化 Agent 工作台
            </div>
            <h1>AgentCanvas</h1>
            <p>把知识库、模型 Provider 与 Agent Flow 放在同一个沉浸、清澈、可调试的工作台里。</p>
            <div className="auth-feature-grid">
              <span>液态玻璃</span>
              <span>流程运行</span>
              <span>RAG 调试</span>
            </div>
          </div>
          <p>为专注构建 Agent 而设计，不是临时拼出来的网页入口。</p>
        </aside>
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
  const navigate = useNavigate();
  const login = useAuthStore((state) => state.login);
  const loading = useAuthStore((state) => state.loading);
  const error = useAuthStore((state) => state.error);
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [localError, setLocalError] = useState('');

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
    <AuthFrame title="欢迎回来" subtitle="登录后继续构建、发布和调试你的 Agent Flow。">
      <form className="form-stack" noValidate onSubmit={(event) => void onSubmit(event)}>
        <Field label="邮箱">
          <TextInput autoComplete="email" type="email" inputMode="email" value={email} onChange={(event) => setEmail(event.target.value)} placeholder="name@example.com" />
        </Field>
        <Field label="密码">
          <TextInput autoComplete="current-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder="至少 6 位" />
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
    <AuthFrame title="创建工作台" subtitle="几秒钟后，你就能开始搭建第一个 Agent。">
      <form className="form-stack" noValidate onSubmit={(event) => void onSubmit(event)}>
        <Field label="用户名">
          <TextInput autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} placeholder="你的名字" />
        </Field>
        <Field label="邮箱">
          <TextInput autoComplete="email" type="email" inputMode="email" value={email} onChange={(event) => setEmail(event.target.value)} placeholder="name@example.com" />
        </Field>
        <Field label="密码">
          <TextInput autoComplete="new-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} placeholder="至少 6 位" />
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
