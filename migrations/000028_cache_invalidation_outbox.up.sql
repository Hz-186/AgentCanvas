CREATE TABLE cache_invalidation_outbox (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    resource_kind VARCHAR(64) NOT NULL,
    attempts INT NOT NULL DEFAULT 0,
    next_retry_at DATETIME NOT NULL,
    processed_at DATETIME NULL,
    last_error TEXT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    KEY idx_cache_outbox_pending (processed_at, next_retry_at, id),
    KEY idx_cache_outbox_owner_kind (owner_id, resource_kind)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
