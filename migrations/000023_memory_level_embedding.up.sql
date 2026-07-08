ALTER TABLE memories
    ADD COLUMN memory_level ENUM('working','short_term','long_term') NOT NULL DEFAULT 'long_term' AFTER memory_type,
    ADD COLUMN session_id VARCHAR(64) NULL AFTER conversation_id,
    ADD COLUMN embedding BLOB NULL AFTER metadata_json,
    ADD COLUMN access_count INT NOT NULL DEFAULT 0 AFTER importance,
    ADD COLUMN consolidation_count INT NOT NULL DEFAULT 0 AFTER access_count,
    ADD COLUMN parent_id BIGINT NULL AFTER id,
    ADD COLUMN conflict_flag TINYINT(1) NOT NULL DEFAULT 0 AFTER parent_id;

ALTER TABLE memories ADD INDEX idx_memory_level (owner_id, memory_level);

CREATE TABLE IF NOT EXISTS memory_merge_logs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    source_id BIGINT NOT NULL,
    target_id BIGINT NOT NULL,
    similarity DOUBLE NOT NULL DEFAULT 0,
    reason TEXT,
    created_at DATETIME NOT NULL,
    INDEX idx_owner_id (owner_id),
    INDEX idx_source_id (source_id),
    INDEX idx_target_id (target_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS memory_extraction_jobs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    conversation_id BIGINT NOT NULL,
    source_message_ids JSON,
    status ENUM('pending','running','completed','failed') NOT NULL DEFAULT 'pending',
    result_json JSON,
    error_message TEXT,
    created_at DATETIME NOT NULL,
    completed_at DATETIME,
    INDEX idx_owner_id (owner_id),
    INDEX idx_conversation_id (conversation_id),
    INDEX idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
