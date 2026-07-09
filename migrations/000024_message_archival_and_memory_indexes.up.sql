ALTER TABLE messages ADD COLUMN archived_at TIMESTAMP NULL DEFAULT NULL;
CREATE INDEX idx_messages_archived ON messages (conversation_id, archived_at);
CREATE INDEX idx_memories_owner_type_importance ON memories (owner_id, memory_type, importance DESC);
