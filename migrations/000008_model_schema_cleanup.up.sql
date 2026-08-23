-- Rebuildable model cleanup wired through Go/GORM and runtime code.
ALTER TABLE `agent_run_checkpoints`
  RENAME COLUMN `runtime_checkpoint_json` TO `checkpoint_json`,
  DROP COLUMN `status`, DROP COLUMN `snapshot_version`, DROP COLUMN `interaction_id`,
  DROP COLUMN `messages_json`, DROP COLUMN `messages_summary`, DROP COLUMN `steps_json`,
  DROP COLUMN `pending_tool_call_json`, DROP COLUMN `context_json`,
  DROP COLUMN `tool_registry_hash`, DROP COLUMN `tool_policy_hash`, DROP COLUMN `updated_at`;

ALTER TABLE `memories`
  RENAME COLUMN `parent_id` TO `conflict_with_id`, RENAME COLUMN `conflict_flag` TO `has_conflict`,
  RENAME COLUMN `conversation_id` TO `source_conversation_id`, RENAME COLUMN `project_id` TO `source_project_id`,
  RENAME COLUMN `memory_level` TO `retention_tier`, RENAME COLUMN `access_count` TO `recall_count`,
  RENAME COLUMN `consolidation_count` TO `promotion_count`, RENAME COLUMN `source_key` TO `deduplication_key`,
  RENAME COLUMN `last_used_at` TO `last_recalled_at`, DROP COLUMN `session_id`, DROP COLUMN `embedding`;

UPDATE `memories`
  SET `memory_type` = CASE `memory_type`
    WHEN 'profile_memory' THEN 'profile'
    WHEN 'episodic_memory' THEN 'episodic'
    WHEN 'task_memory' THEN 'task'
    WHEN 'summary_memory' THEN 'archival'
    ELSE `memory_type`
  END,
  `retention_tier` = CASE `retention_tier`
    WHEN 'working' THEN 'short_term'
    ELSE `retention_tier`
  END;

ALTER TABLE `memories`
  MODIFY COLUMN `memory_type` enum('profile','episodic','task','archival') NOT NULL DEFAULT 'archival',
  MODIFY COLUMN `retention_tier` enum('short_term','long_term') NOT NULL DEFAULT 'long_term',
  RENAME INDEX `idx_memory_level` TO `idx_memories_retention_tier`,
  RENAME INDEX `uq_memories_owner_source_key` TO `uq_memories_owner_deduplication_key`,
  RENAME INDEX `idx_conversation_id` TO `idx_memories_source_conversation`;

ALTER TABLE `memories`
  RENAME INDEX `idx_memories_project` TO `idx_memories_source_project`;

ALTER TABLE `memory_write_logs` DROP COLUMN `source_message_id`;

ALTER TABLE `mcp_servers`
  RENAME COLUMN `last_error` TO `discovery_error`,
  RENAME COLUMN `discovered_at` TO `tools_discovered_at`;
ALTER TABLE `mcp_tool_cache`
  RENAME COLUMN `server_id` TO `mcp_server_id`,
  RENAME COLUMN `parameters_json` TO `input_schema_json`,
  DROP COLUMN `schema_hash`, DROP COLUMN `cached_at`, DROP COLUMN `updated_at`;
ALTER TABLE `mcp_tool_cache`
  RENAME INDEX `uniq_mcp_tool_cache_server_name` TO `uk_mcp_tool_cache_server_tool`,
  RENAME INDEX `idx_mcp_tool_cache_server` TO `idx_mcp_tool_cache_server_id`;

DROP TABLE `model_usage_logs`, `message_references`, `memory_merge_logs`, `tool_policies`;

ALTER TABLE `conversations`
  DROP COLUMN `source`, DROP COLUMN `message_json`, DROP COLUMN `reference_json`;
ALTER TABLE `messages`
  DROP COLUMN `content_type`, DROP COLUMN `metadata_json`;

ALTER TABLE `knowledge_bases`
  RENAME COLUMN `hybrid_weight` TO `vector_weight`,
  RENAME COLUMN `status` TO `enabled`,
  RENAME INDEX `idx_status` TO `idx_enabled`;
ALTER TABLE `documents`
  RENAME COLUMN `kb_id` TO `knowledge_base_id`,
  RENAME COLUMN `file_size` TO `file_size_bytes`,
  RENAME COLUMN `object_key` TO `storage_object_key`,
  RENAME COLUMN `active_generation` TO `active_generation_id`,
  RENAME COLUMN `parser_status` TO `ingestion_status`,
  RENAME COLUMN `parser_error` TO `ingestion_error`;
ALTER TABLE `documents`
  RENAME INDEX `idx_owner_kb` TO `idx_documents_owner_knowledge_base`,
  RENAME INDEX `idx_parser_status` TO `idx_documents_ingestion_status`,
  RENAME INDEX `idx_kb_enabled` TO `idx_documents_knowledge_base_enabled`;
ALTER TABLE `document_chunks`
  RENAME COLUMN `kb_id` TO `knowledge_base_id`,
  RENAME COLUMN `generation` TO `generation_id`,
  RENAME COLUMN `page_no` TO `page_number`,
  DROP COLUMN `es_index`, DROP COLUMN `es_doc_id`, DROP COLUMN `updated_at`;
ALTER TABLE `document_chunks`
  RENAME INDEX `idx_owner_kb` TO `idx_document_chunks_owner_knowledge_base`;
ALTER TABLE `ingestion_jobs`
  RENAME COLUMN `kb_id` TO `knowledge_base_id`;
ALTER TABLE `retrieval_logs`
  RENAME COLUMN `kb_ids` TO `knowledge_base_ids`;

ALTER TABLE `model_providers`
  RENAME COLUMN `status` TO `enabled`;

ALTER TABLE `agent_releases`
  RENAME COLUMN `version_no` TO `version_number`,
  DROP COLUMN `tool_schema_hash`, DROP COLUMN `resource_versions_json`, DROP COLUMN `created_by`;

ALTER TABLE `oauth_accounts`
  DROP COLUMN `provider_username`, DROP COLUMN `provider_email`, DROP COLUMN `avatar_url`,
  DROP COLUMN `access_token_encrypted`, DROP COLUMN `refresh_token_encrypted`, DROP COLUMN `scopes`,
  DROP COLUMN `token_expires_at`, DROP COLUMN `updated_at`;
ALTER TABLE `auth_sessions`
  DROP COLUMN `user_agent`, DROP COLUMN `ip_address`;
ALTER TABLE `api_tokens`
  DROP COLUMN `last_used_at`;

ALTER TABLE `skills`
  DROP INDEX `uk_skills_owner_name_version`,
  RENAME COLUMN `content_md` TO `content_markdown`,
  RENAME COLUMN `status` TO `enabled`,
  DROP COLUMN `version`,
  ADD COLUMN `active_name` varchar(128) GENERATED ALWAYS AS (IF(`deleted_at` IS NULL, `name`, NULL)) STORED,
  ADD UNIQUE KEY `uk_skills_owner_active_name` (`owner_id`, `active_name`);
ALTER TABLE `skills`
  RENAME INDEX `idx_skills_owner_status_updated` TO `idx_skills_owner_enabled_updated`;

ALTER TABLE `projects`
  DROP INDEX `uk_projects_owner_path`,
  DROP COLUMN `primary_path_hash`, DROP COLUMN `icon`, DROP COLUMN `color`,
  RENAME COLUMN `primary_path` TO `repository_root`;
ALTER TABLE `projects`
  ADD COLUMN `repository_root_hash` binary(32) GENERATED ALWAYS AS (UNHEX(SHA2(`repository_root`, 256))) STORED,
  ADD UNIQUE KEY `uk_projects_owner_repository_root` (`owner_id`, `repository_root_hash`);
ALTER TABLE `project_folders`
  RENAME COLUMN `is_primary` TO `is_repository_root`;
ALTER TABLE `agent_workspaces`
  RENAME COLUMN `unpushed` TO `has_unpushed_commits`;
ALTER TABLE `tool_pack_items`
  RENAME COLUMN `pack_id` TO `tool_pack_id`;
ALTER TABLE `tool_pack_items`
  RENAME INDEX `uk_pack_tool` TO `uk_tool_pack_item`,
  RENAME INDEX `idx_owner_pack` TO `idx_tool_pack_items_owner_pack`;

ALTER TABLE `tool_definitions`
  RENAME COLUMN `status` TO `enabled`;

ALTER TABLE `mcp_servers`
  RENAME COLUMN `status` TO `enabled`,
  RENAME INDEX `idx_mcp_servers_status` TO `idx_mcp_servers_enabled`;

ALTER TABLE `cache_invalidation_outbox`
  RENAME COLUMN `attempts` TO `attempt_count`;
