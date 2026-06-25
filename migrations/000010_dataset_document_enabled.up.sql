ALTER TABLE documents
    ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT TRUE AFTER parser_status,
    ADD INDEX idx_kb_enabled (kb_id, enabled);
