CREATE TABLE IF NOT EXISTS workflow_rule_sets (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    workflow_id BIGINT NOT NULL,
    version_no INT NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'draft',
    revision BIGINT NOT NULL DEFAULT 1,
    source_hash VARCHAR(64) NOT NULL DEFAULT '',
    compiled_hash VARCHAR(64) NOT NULL DEFAULT '',
    compiled_snapshot_json JSON NULL,
    compiler_provider_id BIGINT NULL,
    compiler_model VARCHAR(128) NOT NULL DEFAULT '',
    compiler_prompt_version VARCHAR(64) NOT NULL DEFAULT '',
    token_estimator_version VARCHAR(64) NOT NULL DEFAULT '',
    rollback_of_rule_set_id BIGINT NULL,
    compile_error TEXT NULL,
    published_by BIGINT NULL,
    published_at DATETIME NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_workflow_rule_set_version (owner_id, workflow_id, version_no),
    KEY idx_workflow_rule_set_status (owner_id, workflow_id, status),
    KEY idx_workflow_rule_set_hash (compiled_hash)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS workflow_rule_nodes (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    rule_set_id BIGINT NOT NULL,
    rule_id VARCHAR(128) NOT NULL,
    name VARCHAR(255) NOT NULL DEFAULT '',
    content TEXT NOT NULL,
    strength VARCHAR(16) NOT NULL,
    activation_json JSON NULL,
    priority INT NOT NULL DEFAULT 0,
    safety_critical TINYINT(1) NOT NULL DEFAULT 0,
    policy_binding_json JSON NULL,
    token_cost INT NOT NULL DEFAULT 0,
    topological_order INT NOT NULL DEFAULT 0,
    content_hash VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_rule_set_rule_id (rule_set_id, rule_id),
    KEY idx_rule_nodes_order (rule_set_id, topological_order)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

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

ALTER TABLE workflow_profiles
    ADD COLUMN active_rule_set_id BIGINT NULL AFTER context_policy_json,
    ADD COLUMN rule_compiler_provider_id BIGINT NULL AFTER active_rule_set_id,
    ADD COLUMN rule_compiler_model VARCHAR(128) NOT NULL DEFAULT '' AFTER rule_compiler_provider_id;

ALTER TABLE workflow_runs
    ADD COLUMN rule_set_id BIGINT NULL AFTER flow_version_id,
    ADD COLUMN rule_set_version VARCHAR(64) NOT NULL DEFAULT '' AFTER rule_set_id,
    ADD COLUMN compiled_rule_hash VARCHAR(64) NOT NULL DEFAULT '' AFTER rule_set_version;

