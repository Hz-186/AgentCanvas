import { NavLink, Outlet, useLocation } from 'react-router-dom';
import { Bot, Database, LogOut, MemoryStick, MessageCircle, Settings, Workflow } from 'lucide-react';
import { motion } from 'framer-motion';
import { ThemeSwitch } from '@/components/ThemeSwitch';
import { IconButton } from '@/components/ui';
import { useAuthStore } from '@/stores/authStore';

const nav = [
  { to: '/app/workflows', icon: Workflow, label: '工作流' },
  { to: '/app/knowledge', icon: Database, label: '知识库' },
  { to: '/app/dialogs', icon: MessageCircle, label: '对话' },
  { to: '/app/memory', icon: MemoryStick, label: '记忆' },
  { to: '/app/settings', icon: Settings, label: '设置' },
];

function titleFor(pathname: string): [string, string] {
  if (pathname.includes('/canvas')) return ['Canvas', 'Machina agentium · 编排与观察'];
  if (pathname.includes('/knowledge')) return ['Knowledge', 'Codices · 检索资料与文档索引'];
  if (pathname.includes('/dialogs')) return ['Dialog', 'Colloquium · 对话实验记录'];
  if (pathname.includes('/memory')) return ['Memory', 'Memoria · 长期记忆卷册'];
  if (pathname.includes('/settings')) return ['Settings', 'Instrumenta · 模型与工具配置'];
  return ['Workflows', 'Opera · 选择一份工作流手稿'];
}

export function AppShell() {
  const location = useLocation();
  const logout = useAuthStore((s) => s.logout);
  const [title, subtitle] = titleFor(location.pathname);
  return (
    <div className="shell">
      <aside className="sidebar glass">
        <div className="brand-mark"><Bot size={24} /></div>
        <nav className="nav-stack" aria-label="主导航">
          {nav.map((item) => <NavLink key={item.to} to={item.to} className={({ isActive }) => `nav-button${isActive ? ' active' : ''}`} title={item.label}><item.icon size={20} /></NavLink>)}
        </nav>
        <IconButton aria-label="退出登录" title="退出登录" onClick={() => void logout()}><LogOut size={18} /></IconButton>
      </aside>
      <main className="main-frame">
        <header className="topbar">
          <div className="topbar-title"><h1>{title}</h1><span>{subtitle}</span></div>
          <div className="topbar-actions"><ThemeSwitch /></div>
        </header>
        <motion.div className="page-surface scroll-surface" key={location.pathname} initial={{ opacity: 0, rotate: -.12, y: 5 }} animate={{ opacity: 1, rotate: 0, y: 0 }} transition={{ type: 'spring', stiffness: 210, damping: 26 }}>
          <Outlet />
        </motion.div>
      </main>
    </div>
  );
}
