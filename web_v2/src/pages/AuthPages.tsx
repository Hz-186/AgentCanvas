import { FormEvent, useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Github, Loader2, Network } from 'lucide-react';
import { Button, Field, TextInput } from '@/components/ui';
import { ThemeSwitch } from '@/components/ThemeSwitch';
import { PaperSurface } from '@/components/manuscript/PaperSurface';
import { useAuthStore } from '@/stores/authStore';

function AuthFrame({ mode }: { mode: 'login' | 'register' }) {
  const navigate = useNavigate();
  const login = useAuthStore((s) => s.login);
  const register = useAuthStore((s) => s.register);
  const loading = useAuthStore((s) => s.loading);
  const error = useAuthStore((s) => s.error);
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [username, setUsername] = useState('');

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (mode === 'login') await login(email, password);
    else await register({ username, email, password });
    navigate('/app/workflows', { replace: true });
  };

  return (
    <div className="auth-shell">
      <section className="auth-hero manuscript-plain">
        <div className="auth-brand"><span className="wax-seal"><Network size={22} /></span><span><strong>AgentCanvas</strong><small>Officina Agentium · folio II</small></span></div>
        <div className="auth-plate-notes" aria-hidden="true"><span>FIG. XII</span><i /><strong>Machina rationis</strong><p>memoria · instrumenta · iudicium · responsum</p></div>
      </section>
      <section className="auth-paper-stack">
        <PaperSurface className="auth-panel" variant="folio" hardware="paperclip" rotation={mode === 'login' ? -1.2 : .8}>
          <div className="paper-ribbon auth-ribbon">{mode === 'login' ? 'Accesso all’officina' : 'Nuovo taccuino'}</div>
          <div className="panel-header auth-panel-head">
            <div><span className="folio-index">Folio · {mode === 'login' ? 'VII' : 'VIII'}</span><h3>{mode === 'login' ? '登录 AgentCanvas' : '创建账户'}</h3><p>继续进入手稿工作台</p></div>
          <ThemeSwitch />
          </div>
          <form className="auth-form" onSubmit={submit}>
            {mode === 'register' ? <Field label="用户名"><TextInput value={username} onChange={(e) => setUsername(e.target.value)} required /></Field> : null}
            <Field label="邮箱"><TextInput type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" required /></Field>
            <Field label="密码"><TextInput type="password" value={password} onChange={(e) => setPassword(e.target.value)} autoComplete={mode === 'login' ? 'current-password' : 'new-password'} required minLength={8} /></Field>
            {error ? <div className="editorial-error"><span>×</span>{error}</div> : null}
            <Button tone="primary" type="submit" disabled={loading}>{loading ? <Loader2 size={17} className="spin" /> : null}{mode === 'login' ? '展开工作台' : '装订新账户'}</Button>
            <div className="ink-or"><span>vel</span></div>
            <Button type="button" tone="secondary"><Github size={17} /> 通过 GitHub 进入</Button>
          </form>
          <p className="auth-switch">
            {mode === 'login' ? <>还没有账户？ <Link to="/register">另起一页</Link></> : <>已有账户？ <Link to="/login">返回前页</Link></>}
          </p>
          <div className="auth-signature" aria-hidden="true">ordine · ingegno · memoria</div>
        </PaperSurface>
      </section>
    </div>
  );
}

export function LoginPage() { return <AuthFrame mode="login" />; }
export function RegisterPage() { return <AuthFrame mode="register" />; }
