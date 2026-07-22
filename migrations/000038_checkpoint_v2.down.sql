ALTER TABLE workflow_checkpoints
    DROP COLUMN runtime_checkpoint_json,
    DROP COLUMN snapshot_version;
