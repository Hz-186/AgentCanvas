DROP INDEX idx_memories_owner_type_importance ON memories;
DROP INDEX idx_messages_archived ON messages;
ALTER TABLE messages DROP COLUMN archived_at;
