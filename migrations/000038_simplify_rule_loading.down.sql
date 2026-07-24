ALTER TABLE workflow_runs
    CHANGE COLUMN rule_set_hash compiled_rule_hash VARCHAR(64) NOT NULL DEFAULT '';

ALTER TABLE workflow_rule_nodes
    ADD COLUMN triggers_json JSON NULL AFTER activation_json,
    ADD COLUMN token_cost INT NOT NULL DEFAULT 0 AFTER policy_binding_json,
    ADD COLUMN content_hash VARCHAR(64) NOT NULL DEFAULT '' AFTER token_cost;

ALTER TABLE workflow_rule_sets
    DROP INDEX idx_workflow_rule_set_hash,
    ADD COLUMN source_hash VARCHAR(64) NOT NULL DEFAULT '' AFTER revision,
    CHANGE COLUMN rule_hash compiled_hash VARCHAR(64) NOT NULL DEFAULT '',
    CHANGE COLUMN rule_snapshot_json compiled_snapshot_json JSON NULL,
    ADD COLUMN token_estimator_version VARCHAR(64) NOT NULL DEFAULT '' AFTER compiled_snapshot_json,
    ADD KEY idx_workflow_rule_set_hash (compiled_hash);
