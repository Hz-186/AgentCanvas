ALTER TABLE agent_runs
    ADD COLUMN parent_run_id BIGINT NULL AFTER conversation_id,
    ADD COLUMN caller_node_id VARCHAR(128) NULL AFTER parent_run_id,
    ADD COLUMN call_depth INT NOT NULL DEFAULT 0 AFTER caller_node_id,
    ADD INDEX idx_parent_run_id (parent_run_id),
    ADD INDEX idx_call_depth (call_depth);
