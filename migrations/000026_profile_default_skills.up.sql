ALTER TABLE workflow_profiles
    ADD COLUMN default_skill_ids JSON NULL AFTER default_tool_ids;
