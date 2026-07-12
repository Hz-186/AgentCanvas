import type { NodeProps } from '@xyflow/react';
import { Bot, BrainCircuit, Database, PlugZap, ShieldCheck, Wrench } from 'lucide-react';
import { agentModeFromConfig, numberArray } from '../../config';
import type { CanvasNode } from '../../types';
import { NodeWrapper } from './NodeWrapper';

const modeLabels: Record<string, string> = {
  react: 'ReAct',
  plan_execute: 'Plan & Execute',
};

export function AgentLoopNode({ data, selected }: NodeProps<CanvasNode>) {
  const config = data.config as Record<string, unknown>;
  const mode = agentModeFromConfig(config);
  const toolCount = numberArray(config.tool_ids).length;
  const skillCount = numberArray(config.skill_ids).length;
  const kbCount = numberArray(config.knowledge_ids).length;
  const mcpCount = numberArray(config.mcp_server_ids).length;
  const subAgentCount = numberArray(config.call_workflow_ids).length;
  const model = String(config.model || '继承 Profile');
  const totalTools = toolCount + kbCount + mcpCount + subAgentCount;
  return (
    <NodeWrapper data={data} selected={selected} className="agent-loop-node node-kind-agent_loop">
      <div className="agent-node-topline">
        <div className="workflow-node-head">
          <div className="node-icon agent-node-icon">
            <Bot size={16} />
          </div>
          <div className="min-w-0">
            <strong className="truncate">{data.label}</strong>
            <span className="truncate">Agent Loop</span>
          </div>
        </div>
        <span className={`agent-mode-badge agent-mode-${mode}`}>{modeLabels[mode]}</span>
      </div>
      <div className="agent-node-model truncate">
        <BrainCircuit size={13} />
        <span className="truncate">{model}</span>
      </div>
      <div className="agent-node-metrics">
        <span title="HTTP tools"><Wrench size={12} /><small>TOOLS</small><strong>{toolCount}</strong></span>
        <span title="Knowledge tools"><Database size={12} /><small>KNOWLEDGE</small><strong>{kbCount}</strong></span>
        <span title="MCP servers"><PlugZap size={12} /><small>MCP</small><strong>{mcpCount}</strong></span>
        <span title="Sub agents"><Bot size={12} /><small>AGENTS</small><strong>{subAgentCount}</strong></span>
      </div>
      <div className="agent-node-footer">
        <span><i />{totalTools} dependencies</span>
        <span>{skillCount} skills</span>
        <span>{Number(config.max_iterations ?? 8)} loops</span>
        {config.reflection_enabled ? <span><ShieldCheck size={12} />reflect</span> : null}
      </div>
      <div className="agent-dock-label" aria-hidden="true">DEPENDENCY DOCK</div>
    </NodeWrapper>
  );
}
