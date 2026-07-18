DROP TABLE IF EXISTS conversation_compactions;
DROP TABLE IF EXISTS context_resource_index_outbox;

ALTER TABLE agent_reflections
    DROP INDEX idx_reflection_embedding_profile,
    DROP COLUMN embedding_dimensions,
    DROP COLUMN embedding_model,
    DROP COLUMN embedding_provider_id;
