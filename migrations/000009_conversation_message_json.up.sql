ALTER TABLE conversations
    ADD COLUMN message_json JSON AFTER workflow_id,
    ADD COLUMN reference_json JSON AFTER message_json;
