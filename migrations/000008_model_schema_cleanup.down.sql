-- Restores old names only; dropped data is not reconstructed.
ALTER TABLE `agent_run_checkpoints`
  RENAME COLUMN `checkpoint_json` TO `runtime_checkpoint_json`,
  ADD COLUMN `status` varchar(32) NOT NULL DEFAULT 'active', ADD COLUMN `snapshot_version` int NOT NULL DEFAULT 1,
  ADD COLUMN `interaction_id` varchar(191) NOT NULL DEFAULT '', ADD COLUMN `messages_json` json DEFAULT NULL,
  ADD COLUMN `messages_summary` mediumtext, ADD COLUMN `steps_json` json DEFAULT NULL,
  ADD COLUMN `pending_tool_call_json` json DEFAULT NULL, ADD COLUMN `context_json` json DEFAULT NULL,
  ADD COLUMN `tool_registry_hash` char(64) NOT NULL DEFAULT '', ADD COLUMN `tool_policy_hash` char(64) NOT NULL DEFAULT '',
  ADD COLUMN `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP;

ALTER TABLE `memories`
  RENAME COLUMN `conflict_with_id` TO `parent_id`, RENAME COLUMN `has_conflict` TO `conflict_flag`,
  RENAME COLUMN `source_conversation_id` TO `conversation_id`, RENAME COLUMN `source_project_id` TO `project_id`,
  RENAME COLUMN `retention_tier` TO `memory_level`, RENAME COLUMN `recall_count` TO `access_count`,
  RENAME COLUMN `promotion_count` TO `consolidation_count`, RENAME COLUMN `deduplication_key` TO `source_key`,
  RENAME COLUMN `last_recalled_at` TO `last_used_at`, ADD COLUMN `session_id` varchar(64) DEFAULT NULL,
  ADD COLUMN `embedding` blob;

ALTER TABLE `memories`
  MODIFY COLUMN `memory_type` varchar(64) NOT NULL,
  MODIFY COLUMN `memory_level` enum('working','short_term','long_term') NOT NULL DEFAULT 'long_term',
  RENAME INDEX `idx_memories_retention_tier` TO `idx_memory_level`,
  RENAME INDEX `uq_memories_owner_deduplication_key` TO `uq_memories_owner_source_key`,
  RENAME INDEX `idx_memories_source_conversation` TO `idx_conversation_id`;

ALTER TABLE `memories`
  RENAME INDEX `idx_memories_source_project` TO `idx_memories_project`;

UPDATE `memories`
  SET `memory_type` = CASE `memory_type`
    WHEN 'profile' THEN 'profile_memory'
    WHEN 'episodic' THEN 'episodic_memory'
    WHEN 'task' THEN 'task_memory'
    WHEN 'archival' THEN 'summary_memory'
    ELSE `memory_type`
  END;

ALTER TABLE `memory_write_logs` ADD COLUMN `source_message_id` bigint DEFAULT NULL;

ALTER TABLE `mcp_servers`
  RENAME COLUMN `discovery_error` TO `last_error`,
  RENAME COLUMN `tools_discovered_at` TO `discovered_at`;
ALTER TABLE `mcp_tool_cache`
  RENAME COLUMN `mcp_server_id` TO `server_id`,
  RENAME COLUMN `input_schema_json` TO `parameters_json`,
  ADD COLUMN `schema_hash` varchar(128) DEFAULT NULL, ADD COLUMN `cached_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  ADD COLUMN `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP;
ALTER TABLE `mcp_tool_cache`
  MODIFY COLUMN `cached_at` datetime NOT NULL,
  MODIFY COLUMN `updated_at` datetime NOT NULL;
ALTER TABLE `mcp_tool_cache`
  RENAME INDEX `uk_mcp_tool_cache_server_tool` TO `uniq_mcp_tool_cache_server_name`,
  RENAME INDEX `idx_mcp_tool_cache_server_id` TO `idx_mcp_tool_cache_server`;

ALTER TABLE `tool_pack_items` RENAME COLUMN `tool_pack_id` TO `pack_id`;
ALTER TABLE `tool_pack_items`
  RENAME INDEX `uk_tool_pack_item` TO `uk_pack_tool`,
  RENAME INDEX `idx_tool_pack_items_owner_pack` TO `idx_owner_pack`;
ALTER TABLE `agent_workspaces` RENAME COLUMN `has_unpushed_commits` TO `unpushed`;
ALTER TABLE `project_folders` RENAME COLUMN `is_repository_root` TO `is_primary`;
ALTER TABLE `projects`
  DROP INDEX `uk_projects_owner_repository_root`,
  DROP COLUMN `repository_root_hash`,
  ADD COLUMN `icon` varchar(255) NOT NULL DEFAULT '', ADD COLUMN `color` varchar(64) NOT NULL DEFAULT '',
  RENAME COLUMN `repository_root` TO `primary_path`;
ALTER TABLE `projects`
  ADD COLUMN `primary_path_hash` binary(32) GENERATED ALWAYS AS (UNHEX(SHA2(`primary_path`, 256))) STORED,
  ADD UNIQUE KEY `uk_projects_owner_path` (`owner_id`, `primary_path_hash`);

ALTER TABLE `skills`
  DROP INDEX `uk_skills_owner_active_name`, DROP COLUMN `active_name`,
  ADD COLUMN `version` int NOT NULL DEFAULT 1,
  RENAME COLUMN `content_markdown` TO `content_md`,
  ADD UNIQUE KEY `uk_skills_owner_name_version` (`owner_id`, `name`, `version`);
ALTER TABLE `skills`
  RENAME INDEX `idx_skills_owner_enabled_updated` TO `idx_skills_owner_status_updated`;

ALTER TABLE `api_tokens` ADD COLUMN `last_used_at` datetime DEFAULT NULL;
ALTER TABLE `auth_sessions`
  ADD COLUMN `user_agent` varchar(512) DEFAULT NULL, ADD COLUMN `ip_address` varchar(64) DEFAULT NULL;
ALTER TABLE `oauth_accounts`
  ADD COLUMN `provider_username` varchar(128) DEFAULT NULL, ADD COLUMN `provider_email` varchar(128) DEFAULT NULL,
  ADD COLUMN `avatar_url` varchar(512) DEFAULT NULL, ADD COLUMN `access_token_encrypted` text,
  ADD COLUMN `refresh_token_encrypted` text, ADD COLUMN `scopes` varchar(512) DEFAULT NULL,
  ADD COLUMN `token_expires_at` datetime DEFAULT NULL,
  ADD COLUMN `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP;

ALTER TABLE `agent_releases`
  RENAME COLUMN `version_number` TO `version_no`,
	ADD COLUMN `tool_schema_hash` varchar(64) NOT NULL DEFAULT '',
	ADD COLUMN `resource_versions_json` json NULL,
	ADD COLUMN `created_by` bigint NOT NULL DEFAULT 0;
UPDATE `agent_releases` SET `resource_versions_json` = JSON_OBJECT() WHERE `resource_versions_json` IS NULL;
ALTER TABLE `agent_releases`
  MODIFY COLUMN `resource_versions_json` json NOT NULL,
  MODIFY COLUMN `created_by` bigint NOT NULL;

ALTER TABLE `retrieval_logs` RENAME COLUMN `knowledge_base_ids` TO `kb_ids`;
ALTER TABLE `ingestion_jobs` RENAME COLUMN `knowledge_base_id` TO `kb_id`;
ALTER TABLE `document_chunks`
  RENAME COLUMN `knowledge_base_id` TO `kb_id`,
  RENAME COLUMN `generation_id` TO `generation`,
  RENAME COLUMN `page_number` TO `page_no`,
  ADD COLUMN `es_index` varchar(128) DEFAULT NULL, ADD COLUMN `es_doc_id` varchar(128) DEFAULT NULL,
  ADD COLUMN `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP;
ALTER TABLE `document_chunks`
  RENAME INDEX `idx_document_chunks_owner_knowledge_base` TO `idx_owner_kb`;
ALTER TABLE `documents`
  RENAME COLUMN `knowledge_base_id` TO `kb_id`,
  RENAME COLUMN `file_size_bytes` TO `file_size`,
  RENAME COLUMN `storage_object_key` TO `object_key`,
  RENAME COLUMN `active_generation_id` TO `active_generation`,
  RENAME COLUMN `ingestion_status` TO `parser_status`,
  RENAME COLUMN `ingestion_error` TO `parser_error`;
ALTER TABLE `documents`
  RENAME INDEX `idx_documents_owner_knowledge_base` TO `idx_owner_kb`,
  RENAME INDEX `idx_documents_ingestion_status` TO `idx_parser_status`,
  RENAME INDEX `idx_documents_knowledge_base_enabled` TO `idx_kb_enabled`;
ALTER TABLE `knowledge_bases` RENAME COLUMN `vector_weight` TO `hybrid_weight`;

ALTER TABLE `knowledge_bases`
  RENAME COLUMN `enabled` TO `status`,
  RENAME INDEX `idx_enabled` TO `idx_status`;

ALTER TABLE `model_providers`
  RENAME COLUMN `enabled` TO `status`;

ALTER TABLE `skills`
  RENAME COLUMN `enabled` TO `status`;

ALTER TABLE `tool_definitions`
  RENAME COLUMN `enabled` TO `status`;

ALTER TABLE `mcp_servers`
  RENAME COLUMN `enabled` TO `status`,
  RENAME INDEX `idx_mcp_servers_enabled` TO `idx_mcp_servers_status`;

ALTER TABLE `cache_invalidation_outbox`
  RENAME COLUMN `attempt_count` TO `attempts`;

ALTER TABLE `messages`
  ADD COLUMN `content_type` varchar(32) NOT NULL DEFAULT 'text', ADD COLUMN `metadata_json` json DEFAULT NULL;
ALTER TABLE `conversations`
  ADD COLUMN `source` varchar(32) NOT NULL DEFAULT 'agent', ADD COLUMN `message_json` json DEFAULT NULL,
  ADD COLUMN `reference_json` json DEFAULT NULL;

CREATE TABLE `model_usage_logs` (
  `id` bigint NOT NULL AUTO_INCREMENT, `owner_id` bigint NOT NULL, `provider_id` bigint DEFAULT NULL,
  `provider_type` varchar(64) DEFAULT NULL, `model_name` varchar(128) DEFAULT NULL, `usage_type` varchar(32) NOT NULL,
  `prompt_tokens` int NOT NULL DEFAULT 0, `completion_tokens` int NOT NULL DEFAULT 0, `total_tokens` int NOT NULL DEFAULT 0,
  `estimated_cost` decimal(12,6) DEFAULT 0, `latency_ms` int NOT NULL DEFAULT 0, `success` tinyint NOT NULL DEFAULT 1,
  `error_message` text, `request_id` varchar(128) DEFAULT NULL, `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`), KEY `idx_owner_id` (`owner_id`), KEY `idx_provider_id` (`provider_id`), KEY `idx_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE `message_references` (
  `id` bigint NOT NULL AUTO_INCREMENT, `owner_id` bigint NOT NULL, `message_id` bigint NOT NULL, `kb_id` bigint NOT NULL,
  `document_id` bigint NOT NULL, `chunk_id` bigint NOT NULL, `ref_index` int NOT NULL, `score` double DEFAULT NULL,
  `quote_text` text, `page_no` int DEFAULT NULL, `metadata_json` json DEFAULT NULL, `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`), KEY `idx_message_id` (`message_id`), KEY `idx_chunk_id` (`chunk_id`), KEY `idx_owner_id` (`owner_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE `memory_merge_logs` (
  `id` bigint NOT NULL AUTO_INCREMENT, `owner_id` bigint NOT NULL, `source_id` bigint NOT NULL, `target_id` bigint NOT NULL,
  `similarity` double NOT NULL DEFAULT 0, `reason` text, `created_at` datetime NOT NULL,
  PRIMARY KEY (`id`), KEY `idx_owner_id` (`owner_id`), KEY `idx_source_id` (`source_id`), KEY `idx_target_id` (`target_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
CREATE TABLE `tool_policies` (
  `id` bigint NOT NULL AUTO_INCREMENT, `owner_id` bigint NOT NULL, `name` varchar(255) NOT NULL,
  `require_approval_for_risk` json DEFAULT NULL, `max_timeout_ms` int NOT NULL DEFAULT 30000,
  `max_output_bytes` int NOT NULL DEFAULT 65536, `allowed_hosts` json DEFAULT NULL, `credential_scope` varchar(255) DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP, `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`), UNIQUE KEY `uk_owner_policy_name` (`owner_id`,`name`), KEY `idx_owner_id` (`owner_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
