DROP TABLE IF EXISTS memory_recall_logs;

ALTER TABLE memories
    DROP INDEX idx_memories_decay,
    DROP INDEX idx_memories_supersedes,
    DROP INDEX idx_memories_scope_status,
    DROP COLUMN last_decay_at,
    DROP COLUMN supersedes_id,
    DROP COLUMN status,
    DROP COLUMN scope_id,
    DROP COLUMN scope_type;
