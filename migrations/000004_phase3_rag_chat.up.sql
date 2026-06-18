CREATE TABLE IF NOT EXISTS conversations (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    title VARCHAR(255),
    source VARCHAR(32) NOT NULL DEFAULT 'rag_chat',
    agent_id BIGINT NULL,
    last_message_at DATETIME NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL,
    INDEX idx_owner_id (owner_id),
    INDEX idx_agent_id (agent_id),
    INDEX idx_last_message_at (last_message_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS messages (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    conversation_id BIGINT NOT NULL,
    role VARCHAR(32) NOT NULL,
    content MEDIUMTEXT NOT NULL,
    content_type VARCHAR(32) NOT NULL DEFAULT 'text',
    run_id BIGINT NULL,
    token_count INT NOT NULL DEFAULT 0,
    metadata_json JSON,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_conversation_id (conversation_id),
    INDEX idx_owner_id (owner_id),
    INDEX idx_run_id (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS message_references (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    message_id BIGINT NOT NULL,
    kb_id BIGINT NOT NULL,
    document_id BIGINT NOT NULL,
    chunk_id BIGINT NOT NULL,
    ref_index INT NOT NULL,
    score DOUBLE,
    quote_text TEXT,
    page_no INT NULL,
    metadata_json JSON,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_message_id (message_id),
    INDEX idx_chunk_id (chunk_id),
    INDEX idx_owner_id (owner_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS model_usage_logs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    provider_id BIGINT,
    provider_type VARCHAR(64),
    model_name VARCHAR(128),
    usage_type VARCHAR(32) NOT NULL,
    prompt_tokens INT NOT NULL DEFAULT 0,
    completion_tokens INT NOT NULL DEFAULT 0,
    total_tokens INT NOT NULL DEFAULT 0,
    estimated_cost DECIMAL(12, 6) DEFAULT 0,
    latency_ms INT NOT NULL DEFAULT 0,
    success TINYINT NOT NULL DEFAULT 1,
    error_message TEXT,
    request_id VARCHAR(128),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_owner_id (owner_id),
    INDEX idx_provider_id (provider_id),
    INDEX idx_created_at (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
