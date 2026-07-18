UPDATE workflow_checkpoints c
JOIN workflow_runs r ON r.id = c.run_id
SET c.status = 'invalidated', c.updated_at = UTC_TIMESTAMP()
WHERE r.compiled_rule_hash <> ''
  AND r.status IN ('queued', 'running', 'waiting_human', 'paused', 'resuming');

UPDATE approval_requests a
JOIN workflow_runs r ON r.id = a.run_id
SET a.status = 'rejected',
    a.decision_note = 'Rule Graph removal migration invalidated this approval.',
    a.decided_at = UTC_TIMESTAMP(),
    a.updated_at = UTC_TIMESTAMP()
WHERE a.status = 'pending'
  AND r.compiled_rule_hash <> ''
  AND r.status IN ('queued', 'running', 'waiting_human', 'paused', 'resuming');

UPDATE workflow_runs
SET status = 'cancelled',
    error_message = 'rule_snapshot_obsolete: Rule Graph snapshots were removed by migration',
    finished_at = UTC_TIMESTAMP(),
    updated_at = UTC_TIMESTAMP()
WHERE compiled_rule_hash <> ''
  AND status IN ('queued', 'running', 'waiting_human', 'paused', 'resuming');

UPDATE agent_turns t
JOIN agent_releases r ON r.id = t.agent_release_id
SET t.status = 'cancelled',
    t.error_message = 'rule_snapshot_obsolete: Rule Graph definitions were removed by migration',
    t.finished_at = UTC_TIMESTAMP(),
    t.updated_at = UTC_TIMESTAMP()
WHERE t.status IN ('queued', 'retry_wait', 'running', 'waiting_human', 'paused')
  AND CAST(r.definition_json AS CHAR) LIKE '%"manual_depends_on"%';

UPDATE workflow_rule_sets
SET status = 'draft',
    source_hash = '',
    compiled_hash = '',
    compiled_snapshot_json = NULL,
    published_by = NULL,
    published_at = NULL,
    updated_at = UTC_TIMESTAMP()
WHERE status IN ('queued', 'compiling', 'review_required', 'ready', 'failed');

UPDATE context_resource_index_outbox
SET operation = 'delete',
    status = 'pending',
    attempt_count = 0,
    available_at = UTC_TIMESTAMP(),
    locked_by = '',
    locked_at = NULL,
    lease_expires_at = NULL,
    last_error = '',
    completed_at = NULL,
    updated_at = UTC_TIMESTAMP()
WHERE resource_type = 'optional_rule';

DROP TABLE IF EXISTS workflow_rule_compile_jobs;
DROP TABLE IF EXISTS workflow_rule_edges;

ALTER TABLE workflow_rule_nodes
    ADD COLUMN triggers_json JSON NULL AFTER activation_json,
    DROP INDEX idx_rule_nodes_order,
    DROP COLUMN topological_order;

ALTER TABLE workflow_rule_sets
    DROP COLUMN compiler_provider_id,
    DROP COLUMN compiler_model,
    DROP COLUMN compiler_prompt_version,
    DROP COLUMN compile_error;

ALTER TABLE workflow_profiles
    DROP COLUMN rule_compiler_provider_id,
    DROP COLUMN rule_compiler_model;
