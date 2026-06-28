ALTER TABLE workflow_runs
    DROP INDEX idx_call_depth,
    DROP INDEX idx_parent_run_id,
    DROP COLUMN call_depth,
    DROP COLUMN caller_node_id,
    DROP COLUMN parent_run_id;
