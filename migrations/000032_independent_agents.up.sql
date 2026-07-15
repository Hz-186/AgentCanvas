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

ALTER TABLE conversations
    ADD COLUMN agent_id BIGINT NULL AFTER workflow_id,
    ADD COLUMN agent_release_id BIGINT NULL AFTER agent_id,
    ADD COLUMN parent_conversation_id BIGINT NULL AFTER agent_release_id,
    ADD INDEX idx_owner_agent (owner_id, agent_id),
    ADD INDEX idx_agent_release_id (agent_release_id),
    ADD INDEX idx_parent_conversation_id (parent_conversation_id);

ALTER TABLE workflow_runs
    MODIFY COLUMN workflow_id BIGINT NULL,
    MODIFY COLUMN flow_version_id BIGINT NULL,
    ADD COLUMN agent_id BIGINT NULL AFTER flow_version_id,
    ADD COLUMN agent_release_id BIGINT NULL AFTER agent_id,
    ADD INDEX idx_agent_id (agent_id),
    ADD INDEX idx_agent_release_id (agent_release_id);

ALTER TABLE agent_reflections
    MODIFY COLUMN workflow_id BIGINT NULL,
    ADD COLUMN agent_id BIGINT NULL AFTER workflow_id,
	MODIFY COLUMN scope ENUM('node','workflow','agent','global') NOT NULL DEFAULT 'workflow',
	ADD UNIQUE INDEX uk_owner_agent_content (owner_id, agent_id, content_hash),
    ADD INDEX idx_reflections_agent_scope (owner_id, agent_id, status);

ALTER TABLE agent_reflection_jobs
    MODIFY COLUMN workflow_id BIGINT NULL,
    ADD COLUMN agent_id BIGINT NULL AFTER workflow_id,
    ADD INDEX idx_reflection_jobs_agent (owner_id, agent_id, status);
