ALTER TABLE workflow_runs
    ADD COLUMN run_kind VARCHAR(32) NOT NULL DEFAULT 'workflow' AFTER flow_version_id,
    ADD COLUMN definition_json JSON NULL AFTER run_kind,
    ADD COLUMN definition_hash VARCHAR(64) NOT NULL DEFAULT '' AFTER definition_json,
    ADD INDEX idx_run_kind (run_kind),
    ADD INDEX idx_definition_hash (definition_hash);
