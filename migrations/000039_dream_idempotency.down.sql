ALTER TABLE memories
    DROP INDEX uq_memories_owner_source_key,
    DROP COLUMN source_key;

UPDATE mcp_servers SET transport = 'sse' WHERE transport = 'streamable_http';
ALTER TABLE mcp_servers ALTER COLUMN transport SET DEFAULT 'sse';

ALTER TABLE memory_extraction_jobs
    DROP INDEX idx_memory_extraction_due,
    DROP INDEX uq_memory_extraction_idempotency,
    DROP COLUMN updated_at,
    DROP COLUMN lease_expires_at,
    DROP COLUMN locked_at,
    DROP COLUMN locked_by,
    DROP COLUMN attempt_count,
    DROP COLUMN due_at,
    DROP COLUMN through_message_id,
    DROP COLUMN trigger_reason,
    DROP COLUMN idempotency_key;
