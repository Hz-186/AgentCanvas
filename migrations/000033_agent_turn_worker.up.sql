ALTER TABLE agent_turns
    ADD COLUMN attempt_count INT NOT NULL DEFAULT 0 AFTER error_message,
    ADD COLUMN max_attempts INT NOT NULL DEFAULT 3 AFTER attempt_count,
    ADD COLUMN worker_id VARCHAR(128) NOT NULL DEFAULT '' AFTER max_attempts,
    ADD COLUMN lease_token VARCHAR(64) NOT NULL DEFAULT '' AFTER worker_id,
    ADD COLUMN lease_expires_at DATETIME NULL AFTER lease_token,
    ADD COLUMN last_heartbeat_at DATETIME NULL AFTER lease_expires_at,
    ADD COLUMN retry_at DATETIME NULL AFTER last_heartbeat_at,
    ADD INDEX idx_agent_turn_claim (status, retry_at, lease_expires_at, id),
    ADD INDEX idx_agent_turn_worker (worker_id, lease_expires_at);
