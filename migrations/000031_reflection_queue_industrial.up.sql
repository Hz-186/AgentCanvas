ALTER TABLE agent_reflection_jobs
    ADD COLUMN lock_token CHAR(32) NOT NULL DEFAULT '' AFTER locked_at,
    ADD COLUMN lease_expires_at DATETIME NULL AFTER lock_token,
    ADD COLUMN last_heartbeat_at DATETIME NULL AFTER lease_expires_at,
    ADD COLUMN dispatch_seq INT NOT NULL DEFAULT 0 AFTER last_heartbeat_at,
    ADD COLUMN failure_type VARCHAR(32) NOT NULL DEFAULT '' AFTER error_message,
    ADD INDEX idx_reflection_job_lease (status, lease_expires_at, retry_at, id);

CREATE TABLE IF NOT EXISTS agent_reflection_job_outbox (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    event_id VARCHAR(191) NOT NULL,
    job_id BIGINT NOT NULL,
    dispatch_seq INT NOT NULL,
    event_type ENUM('job','dlq') NOT NULL DEFAULT 'job',
    available_at DATETIME NOT NULL,
    status ENUM('pending','publishing','published') NOT NULL DEFAULT 'pending',
    attempt_count INT NOT NULL DEFAULT 0,
    locked_by VARCHAR(128) NOT NULL DEFAULT '',
    locked_at DATETIME NULL,
    published_at DATETIME NULL,
    last_error TEXT NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_reflection_outbox_event (event_id),
    UNIQUE KEY uk_reflection_outbox_dispatch (job_id, dispatch_seq, event_type),
    INDEX idx_reflection_outbox_pending (status, available_at, id),
    INDEX idx_reflection_outbox_published (published_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS agent_reflection_evidence (
    id BIGINT PRIMARY KEY AUTO_INCREMENT,
    reflection_id BIGINT NOT NULL,
    job_id BIGINT NULL,
    run_id BIGINT NOT NULL DEFAULT 0,
    candidate_hash CHAR(64) NOT NULL,
    evidence_json JSON NULL,
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uk_reflection_evidence_job_candidate (job_id, candidate_hash),
    INDEX idx_reflection_evidence_reflection (reflection_id, created_at),
    INDEX idx_reflection_evidence_run (run_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

INSERT INTO agent_reflection_evidence (
    reflection_id, job_id, run_id, candidate_hash, evidence_json, created_at, updated_at
)
SELECT id, NULL, source_run_id, content_hash, evidence_json, created_at, updated_at
FROM agent_reflections
WHERE evidence_json IS NOT NULL;
