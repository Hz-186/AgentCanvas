ALTER TABLE conversations
    DROP INDEX idx_owner_dialog,
    DROP COLUMN dialog_id;

DROP TABLE IF EXISTS dialogs;
