ALTER TABLE agent_profiles
    ADD COLUMN default_tool_ids JSON NULL AFTER allow_code_execution,
    ADD COLUMN default_knowledge_ids JSON NULL AFTER default_tool_ids,
    ADD COLUMN default_knowledge_top_k INT NOT NULL DEFAULT 5 AFTER default_knowledge_ids,
    ADD COLUMN default_knowledge_mode VARCHAR(32) NOT NULL DEFAULT 'hybrid' AFTER default_knowledge_top_k,
    ADD COLUMN default_call_agent_ids JSON NULL AFTER default_knowledge_mode,
    ADD COLUMN default_max_agent_call_depth INT NOT NULL DEFAULT 3 AFTER default_call_agent_ids;
