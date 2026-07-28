ALTER TABLE memories
    ADD COLUMN scope_type ENUM('user','agent','workflow','conversation') NOT NULL DEFAULT 'user' AFTER conversation_id,
    ADD COLUMN scope_id BIGINT NOT NULL DEFAULT 0 AFTER scope_type,
    ADD COLUMN status ENUM('active','superseded','revoked') NOT NULL DEFAULT 'active' AFTER scope_id,
    ADD COLUMN supersedes_id BIGINT NULL AFTER status,
    ADD COLUMN last_decay_at DATETIME NULL AFTER last_used_at,
    ADD INDEX idx_memories_scope_status (owner_id, scope_type, scope_id, status, memory_type),
    ADD INDEX idx_memories_supersedes (supersedes_id),
    ADD INDEX idx_memories_decay (owner_id, status, memory_level, last_decay_at);

UPDATE memories
SET scope_type = 'conversation', scope_id = conversation_id, status = 'active'
WHERE conversation_id IS NOT NULL AND conversation_id > 0;

UPDATE memories
SET scope_type = 'user', scope_id = owner_id, status = 'active'
WHERE conversation_id IS NULL OR conversation_id <= 0;

CREATE TABLE IF NOT EXISTS memory_recall_logs (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    owner_id BIGINT NOT NULL,
    agent_id BIGINT NOT NULL DEFAULT 0,
    workflow_id BIGINT NOT NULL DEFAULT 0,
    conversation_id BIGINT NOT NULL DEFAULT 0,
    run_id BIGINT NOT NULL DEFAULT 0,
    query TEXT NOT NULL,
    candidate_json JSON NOT NULL,
    injected_json JSON NOT NULL,
    token_cost INT NOT NULL DEFAULT 0,
    feedback VARCHAR(32) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_memory_recall_scope (owner_id, agent_id, workflow_id, conversation_id, id),
    INDEX idx_memory_recall_run (owner_id, run_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
