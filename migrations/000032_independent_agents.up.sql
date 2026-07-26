CREATE TABLE IF NOT EXISTS agents (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT,
    avatar_url VARCHAR(1024),
    status VARCHAR(32) NOT NULL DEFAULT 'draft',
    draft_definition_json JSON NOT NULL,
    current_release_id BIGINT NULL,
    legacy_dialog_id BIGINT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL,
    INDEX idx_agents_owner_status (owner_id, status),
    UNIQUE INDEX uk_agents_legacy_dialog (owner_id, legacy_dialog_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS agent_releases (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    agent_id BIGINT NOT NULL,
    version_no INT NOT NULL,
    definition_json JSON NOT NULL,
    checksum VARCHAR(64) NOT NULL,
    rule_set_hash VARCHAR(64) NOT NULL DEFAULT '',
    tool_schema_hash VARCHAR(64) NOT NULL DEFAULT '',
    resource_versions_json JSON NOT NULL,
    created_by BIGINT NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE INDEX uk_agent_release_version (agent_id, version_no),
    INDEX idx_agent_releases_owner_agent (owner_id, agent_id),
    INDEX idx_agent_releases_checksum (checksum)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS agent_turns (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    agent_id BIGINT NOT NULL,
    agent_release_id BIGINT NOT NULL,
    conversation_id BIGINT NOT NULL,
    run_id BIGINT NULL,
    user_message_id BIGINT NOT NULL,
    assistant_message_id BIGINT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'queued',
    input_json JSON NOT NULL,
    output_json JSON NULL,
    error_message TEXT,
    started_at DATETIME NULL,
    finished_at DATETIME NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE INDEX uk_agent_turn_idempotency (owner_id, conversation_id, idempotency_key),
    UNIQUE INDEX uk_agent_turn_run (run_id),
    INDEX idx_agent_turns_agent_conversation (agent_id, conversation_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

SELECT COUNT(*) INTO @has_conversation_workflow_id FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversations' AND COLUMN_NAME = 'workflow_id';
SELECT COUNT(*) INTO @has_conversation_agent_id FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversations' AND COLUMN_NAME = 'agent_id';
SELECT COUNT(*) INTO @has_conversation_agent_release_id FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversations' AND COLUMN_NAME = 'agent_release_id';
SELECT COUNT(*) INTO @has_parent_conversation_id FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversations' AND COLUMN_NAME = 'parent_conversation_id';
SELECT COUNT(*) INTO @has_idx_owner_agent FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversations' AND INDEX_NAME = 'idx_owner_agent';
SELECT COUNT(*) INTO @has_conversation_idx_agent_release FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversations' AND INDEX_NAME = 'idx_agent_release_id';
SELECT COUNT(*) INTO @has_idx_parent_conversation FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'conversations' AND INDEX_NAME = 'idx_parent_conversation_id';
SET @conversation_agent_ddl = IF(
    @has_conversation_workflow_id + @has_conversation_agent_id + @has_conversation_agent_release_id +
        @has_parent_conversation_id + @has_idx_owner_agent + @has_conversation_idx_agent_release +
        @has_idx_parent_conversation = 7,
    'SELECT 1',
    CONCAT('ALTER TABLE conversations ', CONCAT_WS(', ',
        IF(@has_conversation_workflow_id = 0, 'ADD COLUMN workflow_id BIGINT NULL', NULL),
        IF(@has_conversation_agent_id = 0, 'ADD COLUMN agent_id BIGINT NULL', NULL),
        IF(@has_conversation_agent_release_id = 0, 'ADD COLUMN agent_release_id BIGINT NULL', NULL),
        IF(@has_parent_conversation_id = 0, 'ADD COLUMN parent_conversation_id BIGINT NULL', NULL),
        IF(@has_idx_owner_agent = 0, 'ADD INDEX idx_owner_agent (owner_id, agent_id)', NULL),
        IF(@has_conversation_idx_agent_release = 0, 'ADD INDEX idx_agent_release_id (agent_release_id)', NULL),
        IF(@has_idx_parent_conversation = 0, 'ADD INDEX idx_parent_conversation_id (parent_conversation_id)', NULL)
    ))
);
PREPARE conversation_agent_stmt FROM @conversation_agent_ddl;
EXECUTE conversation_agent_stmt;
DEALLOCATE PREPARE conversation_agent_stmt;

ALTER TABLE workflow_runs
    MODIFY COLUMN workflow_id BIGINT NULL,
    MODIFY COLUMN flow_version_id BIGINT NULL;
SELECT COUNT(*) INTO @has_run_agent_id FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'workflow_runs' AND COLUMN_NAME = 'agent_id';
SELECT COUNT(*) INTO @has_run_agent_release_id FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'workflow_runs' AND COLUMN_NAME = 'agent_release_id';
SELECT COUNT(*) INTO @has_run_idx_agent FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'workflow_runs' AND INDEX_NAME = 'idx_agent_id';
SELECT COUNT(*) INTO @has_run_idx_agent_release FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'workflow_runs' AND INDEX_NAME = 'idx_agent_release_id';
SET @run_agent_ddl = IF(
    @has_run_agent_id + @has_run_agent_release_id + @has_run_idx_agent + @has_run_idx_agent_release = 4,
    'SELECT 1',
    CONCAT('ALTER TABLE workflow_runs ', CONCAT_WS(', ',
        IF(@has_run_agent_id = 0, 'ADD COLUMN agent_id BIGINT NULL', NULL),
        IF(@has_run_agent_release_id = 0, 'ADD COLUMN agent_release_id BIGINT NULL', NULL),
        IF(@has_run_idx_agent = 0, 'ADD INDEX idx_agent_id (agent_id)', NULL),
        IF(@has_run_idx_agent_release = 0, 'ADD INDEX idx_agent_release_id (agent_release_id)', NULL)
    ))
);
PREPARE run_agent_stmt FROM @run_agent_ddl;
EXECUTE run_agent_stmt;
DEALLOCATE PREPARE run_agent_stmt;

ALTER TABLE agent_reflections
    MODIFY COLUMN workflow_id BIGINT NULL,
    MODIFY COLUMN scope ENUM('node','workflow','agent','global') NOT NULL DEFAULT 'workflow';
SELECT COUNT(*) INTO @has_reflection_agent_id FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_reflections' AND COLUMN_NAME = 'agent_id';
SELECT COUNT(*) INTO @has_reflection_agent_content_idx FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_reflections' AND INDEX_NAME = 'uk_owner_agent_content';
SELECT COUNT(*) INTO @has_reflection_agent_scope_idx FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_reflections' AND INDEX_NAME = 'idx_reflections_agent_scope';
SET @reflection_agent_ddl = IF(
    @has_reflection_agent_id + @has_reflection_agent_content_idx + @has_reflection_agent_scope_idx = 3,
    'SELECT 1',
    CONCAT('ALTER TABLE agent_reflections ', CONCAT_WS(', ',
        IF(@has_reflection_agent_id = 0, 'ADD COLUMN agent_id BIGINT NULL', NULL),
        IF(@has_reflection_agent_content_idx = 0, 'ADD UNIQUE INDEX uk_owner_agent_content (owner_id, agent_id, content_hash)', NULL),
        IF(@has_reflection_agent_scope_idx = 0, 'ADD INDEX idx_reflections_agent_scope (owner_id, agent_id, status)', NULL)
    ))
);
PREPARE reflection_agent_stmt FROM @reflection_agent_ddl;
EXECUTE reflection_agent_stmt;
DEALLOCATE PREPARE reflection_agent_stmt;

ALTER TABLE agent_reflection_jobs
    MODIFY COLUMN workflow_id BIGINT NULL;
SELECT COUNT(*) INTO @has_reflection_job_agent_id FROM information_schema.COLUMNS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_reflection_jobs' AND COLUMN_NAME = 'agent_id';
SELECT COUNT(*) INTO @has_reflection_job_agent_idx FROM information_schema.STATISTICS
WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'agent_reflection_jobs' AND INDEX_NAME = 'idx_reflection_jobs_agent';
SET @reflection_job_agent_ddl = IF(
    @has_reflection_job_agent_id + @has_reflection_job_agent_idx = 2,
    'SELECT 1',
    CONCAT('ALTER TABLE agent_reflection_jobs ', CONCAT_WS(', ',
        IF(@has_reflection_job_agent_id = 0, 'ADD COLUMN agent_id BIGINT NULL', NULL),
        IF(@has_reflection_job_agent_idx = 0, 'ADD INDEX idx_reflection_jobs_agent (owner_id, agent_id, status)', NULL)
    ))
);
PREPARE reflection_job_agent_stmt FROM @reflection_job_agent_ddl;
EXECUTE reflection_job_agent_stmt;
DEALLOCATE PREPARE reflection_job_agent_stmt;
