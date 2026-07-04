ALTER TABLE workflow_profiles
    ADD COLUMN default_tool_pack_ids JSON NULL AFTER allow_code_execution,
    ADD COLUMN default_mcp_server_ids JSON NULL AFTER default_tool_ids,
    ADD COLUMN output_schema_json JSON NULL AFTER default_max_workflow_call_depth,
    ADD COLUMN tool_policy_json JSON NULL AFTER output_schema_json,
    ADD COLUMN memory_policy_json JSON NULL AFTER tool_policy_json,
    ADD COLUMN context_policy_json JSON NULL AFTER memory_policy_json,
    ADD COLUMN risk_level VARCHAR(32) NOT NULL DEFAULT 'medium' AFTER context_policy_json,
    ADD COLUMN mode VARCHAR(32) NOT NULL DEFAULT 'react' AFTER risk_level;
