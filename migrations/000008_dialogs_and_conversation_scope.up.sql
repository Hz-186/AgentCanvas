CREATE TABLE IF NOT EXISTS dialogs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    name VARCHAR(128) NOT NULL,
    description TEXT,
    provider_id BIGINT NOT NULL DEFAULT 0,
    model VARCHAR(128) NOT NULL DEFAULT '',
    system_prompt TEXT,
    prologue TEXT,
    kb_ids JSON,
    top_k INT NOT NULL DEFAULT 8,
    retrieval_mode VARCHAR(32) NOT NULL DEFAULT 'keyword',
    history_round_limit INT NOT NULL DEFAULT 8,
    status TINYINT NOT NULL DEFAULT 1,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    deleted_at DATETIME NULL,
    INDEX idx_owner_id (owner_id),
    INDEX idx_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

ALTER TABLE conversations
    ADD COLUMN dialog_id BIGINT NULL AFTER owner_id,
    ADD INDEX idx_owner_dialog (owner_id, dialog_id);
