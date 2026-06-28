ALTER TABLE workflow_profiles
    ADD COLUMN default_tool_pack_ids JSON NULL AFTER allow_code_execution,
    ADD COLUMN default_mcp_server_ids JSON NULL AFTER default_tool_ids,
    ADD COLUMN output_schema_json JSON NULL AFTER default_max_workflow_call_depth;
