ALTER TABLE agent_reflection_jobs
    DROP INDEX idx_reflection_jobs_agent,
    DROP COLUMN agent_id,
    MODIFY COLUMN workflow_id BIGINT NOT NULL;

ALTER TABLE agent_reflections
    DROP INDEX idx_reflections_agent_scope,
	DROP INDEX uk_owner_agent_content,
    DROP COLUMN agent_id,
	MODIFY COLUMN scope ENUM('node','workflow','global') NOT NULL DEFAULT 'workflow',
    MODIFY COLUMN workflow_id BIGINT NOT NULL;

ALTER TABLE workflow_runs
    DROP INDEX idx_agent_release_id,
    DROP INDEX idx_agent_id,
    DROP COLUMN agent_release_id,
    DROP COLUMN agent_id,
    MODIFY COLUMN workflow_id BIGINT NOT NULL,
    MODIFY COLUMN flow_version_id BIGINT NOT NULL;

ALTER TABLE conversations
    DROP INDEX idx_parent_conversation_id,
    DROP INDEX idx_agent_release_id,
    DROP INDEX idx_owner_agent,
    DROP COLUMN parent_conversation_id,
    DROP COLUMN agent_release_id,
    DROP COLUMN agent_id;

DROP TABLE IF EXISTS agent_turns;
DROP TABLE IF EXISTS agent_releases;
DROP TABLE IF EXISTS agents;
