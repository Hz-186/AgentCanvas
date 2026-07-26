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

-- 000021 was already applied in some development databases before these
-- profile policy columns were appended to that migration. Reconcile those
-- databases here so the rule-set columns below have a stable anchor.
SELECT COUNT(*) INTO @has_tool_policy_json FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'workflow_profiles' AND COLUMN_NAME = 'tool_policy_json';
SELECT COUNT(*) INTO @has_memory_policy_json FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'workflow_profiles' AND COLUMN_NAME = 'memory_policy_json';
SELECT COUNT(*) INTO @has_context_policy_json FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'workflow_profiles' AND COLUMN_NAME = 'context_policy_json';
SELECT COUNT(*) INTO @has_risk_level FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'workflow_profiles' AND COLUMN_NAME = 'risk_level';
SELECT COUNT(*) INTO @has_mode FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'workflow_profiles' AND COLUMN_NAME = 'mode';
SET @profile_policy_ddl = IF(
    @has_tool_policy_json + @has_memory_policy_json + @has_context_policy_json + @has_risk_level + @has_mode = 5,
    'SELECT 1',
    CONCAT('ALTER TABLE workflow_profiles ', CONCAT_WS(', ',
        IF(@has_tool_policy_json = 0, 'ADD COLUMN tool_policy_json JSON NULL', NULL),
        IF(@has_memory_policy_json = 0, 'ADD COLUMN memory_policy_json JSON NULL', NULL),
        IF(@has_context_policy_json = 0, 'ADD COLUMN context_policy_json JSON NULL', NULL),
        IF(@has_risk_level = 0, 'ADD COLUMN risk_level VARCHAR(32) NOT NULL DEFAULT ''medium''', NULL),
        IF(@has_mode = 0, 'ADD COLUMN mode VARCHAR(32) NOT NULL DEFAULT ''react''', NULL)
    ))
);
PREPARE profile_policy_stmt FROM @profile_policy_ddl;
EXECUTE profile_policy_stmt;
DEALLOCATE PREPARE profile_policy_stmt;

SELECT COUNT(*) INTO @has_active_rule_set_id FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'workflow_profiles' AND COLUMN_NAME = 'active_rule_set_id';
SELECT COUNT(*) INTO @has_rule_compiler_provider_id FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'workflow_profiles' AND COLUMN_NAME = 'rule_compiler_provider_id';
SELECT COUNT(*) INTO @has_rule_compiler_model FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'workflow_profiles' AND COLUMN_NAME = 'rule_compiler_model';
SET @profile_rule_ddl = IF(
    @has_active_rule_set_id + @has_rule_compiler_provider_id + @has_rule_compiler_model = 3,
    'SELECT 1',
    CONCAT('ALTER TABLE workflow_profiles ', CONCAT_WS(', ',
        IF(@has_active_rule_set_id = 0, 'ADD COLUMN active_rule_set_id BIGINT NULL', NULL),
        IF(@has_rule_compiler_provider_id = 0, 'ADD COLUMN rule_compiler_provider_id BIGINT NULL', NULL),
        IF(@has_rule_compiler_model = 0, 'ADD COLUMN rule_compiler_model VARCHAR(128) NOT NULL DEFAULT ''''', NULL)
    ))
);
PREPARE profile_rule_stmt FROM @profile_rule_ddl;
EXECUTE profile_rule_stmt;
DEALLOCATE PREPARE profile_rule_stmt;

SELECT COUNT(*) INTO @has_rule_set_id FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'workflow_runs' AND COLUMN_NAME = 'rule_set_id';
SELECT COUNT(*) INTO @has_rule_set_version FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'workflow_runs' AND COLUMN_NAME = 'rule_set_version';
SELECT COUNT(*) INTO @has_compiled_rule_hash FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'workflow_runs' AND COLUMN_NAME = 'compiled_rule_hash';
SET @run_rule_ddl = IF(
    @has_rule_set_id + @has_rule_set_version + @has_compiled_rule_hash = 3,
    'SELECT 1',
    CONCAT('ALTER TABLE workflow_runs ', CONCAT_WS(', ',
        IF(@has_rule_set_id = 0, 'ADD COLUMN rule_set_id BIGINT NULL', NULL),
        IF(@has_rule_set_version = 0, 'ADD COLUMN rule_set_version VARCHAR(64) NOT NULL DEFAULT ''''', NULL),
        IF(@has_compiled_rule_hash = 0, 'ADD COLUMN compiled_rule_hash VARCHAR(64) NOT NULL DEFAULT ''''', NULL)
    ))
);
PREPARE run_rule_stmt FROM @run_rule_ddl;
EXECUTE run_rule_stmt;
DEALLOCATE PREPARE run_rule_stmt;
