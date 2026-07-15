DROP TABLE IF EXISTS agent_reflection_evidence;
DROP TABLE IF EXISTS agent_reflection_job_outbox;

ALTER TABLE agent_reflection_jobs
    DROP INDEX idx_reflection_job_lease,
    DROP COLUMN failure_type,
    DROP COLUMN dispatch_seq,
    DROP COLUMN last_heartbeat_at,
    DROP COLUMN lease_expires_at,
    DROP COLUMN lock_token;
