DROP INDEX idx_approval_interaction ON approval_requests;
ALTER TABLE approval_requests DROP COLUMN interaction_id;
ALTER TABLE workflow_checkpoints DROP COLUMN interaction_id;
