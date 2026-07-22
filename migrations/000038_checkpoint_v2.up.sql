ALTER TABLE workflow_checkpoints
    ADD COLUMN snapshot_version INT NOT NULL DEFAULT 1 AFTER status,
    ADD COLUMN runtime_checkpoint_json JSON NULL AFTER snapshot_version;
