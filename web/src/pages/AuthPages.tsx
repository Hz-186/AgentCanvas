import { useCallback, useEffect, useRef, useState, type FormEvent, type PointerEvent as ReactPointerEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { ActivitySquare, Github, LockKeyhole, Mail, Moon, Network, Route, Sparkles, Sun, Workflow } from 'lucide-react';
import { authApi } from '../api/auth';
import { Button, Field, IconButton, TextInput } from '../components/ui';
import { useAuthStore } from '../stores/authStore';

const LIQUID_GLASS_POINTER_QUERY = '(hover: hover) and (pointer: fine) and (prefers-reduced-motion: no-preference)';

type LensPointer = {
  x: number;
  y: number;
  active: boolean;
};

function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value));
}

function canUseLiquidGlassPointer() {
  return typeof window !== 'undefined' && window.matchMedia(LIQUID_GLASS_POINTER_QUERY).matches;
}

function roundedRectClip(context: CanvasRenderingContext2D, width: number, height: number, radius: number) {
  context.beginPath();
  context.moveTo(radius, 0);
  context.lineTo(width - radius, 0);
  context.quadraticCurveTo(width, 0, width, radius);
  context.lineTo(width, height - radius);
  context.quadraticCurveTo(width, height, width - radius, height);
  context.lineTo(radius, height);
  context.quadraticCurveTo(0, height, 0, height - radius);
  context.lineTo(0, radius);
  context.quadraticCurveTo(0, 0, radius, 0);
  context.closePath();
  context.clip();
}

function lensPoint(x: number, y: number, centerX: number, centerY: number, radius: number, strength: number) {
  const deltaX = x - centerX;
  const deltaY = y - centerY;
  const distance = Math.hypot(deltaX, deltaY);

  if (distance <= 0.01 || distance > radius) {
    return { x, y };
  }

  const normalized = distance / radius;
  const falloff = Math.pow(1 - normalized, 2.2);
  const edgePull = Math.sin(normalized * Math.PI);
  const displacement = strength * falloff * edgePull;

  return {
    x: x + (deltaX / distance) * displacement,
    y: y + (deltaY / distance) * displacement,
  };
}

function drawWarpedLine(
  context: CanvasRenderingContext2D,
  startX: number,
  startY: number,
  endX: number,
  endY: number,
  centerX: number,
  centerY: number,
  radius: number,
  strength: number,
) {
  const segments = Math.max(18, Math.ceil(Math.hypot(endX - startX, endY - startY) / 18));

  context.beginPath();
  for (let index = 0; index <= segments; index += 1) {
    const progress = index / segments;
    const x = startX + (endX - startX) * progress;
    const y = startY + (endY - startY) * progress;
    const warped = lensPoint(x, y, centerX, centerY, radius, strength);

    if (index === 0) {
      context.moveTo(warped.x, warped.y);
    } else {
      context.lineTo(warped.x, warped.y);
    }
  }
  context.stroke();
}

function drawLiquidRefraction(canvas: HTMLCanvasElement, panel: HTMLElement, pointer: LensPointer) {
  const width = Math.max(1, Math.floor(canvas.clientWidth));
  const height = Math.max(1, Math.floor(canvas.clientHeight));
  const pixelRatio = Math.min(window.devicePixelRatio || 1, 2);
  const targetWidth = Math.round(width * pixelRatio);
  const targetHeight = Math.round(height * pixelRatio);

  if (canvas.width !== targetWidth || canvas.height !== targetHeight) {
    canvas.width = targetWidth;
    canvas.height = targetHeight;
  }

  const context = canvas.getContext('2d');
  if (!context) return;

  context.setTransform(pixelRatio, 0, 0, pixelRatio, 0, 0);
  context.clearRect(0, 0, width, height);

  if (!pointer.active) return;

  const rootStyles = getComputedStyle(document.documentElement);
  const lineColor = rootStyles.getPropertyValue('--tech-line').trim() || 'rgba(0, 113, 227, 0.13)';
  const strongLineColor = rootStyles.getPropertyValue('--tech-line-strong').trim() || 'rgba(0, 113, 227, 0.22)';
  const shell = panel.closest('.auth-shell') as HTMLElement | null;
  const shellRect = shell?.getBoundingClientRect();
  const panelRect = panel.getBoundingClientRect();
  const offsetX = shellRect ? panelRect.left - shellRect.left : 0;
  const offsetY = shellRect ? panelRect.top - shellRect.top : 0;
  const gridStep = 42;
  const dotStep = 84;
  const lineStartX = -((offsetX % gridStep) + gridStep);
  const lineStartY = -((offsetY % gridStep) + gridStep);
  const dotStartX = 21 - ((offsetX % dotStep) + dotStep);
  const dotStartY = 21 - ((offsetY % dotStep) + dotStep);
  const centerX = pointer.x * width;
  const centerY = pointer.y * height;
  const radius = Math.max(width, height) * 0.64;
  const strength = Math.min(width, height) * 0.055;

  context.save();
  roundedRectClip(context, width, height, 30);

  context.lineCap = 'round';
  context.lineJoin = 'round';
  context.lineWidth = 1;
  context.strokeStyle = lineColor;
  context.globalAlpha = 0.94;

  for (let x = lineStartX; x <= width + gridStep; x += gridStep) {
    drawWarpedLine(context, x, -gridStep, x, height + gridStep, centerX, centerY, radius, strength);
  }

  for (let y = lineStartY; y <= height + gridStep; y += gridStep) {
    drawWarpedLine(context, -gridStep, y, width + gridStep, y, centerX, centerY, radius, strength);
  }

  context.fillStyle = strongLineColor;
  context.globalAlpha = 0.42;

  for (let x = dotStartX; x <= width + dotStep; x += dotStep) {
    for (let y = dotStartY; y <= height + dotStep; y += dotStep) {
      const point = lensPoint(x, y, centerX, centerY, radius, strength * 0.72);
      context.beginPath();
      context.arc(point.x, point.y, 1.25, 0, Math.PI * 2);
      context.fill();
    }
  }

  context.restore();
}

function AuthBrandPanel() {
  const panelRef = useRef<HTMLElement>(null);
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const pointerRef = useRef<LensPointer>({ x: 0.5, y: 0.46, active: false });
  const frameRef = useRef<number | null>(null);

  const scheduleDraw = useCallback(() => {
    if (frameRef.current !== null) return;

    frameRef.current = window.requestAnimationFrame(() => {
      frameRef.current = null;
      const canvas = canvasRef.current;
      const panel = panelRef.current;
      if (!canvas || !panel) return;
      drawLiquidRefraction(canvas, panel, pointerRef.current);
    });
  }, []);

  const resetPanel = useCallback(() => {
    const panel = panelRef.current;
    if (!panel) return;

    pointerRef.current = { x: 0.5, y: 0.46, active: false };
    panel.dataset.liquidActive = 'false';
    panel.style.setProperty('--tilt-x', '0deg');
    panel.style.setProperty('--tilt-y', '0deg');
    panel.style.setProperty('--shift-x', '0px');
    panel.style.setProperty('--shift-y', '0px');
    panel.style.setProperty('--lens-x', '50%');
    panel.style.setProperty('--lens-y', '46%');
    panel.style.setProperty('--lens-opacity', '0');
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

      pointerRef.current = { x, y, active: true };
      panel.dataset.liquidActive = 'true';
      panel.style.setProperty('--tilt-x', `${(-normalizedY * 8).toFixed(2)}deg`);
      panel.style.setProperty('--tilt-y', `${(normalizedX * 10).toFixed(2)}deg`);
      panel.style.setProperty('--shift-x', `${(normalizedX * 10).toFixed(2)}px`);
      panel.style.setProperty('--shift-y', `${(normalizedY * 8).toFixed(2)}px`);
      panel.style.setProperty('--lens-x', `${(x * 100).toFixed(2)}%`);
      panel.style.setProperty('--lens-y', `${(y * 100).toFixed(2)}%`);
      panel.style.setProperty('--lens-opacity', '1');
      panel.style.setProperty('--glass-shadow-x', `${(-normalizedX * 18).toFixed(2)}px`);
      panel.style.setProperty('--glass-shadow-y', `${(30 + normalizedY * 12).toFixed(2)}px`);
      panel.style.setProperty('--glass-shadow-blur', `${(72 + Math.abs(normalizedX) * 16).toFixed(2)}px`);
      scheduleDraw();
    },
    [resetPanel, scheduleDraw],
  );

  useEffect(() => {
    const panel = panelRef.current;
    if (!panel) return undefined;

    const resizeObserver = new ResizeObserver(() => {
      scheduleDraw();
    });
    const motionQuery = window.matchMedia(LIQUID_GLASS_POINTER_QUERY);
    const handleMotionChange = () => {
      if (!motionQuery.matches) resetPanel();
      scheduleDraw();
    };

    resetPanel();
    resizeObserver.observe(panel);
    motionQuery.addEventListener('change', handleMotionChange);
    scheduleDraw();

    return () => {
      resizeObserver.disconnect();
      motionQuery.removeEventListener('change', handleMotionChange);
      if (frameRef.current !== null) {
        window.cancelAnimationFrame(frameRef.current);
      }
    };
  }, [resetPanel, scheduleDraw]);

  return (
    <aside ref={panelRef} className="auth-brand" onPointerEnter={updatePanel} onPointerMove={updatePanel} onPointerLeave={resetPanel}>
      <canvas ref={canvasRef} className="auth-brand-refraction" aria-hidden="true" />
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
