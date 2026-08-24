import { useEffect, useState } from 'react';
import { settingsApi } from '../api/resources';
import { EditorialHeader } from '../components/editorial';
import { Panel } from '../components/ui';
import type { AuditLog } from '../types/api';
import { formatDate, friendlyErrorMessage } from '../utils/format';
import { MCPSettings } from './MCPSettings';
import { ProviderSettings } from './ProviderSettings';
import { SkillSettings } from './SkillSettings';
import { TokenSettings } from './TokenSettings';
import { ToolSettings } from './ToolSettings';

export { MemoryCenter, MemoryCenter as MemoryPage } from './MemoryCenter';

type ManagementView = 'settings' | 'tools' | 'skills';

const tabs = {
  settings: ['models', 'access', 'audit'],
  tools: ['http', 'mcp', 'packs'],
  skills: ['skills'],
} as const;

const tabDescriptions: Record<string, string> = {
  models: 'Providers & Models',
  access: 'API Tokens',
  audit: 'Activity history',
  http: 'HTTP endpoints',
  mcp: 'Model Context Protocol',
  packs: 'Reusable tool sets',
  skills: 'Reusable instructions',
};

function AuditSettings() {
  const [audits, setAudits] = useState<AuditLog[]>([]);
  const [error, setError] = useState('');

  useEffect(() => {
    void settingsApi.audits.list().then(setAudits).catch((err) => setError(friendlyErrorMessage(err, '加载审计日志失败')));
  }, []);

  return (
    <>
      {error ? <p className="error-text">{error}</p> : null}
      <Panel className="management-panel section-audit" title="审计日志" eyebrow="Audit Trail">
        <div className="table-wrap">
          <table className="table">
            <thead><tr><th>Action</th><th>Resource</th><th>IP</th><th>Time</th></tr></thead>
            <tbody>{audits.map((audit) => <tr key={audit.id}><td>{audit.action}</td><td>{audit.resource_type} / {audit.resource_id}</td><td>{audit.ip_address}</td><td>{formatDate(audit.created_at)}</td></tr>)}</tbody>
          </table>
        </div>
      </Panel>
    </>
  );
}

function ManagementPage({ view }: { view: ManagementView }) {
  const viewTabs = tabs[view];
  const [activeSection, setActiveSection] = useState<string>(viewTabs[0]);

  useEffect(() => { setActiveSection(viewTabs[0]); }, [view]);

  return (
    <div className="page management-page" data-management-view={view} data-active-section={activeSection}>
      <EditorialHeader
        word={view === 'settings' ? 'System' : view === 'tools' ? 'Tool' : 'Skill'}
        script={view === 'settings' ? 'Settings' : view === 'tools' ? 'Atelier' : 'Library'}
        kicker={view === 'settings' ? 'SYSTEM CONTROL / 07' : view === 'tools' ? 'TOOL GOVERNANCE / 05' : 'CAPABILITY LIBRARY / 06'}
        description={view === 'settings' ? '模型、访问与审计 · 保持系统配置清晰而专注。' : view === 'tools' ? 'HTTP、MCP 与工具治理 · 组合、测试并约束外部能力。' : '可复用能力 · 管理、校验并组织 Agent Skills。'}
      />
      <nav className="management-nav glass" aria-label="管理分类">
        {viewTabs.map((tab) => <button type="button" key={tab} className={activeSection === tab ? 'active' : ''} onClick={() => setActiveSection(tab)}><span>{tab}</span><small>{tabDescriptions[tab]}</small></button>)}
      </nav>
      <div className="management-block">
        {activeSection === 'models' ? <ProviderSettings /> : null}
        {activeSection === 'access' ? <TokenSettings /> : null}
        {activeSection === 'audit' ? <AuditSettings /> : null}
        {activeSection === 'http' ? <ToolSettings section="http" /> : null}
        {activeSection === 'mcp' ? <MCPSettings /> : null}
        {activeSection === 'packs' ? <ToolSettings section="packs" /> : null}
        {activeSection === 'skills' ? <SkillSettings /> : null}
      </div>
    </div>
  );
}

export function SettingsPage() { return <ManagementPage view="settings" />; }
export function ToolsPage() { return <ManagementPage view="tools" />; }
export function SkillsPage() { return <ManagementPage view="skills" />; }
