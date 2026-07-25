ALTER TABLE workflow_checkpoints
    ADD COLUMN interaction_id VARCHAR(128) NOT NULL DEFAULT '' AFTER snapshot_version;

ALTER TABLE approval_requests
    ADD COLUMN interaction_id VARCHAR(128) NOT NULL DEFAULT '' AFTER tool_call_id;

CREATE INDEX idx_approval_interaction ON approval_requests (owner_id, interaction_id);
