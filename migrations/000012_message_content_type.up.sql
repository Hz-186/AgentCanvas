ALTER TABLE messages ADD COLUMN content_type varchar(32) NOT NULL DEFAULT 'text';
ALTER TABLE messages ADD COLUMN metadata_json json DEFAULT NULL;
