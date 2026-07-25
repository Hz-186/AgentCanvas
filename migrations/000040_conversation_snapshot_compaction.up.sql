ALTER TABLE conversation_compactions
    ADD COLUMN parent_snapshot_id BIGINT NULL AFTER last_message_id,
    ADD COLUMN snapshot_version INT NOT NULL DEFAULT 0 AFTER parent_snapshot_id,
    ADD COLUMN prompt_hash CHAR(64) NOT NULL DEFAULT '' AFTER prompt_version,
    ADD COLUMN summary_token_limit INT NOT NULL DEFAULT 0 AFTER after_tokens,
    ADD COLUMN summary_tokens INT NOT NULL DEFAULT 0 AFTER summary_token_limit,
    ADD COLUMN completed_at DATETIME NULL AFTER error_message,
    ADD INDEX idx_conversation_snapshot_current (owner_id, conversation_id, status, snapshot_version);

CREATE TABLE IF NOT EXISTS conversation_snapshot_claims (
    owner_id BIGINT NOT NULL,
    conversation_id BIGINT NOT NULL,
    parent_snapshot_id BIGINT NULL,
    parent_version INT NOT NULL DEFAULT 0,
    claim_token VARCHAR(64) NOT NULL,
    expires_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (owner_id, conversation_id),
    UNIQUE KEY uk_conversation_snapshot_claim_token (claim_token),
    INDEX idx_conversation_snapshot_claim_expiry (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
