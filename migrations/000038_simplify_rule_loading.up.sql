ALTER TABLE workflow_rule_sets
    DROP INDEX idx_workflow_rule_set_hash,
    DROP COLUMN source_hash,
    CHANGE COLUMN compiled_hash rule_hash VARCHAR(64) NOT NULL DEFAULT '',
    CHANGE COLUMN compiled_snapshot_json rule_snapshot_json JSON NULL,
    DROP COLUMN token_estimator_version,
    ADD KEY idx_workflow_rule_set_hash (rule_hash);

ALTER TABLE workflow_rule_nodes
    DROP COLUMN triggers_json,
    DROP COLUMN token_cost,
    DROP COLUMN content_hash;

ALTER TABLE workflow_runs
    CHANGE COLUMN compiled_rule_hash rule_set_hash VARCHAR(64) NOT NULL DEFAULT '';
