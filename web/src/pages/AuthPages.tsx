import { FormEvent, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Github, Sparkles } from 'lucide-react';
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
            <h1>AgentCanvas</h1>
            <p>把知识库、模型 Provider 与 Agent Flow 放在同一个精致、克制、可调试的工作台里。</p>
          </div>
          <p>Liquid glass interface · REST/SSE runtime · macOS inspired</p>
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

export function LoginPage() {
  const navigate = useNavigate();
  const login = useAuthStore((state) => state.login);
  const loading = useAuthStore((state) => state.loading);
  const error = useAuthStore((state) => state.error);
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    await login(email, password);
    navigate('/app/agents');
  }

  async function githubLogin() {
    const { redirect_url } = await authApi.githubRedirect();
    window.location.href = redirect_url;
  }

  return (
    <AuthFrame title="欢迎回来" subtitle="登录后继续构建和调试你的 Agent Flow。">
      <form className="form-stack" onSubmit={(event) => void onSubmit(event)}>
        <Field label="邮箱">
          <TextInput autoComplete="email" type="email" value={email} onChange={(event) => setEmail(event.target.value)} required />
        </Field>
        <Field label="密码">
          <TextInput autoComplete="current-password" type="password" value={password} onChange={(event) => setPassword(event.target.value)} required />
        </Field>
        {error ? <p className="error-text">{error}</p> : null}
        <Button tone="primary" disabled={loading}>
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

  async function onSubmit(event: FormEvent) {
    event.preventDefault();
    await register({ username, email, password });
    navigate('/app/agents');
  }

  return (
    <AuthFrame title="创建工作台" subtitle="几秒钟后，你就能开始搭建第一个 Agent。">
      <form className="form-stack" onSubmit={(event) => void onSubmit(event)}>
        <Field label="用户名">
          <TextInput autoComplete="username" value={username} onChange={(event) => setUsername(event.target.value)} required />
        </Field>
        <Field label="邮箱">
          <TextInput autoComplete="email" type="email" value={email} onChange={(event) => setEmail(event.target.value)} required />
        </Field>
        <Field label="密码">
          <TextInput autoComplete="new-password" type="password" minLength={6} value={password} onChange={(event) => setPassword(event.target.value)} required />
        </Field>
        {error ? <p className="error-text">{error}</p> : null}
        <Button tone="primary" disabled={loading}>
          {loading ? '创建中...' : '创建账号'}
        </Button>
      </form>
      <p className="muted">
        已有账号？ <Link to="/login">回到登录</Link>
      </p>
    </AuthFrame>
  );
}
