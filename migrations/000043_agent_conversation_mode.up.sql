ALTER TABLE conversations
    ADD COLUMN agent_mode VARCHAR(32) NOT NULL DEFAULT 'react' AFTER agent_release_id;
