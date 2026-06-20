CREATE TABLE IF NOT EXISTS memories (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    conversation_id BIGINT NULL,
    memory_type VARCHAR(64) NOT NULL,
    title VARCHAR(255),
    content TEXT NOT NULL,
    importance DOUBLE NOT NULL DEFAULT 0.5,
    source VARCHAR(64),
    metadata_json JSON,
    last_used_at DATETIME NULL,
    expires_at DATETIME NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL,
    INDEX idx_owner_type (owner_id, memory_type),
    INDEX idx_conversation_id (conversation_id),
    INDEX idx_importance (importance)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS memory_write_logs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    memory_id BIGINT,
    run_id BIGINT,
    source_message_id BIGINT,
    action VARCHAR(32) NOT NULL,
    before_json JSON,
    after_json JSON,
    reason TEXT,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_owner_id (owner_id),
    INDEX idx_run_id (run_id),
    INDEX idx_memory_id (memory_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS tool_definitions (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    tool_type VARCHAR(64) NOT NULL,
    description TEXT,
    config_json JSON NOT NULL,
    input_schema_json JSON,
    output_schema_json JSON,
    status TINYINT NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL,
    INDEX idx_owner_id (owner_id),
    INDEX idx_tool_type (tool_type)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS tool_invocations (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    run_id BIGINT,
    node_id VARCHAR(128),
    tool_id BIGINT,
    tool_name VARCHAR(128),
    tool_type VARCHAR(64),
    input_json JSON,
    output_json JSON,
    status VARCHAR(32) NOT NULL,
    error_message TEXT,
    latency_ms INT NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_owner_id (owner_id),
    INDEX idx_run_id (run_id),
    INDEX idx_tool_id (tool_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
