CREATE INDEX idx_skills_owner_deleted_updated_id ON skills (owner_id, deleted_at, updated_at DESC, id DESC);
CREATE INDEX idx_memories_owner_deleted_updated_id ON memories (owner_id, deleted_at, updated_at DESC, id DESC);
CREATE INDEX idx_tool_definitions_owner_deleted_updated_id ON tool_definitions (owner_id, deleted_at, updated_at DESC, id DESC);
CREATE INDEX idx_dialogs_owner_deleted_updated_id ON dialogs (owner_id, deleted_at, updated_at DESC, id DESC);
CREATE INDEX idx_workflows_owner_deleted_id ON workflows (owner_id, deleted_at, id DESC);
CREATE INDEX idx_knowledge_bases_owner_deleted_id ON knowledge_bases (owner_id, deleted_at, id DESC);
