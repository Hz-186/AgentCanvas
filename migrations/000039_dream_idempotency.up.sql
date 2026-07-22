ALTER TABLE memory_extraction_jobs
    ADD COLUMN idempotency_key VARCHAR(191) NULL AFTER conversation_id,
    ADD COLUMN trigger_reason VARCHAR(32) NOT NULL DEFAULT '' AFTER idempotency_key,
    ADD COLUMN through_message_id BIGINT NOT NULL DEFAULT 0 AFTER source_message_ids,
    ADD COLUMN due_at DATETIME NULL AFTER status,
    ADD COLUMN attempt_count INT NOT NULL DEFAULT 0 AFTER due_at,
    ADD COLUMN locked_by VARCHAR(128) NOT NULL DEFAULT '' AFTER attempt_count,
    ADD COLUMN locked_at DATETIME NULL AFTER locked_by,
    ADD COLUMN lease_expires_at DATETIME NULL AFTER locked_at,
    ADD COLUMN updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP AFTER created_at,
    ADD UNIQUE INDEX uq_memory_extraction_idempotency (owner_id, idempotency_key),
    ADD INDEX idx_memory_extraction_due (status, due_at, lease_expires_at);

ALTER TABLE memories
    ADD COLUMN source_key VARCHAR(191) NULL AFTER source,
    ADD UNIQUE INDEX uq_memories_owner_source_key (owner_id, source_key);

UPDATE mcp_servers SET transport = 'streamable_http' WHERE transport = 'sse';
ALTER TABLE mcp_servers ALTER COLUMN transport SET DEFAULT 'streamable_http';
