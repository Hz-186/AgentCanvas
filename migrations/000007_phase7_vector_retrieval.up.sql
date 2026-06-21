ALTER TABLE knowledge_bases
    ADD COLUMN embedding_provider_id BIGINT NULL AFTER retrieval_mode,
    ADD COLUMN embedding_model VARCHAR(128) NOT NULL DEFAULT '' AFTER embedding_provider_id,
    ADD COLUMN embedding_dimensions INT NOT NULL DEFAULT 0 AFTER embedding_model,
    ADD COLUMN hybrid_weight DOUBLE NOT NULL DEFAULT 0.5 AFTER embedding_dimensions,
    ADD COLUMN rerank_enabled BOOLEAN NOT NULL DEFAULT FALSE AFTER hybrid_weight,
    ADD COLUMN rerank_provider_id BIGINT NULL AFTER rerank_enabled,
    ADD COLUMN rerank_model VARCHAR(128) NOT NULL DEFAULT '' AFTER rerank_provider_id,
    ADD INDEX idx_embedding_provider_id (embedding_provider_id),
    ADD INDEX idx_rerank_provider_id (rerank_provider_id);
