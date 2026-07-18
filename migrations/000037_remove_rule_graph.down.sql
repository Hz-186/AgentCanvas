ALTER TABLE workflow_profiles
    ADD COLUMN rule_compiler_provider_id BIGINT NULL AFTER active_rule_set_id,
    ADD COLUMN rule_compiler_model VARCHAR(128) NOT NULL DEFAULT '' AFTER rule_compiler_provider_id;

ALTER TABLE workflow_rule_sets
    ADD COLUMN compiler_provider_id BIGINT NULL AFTER compiled_snapshot_json,
    ADD COLUMN compiler_model VARCHAR(128) NOT NULL DEFAULT '' AFTER compiler_provider_id,
    ADD COLUMN compiler_prompt_version VARCHAR(64) NOT NULL DEFAULT '' AFTER compiler_model,
    ADD COLUMN compile_error TEXT NULL AFTER rollback_of_rule_set_id;

ALTER TABLE workflow_rule_nodes
    DROP COLUMN triggers_json,
    ADD COLUMN topological_order INT NOT NULL DEFAULT 0 AFTER token_cost,
    ADD INDEX idx_rule_nodes_order (rule_set_id, topological_order);

CREATE TABLE IF NOT EXISTS workflow_rule_edges (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    rule_set_id BIGINT NOT NULL,
    rule_id VARCHAR(128) NOT NULL,
    depends_on_rule_id VARCHAR(128) NOT NULL,
    source VARCHAR(32) NOT NULL,
    confidence DOUBLE NOT NULL DEFAULT 0,
    reason TEXT NULL,
    decision VARCHAR(16) NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_rule_set_edge (rule_set_id, rule_id, depends_on_rule_id),
    KEY idx_rule_edges_decision (rule_set_id, decision)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS workflow_rule_compile_jobs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    workflow_id BIGINT NOT NULL,
    rule_set_id BIGINT NOT NULL,
    revision BIGINT NOT NULL,
    source_hash VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'queued',
    attempts INT NOT NULL DEFAULT 0,
    worker_id VARCHAR(128) NOT NULL DEFAULT '',
    idempotency_key VARCHAR(128) NOT NULL DEFAULT '',
    compiler_provider_id BIGINT NULL,
    compiler_model VARCHAR(128) NOT NULL DEFAULT '',
    prompt_tokens INT NOT NULL DEFAULT 0,
    completion_tokens INT NOT NULL DEFAULT 0,
    error_message TEXT NULL,
    available_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    started_at DATETIME NULL,
    finished_at DATETIME NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_rule_compile_idempotency (owner_id, workflow_id, idempotency_key),
    KEY idx_rule_compile_pending (status, available_at, id),
    KEY idx_rule_compile_set (rule_set_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
