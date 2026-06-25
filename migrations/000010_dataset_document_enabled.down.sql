ALTER TABLE documents
    DROP INDEX idx_kb_enabled,
    DROP COLUMN enabled;
