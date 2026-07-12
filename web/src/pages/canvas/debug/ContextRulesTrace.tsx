import { StatusBadge } from '../../../components/ui';

type RecordValue = Record<string, unknown>;

function record(value: unknown): RecordValue {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as RecordValue : {};
}

function number(value: unknown): number {
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

function names(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((item): item is string => typeof item === 'string' && item.length > 0) : [];
}

function formatTokens(value: number) {
  return new Intl.NumberFormat('en-US').format(value);
}

function levelLabel(level: string) {
  return level.replace('l0_safety', 'L0 safety').replace('l1_core', 'L1 core').replace('l2_scenario', 'L2 scenario').replace('l3_tool', 'L3 tool').replace('l4_ephemeral', 'L4 ephemeral');
}

export function ContextRulesTrace({ trace }: { trace: unknown }) {
  const context = record(trace);
  const ruleTrace = record(context.rule_trace);
  const budget = record(context.rule_budget);
  const rounds = Array.isArray(context.rule_rounds) ? context.rule_rounds.map(record) : [];
  const loaded = names(ruleTrace.loaded);
  const saved = number(context.saved_tokens);
  const inputBudget = number(budget.input_budget_tokens) || number(context.max_input_tokens);
  const availableRules = number(budget.available_rule_tokens);
  const usedRules = number(ruleTrace.estimated_used);
  const levelUsage = record(ruleTrace.level_usage);
  const providerPrompt = number(context.provider_prompt_tokens);
  const estimateError = number(context.token_estimation_error);
  const ruleSetVersion = typeof context.rule_set_version === 'string' ? context.rule_set_version : '';

  if (loaded.length === 0 && rounds.length === 0 && saved === 0) return null;

  return (
    <div className="context-rules-trace">
      <div className="context-rules-summary">
        <div><span>Input budget</span><strong>{formatTokens(inputBudget)} tok</strong></div>
        <div><span>Rules used</span><strong>{formatTokens(usedRules)} tok</strong></div>
        <div><span>Rules free</span><strong>{formatTokens(availableRules)} tok</strong></div>
        <div><span>Context saved</span><strong>{formatTokens(saved)} tok</strong></div>
        {providerPrompt > 0 ? <div><span>Provider prompt</span><strong>{formatTokens(providerPrompt)} tok</strong></div> : null}
        {providerPrompt > 0 ? <div><span>Estimate delta</span><strong>{estimateError > 0 ? '+' : ''}{formatTokens(estimateError)} tok</strong></div> : null}
      </div>
      {ruleSetVersion ? <div className="context-rules-version"><span>Rule set</span><StatusBadge tone="neutral">{ruleSetVersion}</StatusBadge></div> : null}
      {context.core_overflow === true ? <p className="context-rules-warning">Core rules exceeded the configured input budget. This run was rejected before model execution.</p> : null}
      {loaded.length > 0 ? (
        <div className="context-rules-loaded">
          <span>Active rules</span>
          <div>{loaded.map((item) => <StatusBadge key={item} tone={item.startsWith('safety.') || item.startsWith('core.') ? 'warn' : 'info'}>{item}</StatusBadge>)}</div>
        </div>
      ) : null}
      {Object.keys(levelUsage).length > 0 ? (
        <div className="context-rules-levels">
          {Object.entries(levelUsage).map(([level, tokens]) => <span key={level}>{levelLabel(level)} <strong>{formatTokens(number(tokens))}</strong></span>)}
        </div>
      ) : null}
      {rounds.length > 0 ? (
        <div className="context-rules-rounds">
          {rounds.map((round, index) => {
            const added = names(round.loaded);
            const removed = names(round.removed);
            const roundBudget = record(round.budget);
            return (
              <article key={`${number(round.iteration)}-${index}`} className="context-rule-round">
                <div className="context-rule-round-head">
                  <strong>Round {number(round.iteration) || index + 1}</strong>
                  <span>{formatTokens(number(roundBudget.available_rule_tokens))} tok available</span>
                </div>
                {added.length > 0 ? <p><span>Added</span>{added.join(', ')}</p> : null}
                {removed.length > 0 ? <p><span>Removed</span>{removed.join(', ')}</p> : null}
                {added.length === 0 && removed.length === 0 ? <p><span>Stable</span>No rule changes</p> : null}
              </article>
            );
          })}
        </div>
      ) : null}
    </div>
  );
}
