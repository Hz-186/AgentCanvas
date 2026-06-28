CREATE TABLE IF NOT EXISTS tool_policies (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    name VARCHAR(255) NOT NULL,
    require_approval_for_risk JSON NULL,
    max_timeout_ms INT NOT NULL DEFAULT 30000,
    max_output_bytes INT NOT NULL DEFAULT 65536,
    allowed_hosts JSON NULL,
    credential_scope VARCHAR(255) NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_owner_policy_name (owner_id, name),
    INDEX idx_owner_id (owner_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
