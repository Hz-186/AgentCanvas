import { agentModeFromConfig, numberArray, stringArray } from '../../config';
import type { AgentMode } from '../../types';

export function useAgentFormValues(config: Record<string, unknown>) {
  return {
    mode: agentModeFromConfig(config),
    providerId: Number(config.provider_id ?? 0),
    model: String(config.model ?? ''),
    temperature: Number(config.temperature ?? 0.2),
    systemPrompt: String(config.system_prompt ?? ''),
    taskTemplate: String(config.task_template ?? ''),
    toolIds: numberArray(config.tool_ids),
    skillIds: numberArray(config.skill_ids),
    knowledgeIds: numberArray(config.knowledge_ids),
    mcpServerIds: numberArray(config.mcp_server_ids),
    callWorkflowIds: numberArray(config.call_workflow_ids),
    skillLoadingMode: String(config.skill_loading_mode ?? 'metadata_only'),
    riskLevels: stringArray(config.require_approval_for_risk, ['high']),
    outputMode: String(config.output_mode ?? 'final_answer'),
  } satisfies {
    mode: AgentMode;
    providerId: number;
    model: string;
    temperature: number;
    systemPrompt: string;
    taskTemplate: string;
    toolIds: number[];
    skillIds: number[];
    knowledgeIds: number[];
    mcpServerIds: number[];
    callWorkflowIds: number[];
    skillLoadingMode: string;
    riskLevels: string[];
    outputMode: string;
  };
}
