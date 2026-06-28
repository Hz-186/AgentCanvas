ALTER TABLE agent_profiles
    DROP COLUMN default_max_agent_call_depth,
    DROP COLUMN default_call_agent_ids,
    DROP COLUMN default_knowledge_mode,
    DROP COLUMN default_knowledge_top_k,
    DROP COLUMN default_knowledge_ids,
    DROP COLUMN default_tool_ids;
