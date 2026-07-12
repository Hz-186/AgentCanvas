ALTER TABLE workflow_runs
    DROP INDEX idx_definition_hash,
    DROP INDEX idx_run_kind,
    DROP COLUMN definition_hash,
    DROP COLUMN definition_json,
    DROP COLUMN run_kind;
