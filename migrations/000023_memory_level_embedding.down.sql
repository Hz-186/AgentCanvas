ALTER TABLE memories DROP INDEX idx_memory_level;

ALTER TABLE memories
    DROP COLUMN memory_level,
    DROP COLUMN session_id,
    DROP COLUMN embedding,
    DROP COLUMN access_count,
    DROP COLUMN consolidation_count,
    DROP COLUMN parent_id,
    DROP COLUMN conflict_flag;

DROP TABLE IF EXISTS memory_merge_logs;
DROP TABLE IF EXISTS memory_extraction_jobs;
