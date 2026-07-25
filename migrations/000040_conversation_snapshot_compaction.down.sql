DROP TABLE IF EXISTS conversation_snapshot_claims;

ALTER TABLE conversation_compactions
    DROP INDEX idx_conversation_snapshot_current,
    DROP COLUMN completed_at,
    DROP COLUMN summary_tokens,
    DROP COLUMN summary_token_limit,
    DROP COLUMN prompt_hash,
    DROP COLUMN snapshot_version,
    DROP COLUMN parent_snapshot_id;
