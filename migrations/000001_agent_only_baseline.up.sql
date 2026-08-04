/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_change_proposals` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `agent_id` bigint NOT NULL,
  `review_id` bigint NOT NULL,
  `turn_id` bigint NOT NULL,
  `run_id` bigint NOT NULL,
  `kind` varchar(32) NOT NULL,
  `title` varchar(512) NOT NULL,
  `content` mediumtext NOT NULL,
  `payload_json` json NOT NULL,
  `evidence_json` json NOT NULL,
  `diff_json` json NOT NULL,
  `confidence` double NOT NULL DEFAULT '0',
  `checksum` char(64) NOT NULL,
  `security_status` varchar(32) NOT NULL DEFAULT 'passed',
  `security_reason` varchar(512) NOT NULL DEFAULT '',
  `status` varchar(32) NOT NULL DEFAULT 'pending',
  `decision_note` text,
  `decided_by` bigint DEFAULT NULL,
  `decided_at` datetime DEFAULT NULL,
  `applied_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_agent_proposal_checksum` (`owner_id`,`agent_id`,`kind`,`checksum`),
  KEY `idx_agent_proposal_scope` (`owner_id`,`agent_id`,`status`,`id`),
  KEY `idx_agent_proposal_review` (`review_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_improvement_reviews` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `agent_id` bigint NOT NULL,
  `agent_release_id` bigint NOT NULL,
  `conversation_id` bigint NOT NULL,
  `turn_id` bigint NOT NULL,
  `run_id` bigint NOT NULL,
  `provider_id` bigint NOT NULL,
  `model` varchar(255) NOT NULL DEFAULT '',
  `status` varchar(32) NOT NULL DEFAULT 'pending',
  `attempt_count` int NOT NULL DEFAULT '0',
  `max_attempts` int NOT NULL DEFAULT '3',
  `worker_id` varchar(255) NOT NULL DEFAULT '',
  `lease_token` varchar(64) NOT NULL DEFAULT '',
  `lease_expires_at` datetime DEFAULT NULL,
  `last_heartbeat_at` datetime DEFAULT NULL,
  `retry_at` datetime DEFAULT NULL,
  `error_message` text,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `completed_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_agent_review_turn` (`turn_id`),
  KEY `idx_agent_review_claim` (`status`,`retry_at`,`lease_expires_at`),
  KEY `idx_agent_review_scope` (`owner_id`,`agent_id`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_reflection_evidence` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `reflection_id` bigint NOT NULL,
  `job_id` bigint DEFAULT NULL,
  `run_id` bigint NOT NULL DEFAULT '0',
  `candidate_hash` char(64) NOT NULL,
  `evidence_json` json DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_reflection_evidence_job_candidate` (`job_id`,`candidate_hash`),
  KEY `idx_reflection_evidence_reflection` (`reflection_id`,`created_at`),
  KEY `idx_reflection_evidence_run` (`run_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_reflection_job_outbox` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `event_id` varchar(191) NOT NULL,
  `job_id` bigint NOT NULL,
  `dispatch_seq` int NOT NULL,
  `event_type` enum('job','dlq') NOT NULL DEFAULT 'job',
  `available_at` datetime NOT NULL,
  `status` enum('pending','publishing','published') NOT NULL DEFAULT 'pending',
  `attempt_count` int NOT NULL DEFAULT '0',
  `locked_by` varchar(128) NOT NULL DEFAULT '',
  `locked_at` datetime DEFAULT NULL,
  `published_at` datetime DEFAULT NULL,
  `last_error` text,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_reflection_outbox_event` (`event_id`),
  UNIQUE KEY `uk_reflection_outbox_dispatch` (`job_id`,`dispatch_seq`,`event_type`),
  KEY `idx_reflection_outbox_pending` (`status`,`available_at`,`id`),
  KEY `idx_reflection_outbox_published` (`published_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_reflection_jobs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `agent_id` bigint NOT NULL,
  `run_id` bigint NOT NULL,
  `trigger_hash` char(64) NOT NULL,
  `provider_id` bigint NOT NULL DEFAULT '0',
  `model` varchar(128) NOT NULL DEFAULT '',
  `mode` varchar(32) NOT NULL DEFAULT '',
  `task` text NOT NULL,
  `payload_json` json DEFAULT NULL,
  `status` enum('pending','running','completed','failed') NOT NULL DEFAULT 'pending',
  `attempt_count` int NOT NULL DEFAULT '0',
  `max_attempts` int NOT NULL DEFAULT '3',
  `locked_by` varchar(128) NOT NULL DEFAULT '',
  `locked_at` datetime DEFAULT NULL,
  `lock_token` char(32) NOT NULL DEFAULT '',
  `lease_expires_at` datetime DEFAULT NULL,
  `last_heartbeat_at` datetime DEFAULT NULL,
  `dispatch_seq` int NOT NULL DEFAULT '0',
  `retry_at` datetime DEFAULT NULL,
  `error_message` text,
  `failure_type` varchar(32) NOT NULL DEFAULT '',
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `completed_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_reflection_job_trigger` (`run_id`,`trigger_hash`),
  KEY `idx_reflection_job_claim` (`status`,`retry_at`,`id`),
  KEY `idx_reflection_job_owner_agent` (`owner_id`,`agent_id`),
  KEY `idx_reflection_job_lease` (`status`,`lease_expires_at`,`retry_at`,`id`),
  KEY `idx_reflection_jobs_agent` (`owner_id`,`agent_id`,`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_reflection_recall_logs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `reflection_id` bigint NOT NULL,
  `run_id` bigint NOT NULL,
  `score` double NOT NULL DEFAULT '0',
  `rank` int NOT NULL DEFAULT '0',
  `injected_tokens` int NOT NULL DEFAULT '0',
  `outcome` varchar(64) NOT NULL DEFAULT '',
  `verdict` varchar(32) NOT NULL DEFAULT '',
  `feedback_note` text,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `resolved_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_reflection_recall` (`reflection_id`,`run_id`),
  KEY `idx_reflection_recall_run` (`owner_id`,`run_id`),
  KEY `idx_reflection_recall_effect` (`reflection_id`,`verdict`,`outcome`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_reflections` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `agent_id` bigint NOT NULL DEFAULT '0',
  `source_run_id` bigint NOT NULL DEFAULT '0',
  `supersedes_id` bigint DEFAULT NULL,
  `scope` enum('agent','global') NOT NULL DEFAULT 'agent',
  `kind` enum('error_lesson','important_strategy') NOT NULL,
  `status` enum('candidate','active','validated','disputed','superseded','archived') NOT NULL DEFAULT 'candidate',
  `mode` varchar(32) NOT NULL DEFAULT '',
  `trigger_type` varchar(64) NOT NULL DEFAULT '',
  `task_fingerprint` char(64) NOT NULL DEFAULT '',
  `task_summary` text,
  `root_cause_category` varchar(64) NOT NULL DEFAULT '',
  `root_cause` text,
  `corrective_action` text NOT NULL,
  `lesson` text NOT NULL,
  `applicability` text,
  `evidence_json` json DEFAULT NULL,
  `tags_json` json DEFAULT NULL,
  `embedding_provider_id` bigint NOT NULL DEFAULT '0',
  `embedding_model` varchar(191) NOT NULL DEFAULT '',
  `embedding_dimensions` int NOT NULL DEFAULT '0',
  `importance` double NOT NULL DEFAULT '0.5',
  `confidence` double NOT NULL DEFAULT '0.5',
  `content_hash` char(64) NOT NULL,
  `recall_count` int NOT NULL DEFAULT '0',
  `successful_use_count` int NOT NULL DEFAULT '0',
  `harmful_count` int NOT NULL DEFAULT '0',
  `last_recalled_at` datetime DEFAULT NULL,
  `expires_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `deleted_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_owner_agent_content` (`owner_id`,`agent_id`,`content_hash`),
  KEY `idx_reflection_scope` (`owner_id`,`agent_id`,`scope`,`status`),
  KEY `idx_reflection_task` (`owner_id`,`task_fingerprint`),
  KEY `idx_reflection_quality` (`owner_id`,`status`,`importance`,`confidence`),
  KEY `idx_reflection_source_run` (`source_run_id`),
  KEY `idx_reflection_expires` (`expires_at`),
  KEY `idx_reflections_agent_scope` (`owner_id`,`agent_id`,`status`),
  KEY `idx_reflection_embedding_profile` (`owner_id`,`embedding_provider_id`,`embedding_model`,`embedding_dimensions`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_releases` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `agent_id` bigint NOT NULL,
  `version_no` int NOT NULL,
  `definition_json` json NOT NULL,
  `checksum` varchar(64) NOT NULL,
  `rule_hash` varchar(64) NOT NULL DEFAULT '',
  `tool_schema_hash` varchar(64) NOT NULL DEFAULT '',
  `resource_versions_json` json NOT NULL,
  `created_by` bigint NOT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_agent_release_version` (`agent_id`,`version_no`),
  KEY `idx_agent_releases_owner_agent` (`owner_id`,`agent_id`),
  KEY `idx_agent_releases_checksum` (`checksum`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_turns` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `agent_id` bigint NOT NULL,
  `agent_release_id` bigint NOT NULL,
  `conversation_id` bigint NOT NULL,
  `run_id` bigint DEFAULT NULL,
  `user_message_id` bigint NOT NULL,
  `assistant_message_id` bigint DEFAULT NULL,
  `idempotency_key` varchar(128) NOT NULL,
  `status` varchar(32) NOT NULL DEFAULT 'queued',
  `input_json` json NOT NULL,
  `output_json` json DEFAULT NULL,
  `error_message` text,
  `attempt_count` int NOT NULL DEFAULT '0',
  `max_attempts` int NOT NULL DEFAULT '3',
  `worker_id` varchar(128) NOT NULL DEFAULT '',
  `lease_token` varchar(64) NOT NULL DEFAULT '',
  `lease_expires_at` datetime DEFAULT NULL,
  `last_heartbeat_at` datetime DEFAULT NULL,
  `retry_at` datetime DEFAULT NULL,
  `started_at` datetime DEFAULT NULL,
  `finished_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_agent_turn_idempotency` (`owner_id`,`conversation_id`,`idempotency_key`),
  UNIQUE KEY `uk_agent_turn_run` (`run_id`),
  KEY `idx_agent_turns_agent_conversation` (`agent_id`,`conversation_id`,`id`),
  KEY `idx_agent_turn_claim` (`status`,`retry_at`,`lease_expires_at`,`id`),
  KEY `idx_agent_turn_worker` (`worker_id`,`lease_expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agents` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `name` varchar(255) NOT NULL,
  `description` text,
  `avatar_url` varchar(1024) DEFAULT NULL,
  `status` varchar(32) NOT NULL DEFAULT 'draft',
  `draft_definition_json` json NOT NULL,
  `current_release_id` bigint DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `deleted_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_agents_owner_status` (`owner_id`,`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_runs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `agent_id` bigint NOT NULL,
  `agent_release_id` bigint DEFAULT NULL,
  `conversation_id` bigint DEFAULT NULL,
  `parent_run_id` bigint DEFAULT NULL,
  `run_type` enum('turn','subagent') NOT NULL DEFAULT 'turn',
  `delegation_depth` int NOT NULL DEFAULT '0',
  `definition_json` json NOT NULL,
  `definition_hash` char(64) NOT NULL DEFAULT '',
  `rule_hash` char(64) NOT NULL DEFAULT '',
  `status` varchar(32) NOT NULL DEFAULT 'queued',
  `input_json` json NOT NULL,
  `output_json` json DEFAULT NULL,
  `error_message` text,
  `total_tokens` int NOT NULL DEFAULT '0',
  `latency_ms` int NOT NULL DEFAULT '0',
  `started_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `finished_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_agent_runs_owner_status` (`owner_id`,`status`,`id`),
  KEY `idx_agent_runs_agent` (`owner_id`,`agent_id`,`id`),
  KEY `idx_agent_runs_conversation` (`owner_id`,`conversation_id`,`id`),
  KEY `idx_agent_runs_parent` (`owner_id`,`parent_run_id`,`id`),
  KEY `idx_agent_runs_type_depth` (`run_type`,`delegation_depth`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_run_events` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `run_id` bigint NOT NULL,
  `event_type` varchar(64) NOT NULL,
  `payload_json` json NOT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_agent_run_events_run` (`owner_id`,`run_id`,`id`),
  KEY `idx_agent_run_events_type` (`event_type`,`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_run_steps` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `run_id` bigint NOT NULL,
  `step_index` int NOT NULL,
  `step_type` varchar(64) NOT NULL,
  `role` varchar(32) NOT NULL DEFAULT '',
  `content` mediumtext,
  `tool_call_id` varchar(191) NOT NULL DEFAULT '',
  `tool_name` varchar(191) NOT NULL DEFAULT '',
  `arguments_json` json DEFAULT NULL,
  `output_json` json DEFAULT NULL,
  `compressed` tinyint(1) NOT NULL DEFAULT '0',
  `error_message` text,
  `token_count` int NOT NULL DEFAULT '0',
  `latency_ms` int NOT NULL DEFAULT '0',
  `provider_id` bigint NOT NULL DEFAULT '0',
  `model` varchar(191) NOT NULL DEFAULT '',
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_agent_run_step_index` (`run_id`,`step_index`),
  KEY `idx_agent_run_steps_run` (`owner_id`,`run_id`,`step_index`,`id`),
  KEY `idx_agent_run_steps_tool_call` (`tool_call_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_run_checkpoints` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `run_id` bigint NOT NULL,
  `status` varchar(32) NOT NULL DEFAULT 'active',
  `snapshot_version` int NOT NULL DEFAULT '1',
  `interaction_id` varchar(191) NOT NULL DEFAULT '',
  `runtime_checkpoint_json` json DEFAULT NULL,
  `messages_json` json DEFAULT NULL,
  `messages_summary` mediumtext,
  `steps_json` json DEFAULT NULL,
  `pending_tool_call_json` json DEFAULT NULL,
  `context_json` json DEFAULT NULL,
  `tool_registry_hash` char(64) NOT NULL DEFAULT '',
  `tool_policy_hash` char(64) NOT NULL DEFAULT '',
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_agent_run_checkpoints_run` (`owner_id`,`run_id`,`id`),
  KEY `idx_agent_run_checkpoints_interaction` (`interaction_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `agent_approval_requests` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `run_id` bigint NOT NULL,
  `tool_call_id` varchar(191) NOT NULL DEFAULT '',
  `interaction_id` varchar(191) NOT NULL DEFAULT '',
  `tool_name` varchar(191) NOT NULL DEFAULT '',
  `risk_level` varchar(32) NOT NULL DEFAULT '',
  `reason` text,
  `request_json` json DEFAULT NULL,
  `status` enum('pending','approved','rejected') NOT NULL DEFAULT 'pending',
  `decision_note` text,
  `decided_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_agent_approvals_owner_status` (`owner_id`,`status`,`id`),
  KEY `idx_agent_approvals_run` (`owner_id`,`run_id`,`id`),
  KEY `idx_agent_approvals_interaction` (`interaction_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `api_tokens` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `name` varchar(128) NOT NULL,
  `token_hash` char(64) NOT NULL,
  `token_prefix` varchar(16) NOT NULL,
  `scopes` json DEFAULT NULL,
  `last_used_at` datetime DEFAULT NULL,
  `expires_at` datetime DEFAULT NULL,
  `revoked_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_token_hash` (`token_hash`),
  KEY `idx_owner_id` (`owner_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `audit_logs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `actor_id` bigint NOT NULL,
  `action` varchar(128) NOT NULL,
  `resource_type` varchar(64) NOT NULL,
  `resource_id` varchar(64) DEFAULT NULL,
  `detail_json` json DEFAULT NULL,
  `ip_address` varchar(64) DEFAULT NULL,
  `user_agent` varchar(512) DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_owner_id` (`owner_id`),
  KEY `idx_actor_id` (`actor_id`),
  KEY `idx_action` (`action`),
  KEY `idx_resource` (`resource_type`,`resource_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `auth_sessions` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `user_id` bigint NOT NULL,
  `refresh_token_hash` char(64) NOT NULL,
  `user_agent` varchar(512) DEFAULT NULL,
  `ip_address` varchar(64) DEFAULT NULL,
  `expires_at` datetime NOT NULL,
  `revoked_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_refresh_token_hash` (`refresh_token_hash`),
  KEY `idx_user_id` (`user_id`),
  KEY `idx_expires_at` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `cache_invalidation_outbox` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `resource_kind` varchar(64) NOT NULL,
  `attempts` int NOT NULL DEFAULT '0',
  `next_retry_at` datetime NOT NULL,
  `processed_at` datetime DEFAULT NULL,
  `last_error` text,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_cache_outbox_pending` (`processed_at`,`next_retry_at`,`id`),
  KEY `idx_cache_outbox_owner_kind` (`owner_id`,`resource_kind`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `context_resource_index_outbox` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `agent_id` bigint NOT NULL DEFAULT '0',
  `conversation_id` bigint NOT NULL DEFAULT '0',
  `resource_type` varchar(32) NOT NULL,
  `resource_id` varchar(191) NOT NULL,
  `operation` enum('upsert','delete') NOT NULL,
  `content_hash` char(64) NOT NULL DEFAULT '',
  `embedding_provider_id` bigint NOT NULL DEFAULT '0',
  `embedding_model` varchar(191) NOT NULL DEFAULT '',
  `embedding_dimensions` int NOT NULL DEFAULT '0',
  `embedding_profile_hash` char(64) NOT NULL DEFAULT '',
  `status` enum('pending','processing','completed','dead_letter') NOT NULL DEFAULT 'pending',
  `attempt_count` int NOT NULL DEFAULT '0',
  `max_attempts` int NOT NULL DEFAULT '8',
  `available_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `locked_by` varchar(128) NOT NULL DEFAULT '',
  `locked_at` datetime DEFAULT NULL,
  `lease_expires_at` datetime DEFAULT NULL,
  `last_error` text,
  `completed_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_context_resource_version` (`owner_id`,`resource_type`,`resource_id`,`content_hash`,`operation`,`embedding_profile_hash`),
  KEY `idx_context_outbox_claim` (`status`,`available_at`,`lease_expires_at`,`id`),
  KEY `idx_context_outbox_resource` (`owner_id`,`resource_type`,`resource_id`),
  KEY `idx_context_outbox_agent` (`owner_id`,`agent_id`,`resource_type`,`id`),
  KEY `idx_context_outbox_conversation` (`owner_id`,`conversation_id`,`resource_type`,`id`),
  KEY `idx_context_outbox_profile` (`embedding_profile_hash`,`status`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `conversation_compactions` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `conversation_id` bigint NOT NULL,
  `first_message_id` bigint NOT NULL DEFAULT '0',
  `last_message_id` bigint NOT NULL DEFAULT '0',
  `parent_snapshot_id` bigint DEFAULT NULL,
  `snapshot_version` int NOT NULL DEFAULT '0',
  `source_fingerprint` char(64) NOT NULL,
  `trigger_type` enum('auto','manual') NOT NULL,
  `status` enum('completed','fallback','failed') NOT NULL,
  `summary` mediumtext,
  `prompt_version` varchar(64) NOT NULL DEFAULT 'codex-compatible-v1',
  `prompt_hash` char(64) NOT NULL DEFAULT '',
  `provider_id` bigint NOT NULL DEFAULT '0',
  `model` varchar(191) NOT NULL DEFAULT '',
  `before_tokens` int NOT NULL DEFAULT '0',
  `after_tokens` int NOT NULL DEFAULT '0',
  `summary_token_limit` int NOT NULL DEFAULT '0',
  `summary_tokens` int NOT NULL DEFAULT '0',
  `error_message` text,
  `completed_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_conversation_compaction_source` (`owner_id`,`conversation_id`,`source_fingerprint`),
  KEY `idx_conversation_compaction_latest` (`owner_id`,`conversation_id`,`id`),
  KEY `idx_conversation_snapshot_current` (`owner_id`,`conversation_id`,`status`,`snapshot_version`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `conversation_snapshot_claims` (
  `owner_id` bigint NOT NULL,
  `conversation_id` bigint NOT NULL,
  `parent_snapshot_id` bigint DEFAULT NULL,
  `parent_version` int NOT NULL DEFAULT '0',
  `claim_token` varchar(64) NOT NULL,
  `expires_at` datetime NOT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`owner_id`,`conversation_id`),
  UNIQUE KEY `uk_conversation_snapshot_claim_token` (`claim_token`),
  KEY `idx_conversation_snapshot_claim_expiry` (`expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `conversations` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `title` varchar(255) DEFAULT NULL,
  `source` varchar(32) NOT NULL DEFAULT 'agent',
  `message_json` json DEFAULT NULL,
  `reference_json` json DEFAULT NULL,
  `last_message_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `deleted_at` datetime DEFAULT NULL,
  `agent_id` bigint DEFAULT NULL,
  `agent_release_id` bigint DEFAULT NULL,
  `agent_mode` varchar(32) NOT NULL DEFAULT 'react',
  `parent_conversation_id` bigint DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_owner_id` (`owner_id`),
  KEY `idx_last_message_at` (`last_message_at`),
  KEY `idx_owner_agent` (`owner_id`,`agent_id`),
  KEY `idx_agent_release_id` (`agent_release_id`),
  KEY `idx_parent_conversation_id` (`parent_conversation_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `document_chunks` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `kb_id` bigint NOT NULL,
  `document_id` bigint NOT NULL,
  `chunk_index` int NOT NULL,
  `content` mediumtext NOT NULL,
  `content_hash` varchar(64) NOT NULL,
  `token_count` int NOT NULL DEFAULT '0',
  `char_count` int NOT NULL DEFAULT '0',
  `page_no` int DEFAULT NULL,
  `section_title` varchar(255) DEFAULT NULL,
  `es_index` varchar(128) DEFAULT NULL,
  `es_doc_id` varchar(128) DEFAULT NULL,
  `metadata_json` json DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_doc_chunk_index` (`document_id`,`chunk_index`),
  KEY `idx_owner_kb` (`owner_id`,`kb_id`),
  KEY `idx_document_id` (`document_id`),
  KEY `idx_content_hash` (`content_hash`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `documents` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `kb_id` bigint NOT NULL,
  `name` varchar(255) NOT NULL,
  `original_filename` varchar(255) NOT NULL,
  `file_type` varchar(32) DEFAULT NULL,
  `mime_type` varchar(128) DEFAULT NULL,
  `file_size` bigint NOT NULL DEFAULT '0',
  `object_key` varchar(512) NOT NULL,
  `content_hash` varchar(64) DEFAULT NULL,
  `parser_status` varchar(32) NOT NULL DEFAULT 'pending',
  `enabled` tinyint(1) NOT NULL DEFAULT '1',
  `parser_error` text,
  `chunk_count` int NOT NULL DEFAULT '0',
  `token_count` int NOT NULL DEFAULT '0',
  `indexed_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `deleted_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_owner_kb` (`owner_id`,`kb_id`),
  KEY `idx_parser_status` (`parser_status`),
  KEY `idx_content_hash` (`content_hash`),
  KEY `idx_kb_enabled` (`kb_id`,`enabled`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `ingestion_jobs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `kb_id` bigint NOT NULL,
  `document_id` bigint NOT NULL,
  `job_type` varchar(64) NOT NULL DEFAULT 'document_ingestion',
  `status` varchar(32) NOT NULL DEFAULT 'pending',
  `priority` int NOT NULL DEFAULT '0',
  `attempt_count` int NOT NULL DEFAULT '0',
  `max_attempts` int NOT NULL DEFAULT '3',
  `error_message` text,
  `locked_by` varchar(128) DEFAULT NULL,
  `locked_at` datetime DEFAULT NULL,
  `started_at` datetime DEFAULT NULL,
  `finished_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_status_priority` (`status`,`priority`,`created_at`),
  KEY `idx_document_id` (`document_id`),
  KEY `idx_owner_id` (`owner_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `knowledge_bases` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `name` varchar(128) NOT NULL,
  `description` text,
  `retrieval_backend` varchar(32) NOT NULL DEFAULT 'elasticsearch',
  `retrieval_mode` varchar(32) NOT NULL DEFAULT 'keyword',
  `embedding_provider_id` bigint DEFAULT NULL,
  `embedding_model` varchar(128) NOT NULL DEFAULT '',
  `embedding_dimensions` int NOT NULL DEFAULT '0',
  `hybrid_weight` double NOT NULL DEFAULT '0.5',
  `rerank_enabled` tinyint(1) NOT NULL DEFAULT '0',
  `rerank_provider_id` bigint DEFAULT NULL,
  `rerank_model` varchar(128) NOT NULL DEFAULT '',
  `chunk_method` varchar(64) NOT NULL DEFAULT 'fixed_token',
  `chunk_size` int NOT NULL DEFAULT '800',
  `chunk_overlap` int NOT NULL DEFAULT '100',
  `status` tinyint NOT NULL DEFAULT '1',
  `document_count` int NOT NULL DEFAULT '0',
  `chunk_count` int NOT NULL DEFAULT '0',
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `deleted_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_owner_id` (`owner_id`),
  KEY `idx_status` (`status`),
  KEY `idx_embedding_provider_id` (`embedding_provider_id`),
  KEY `idx_rerank_provider_id` (`rerank_provider_id`),
  KEY `idx_knowledge_bases_owner_deleted_id` (`owner_id`,`deleted_at`,`id` DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `mcp_servers` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `name` varchar(128) NOT NULL,
  `transport` varchar(32) NOT NULL DEFAULT 'streamable_http',
  `endpoint_url` text,
  `command` text,
  `args_json` json DEFAULT NULL,
  `env_json` json DEFAULT NULL,
  `status` tinyint NOT NULL DEFAULT '1',
  `last_error` text,
  `discovered_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  `deleted_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_mcp_servers_owner` (`owner_id`,`deleted_at`),
  KEY `idx_mcp_servers_status` (`owner_id`,`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `mcp_tool_cache` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `server_id` bigint NOT NULL,
  `tool_name` varchar(128) NOT NULL,
  `description` text,
  `parameters_json` json DEFAULT NULL,
  `schema_hash` varchar(128) DEFAULT NULL,
  `cached_at` datetime NOT NULL,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uniq_mcp_tool_cache_server_name` (`owner_id`,`server_id`,`tool_name`),
  KEY `idx_mcp_tool_cache_server` (`owner_id`,`server_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `memories` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `parent_id` bigint DEFAULT NULL,
  `conflict_flag` tinyint(1) NOT NULL DEFAULT '0',
  `owner_id` bigint NOT NULL,
  `conversation_id` bigint DEFAULT NULL,
  `scope_type` enum('user','agent','conversation') NOT NULL DEFAULT 'user',
  `scope_id` bigint NOT NULL DEFAULT '0',
  `status` enum('active','superseded','revoked') NOT NULL DEFAULT 'active',
  `supersedes_id` bigint DEFAULT NULL,
  `session_id` varchar(64) DEFAULT NULL,
  `memory_type` varchar(64) NOT NULL,
  `memory_level` enum('working','short_term','long_term') NOT NULL DEFAULT 'long_term',
  `title` varchar(255) DEFAULT NULL,
  `content` text NOT NULL,
  `importance` double NOT NULL DEFAULT '0.5',
  `access_count` int NOT NULL DEFAULT '0',
  `consolidation_count` int NOT NULL DEFAULT '0',
  `source` varchar(64) DEFAULT NULL,
  `source_key` varchar(191) DEFAULT NULL,
  `metadata_json` json DEFAULT NULL,
  `embedding` blob,
  `last_used_at` datetime DEFAULT NULL,
  `last_decay_at` datetime DEFAULT NULL,
  `expires_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `deleted_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_memories_owner_source_key` (`owner_id`,`source_key`),
  KEY `idx_owner_type` (`owner_id`,`memory_type`),
  KEY `idx_conversation_id` (`conversation_id`),
  KEY `idx_importance` (`importance`),
  KEY `idx_memory_level` (`owner_id`,`memory_level`),
  KEY `idx_memories_owner_type_importance` (`owner_id`,`memory_type`,`importance` DESC),
  KEY `idx_memories_owner_deleted_updated_id` (`owner_id`,`deleted_at`,`updated_at` DESC,`id` DESC),
  KEY `idx_memories_scope_status` (`owner_id`,`scope_type`,`scope_id`,`status`,`memory_type`),
  KEY `idx_memories_supersedes` (`supersedes_id`),
  KEY `idx_memories_decay` (`owner_id`,`status`,`memory_level`,`last_decay_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `memory_extraction_jobs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `conversation_id` bigint NOT NULL,
  `idempotency_key` varchar(191) DEFAULT NULL,
  `trigger_reason` varchar(32) NOT NULL DEFAULT '',
  `source_message_ids` json DEFAULT NULL,
  `through_message_id` bigint NOT NULL DEFAULT '0',
  `status` enum('pending','running','completed','failed') NOT NULL DEFAULT 'pending',
  `due_at` datetime DEFAULT NULL,
  `attempt_count` int NOT NULL DEFAULT '0',
  `locked_by` varchar(128) NOT NULL DEFAULT '',
  `locked_at` datetime DEFAULT NULL,
  `lease_expires_at` datetime DEFAULT NULL,
  `result_json` json DEFAULT NULL,
  `error_message` text,
  `created_at` datetime NOT NULL,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `completed_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_memory_extraction_idempotency` (`owner_id`,`idempotency_key`),
  KEY `idx_owner_id` (`owner_id`),
  KEY `idx_conversation_id` (`conversation_id`),
  KEY `idx_status` (`status`),
  KEY `idx_memory_extraction_due` (`status`,`due_at`,`lease_expires_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `memory_merge_logs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `source_id` bigint NOT NULL,
  `target_id` bigint NOT NULL,
  `similarity` double NOT NULL DEFAULT '0',
  `reason` text,
  `created_at` datetime NOT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_owner_id` (`owner_id`),
  KEY `idx_source_id` (`source_id`),
  KEY `idx_target_id` (`target_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `memory_recall_logs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `agent_id` bigint NOT NULL DEFAULT '0',
  `conversation_id` bigint NOT NULL DEFAULT '0',
  `run_id` bigint NOT NULL DEFAULT '0',
  `query` text NOT NULL,
  `candidate_json` json NOT NULL,
  `injected_json` json NOT NULL,
  `token_cost` int NOT NULL DEFAULT '0',
  `feedback` varchar(32) NOT NULL DEFAULT '',
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_memory_recall_scope` (`owner_id`,`agent_id`,`conversation_id`,`id`),
  KEY `idx_memory_recall_run` (`owner_id`,`run_id`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `memory_write_logs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `memory_id` bigint DEFAULT NULL,
  `run_id` bigint DEFAULT NULL,
  `source_message_id` bigint DEFAULT NULL,
  `action` varchar(32) NOT NULL,
  `before_json` json DEFAULT NULL,
  `after_json` json DEFAULT NULL,
  `reason` text,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_owner_id` (`owner_id`),
  KEY `idx_run_id` (`run_id`),
  KEY `idx_memory_id` (`memory_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `message_references` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `message_id` bigint NOT NULL,
  `kb_id` bigint NOT NULL,
  `document_id` bigint NOT NULL,
  `chunk_id` bigint NOT NULL,
  `ref_index` int NOT NULL,
  `score` double DEFAULT NULL,
  `quote_text` text,
  `page_no` int DEFAULT NULL,
  `metadata_json` json DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_message_id` (`message_id`),
  KEY `idx_chunk_id` (`chunk_id`),
  KEY `idx_owner_id` (`owner_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `messages` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `conversation_id` bigint NOT NULL,
  `role` varchar(32) NOT NULL,
  `content` mediumtext NOT NULL,
  `content_type` varchar(32) NOT NULL DEFAULT 'text',
  `run_id` bigint DEFAULT NULL,
  `token_count` int NOT NULL DEFAULT '0',
  `metadata_json` json DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `archived_at` timestamp NULL DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_conversation_id` (`conversation_id`),
  KEY `idx_owner_id` (`owner_id`),
  KEY `idx_run_id` (`run_id`),
  KEY `idx_messages_archived` (`conversation_id`,`archived_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `model_providers` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `name` varchar(64) NOT NULL,
  `provider_type` varchar(64) NOT NULL,
  `base_url` varchar(512) DEFAULT NULL,
  `encrypted_api_key` text,
  `api_key_mask` varchar(64) DEFAULT NULL,
  `default_chat_model` varchar(128) DEFAULT NULL,
  `default_embedding_model` varchar(128) DEFAULT NULL,
  `status` tinyint NOT NULL DEFAULT '1',
  `last_test_status` varchar(32) DEFAULT NULL,
  `last_test_error` text,
  `last_test_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `deleted_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_owner_id` (`owner_id`),
  KEY `idx_provider_type` (`provider_type`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `model_usage_logs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `provider_id` bigint DEFAULT NULL,
  `provider_type` varchar(64) DEFAULT NULL,
  `model_name` varchar(128) DEFAULT NULL,
  `usage_type` varchar(32) NOT NULL,
  `prompt_tokens` int NOT NULL DEFAULT '0',
  `completion_tokens` int NOT NULL DEFAULT '0',
  `total_tokens` int NOT NULL DEFAULT '0',
  `estimated_cost` decimal(12,6) DEFAULT '0.000000',
  `latency_ms` int NOT NULL DEFAULT '0',
  `success` tinyint NOT NULL DEFAULT '1',
  `error_message` text,
  `request_id` varchar(128) DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_owner_id` (`owner_id`),
  KEY `idx_provider_id` (`provider_id`),
  KEY `idx_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `oauth_accounts` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `user_id` bigint NOT NULL,
  `provider` varchar(32) NOT NULL,
  `provider_user_id` varchar(128) NOT NULL,
  `provider_username` varchar(128) DEFAULT NULL,
  `provider_email` varchar(128) DEFAULT NULL,
  `avatar_url` varchar(512) DEFAULT NULL,
  `access_token_encrypted` text,
  `refresh_token_encrypted` text,
  `scopes` varchar(512) DEFAULT NULL,
  `token_expires_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_provider_user` (`provider`,`provider_user_id`),
  KEY `idx_user_id` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `retrieval_logs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `kb_ids` json NOT NULL,
  `query_text` text NOT NULL,
  `retrieval_backend` varchar(32) NOT NULL,
  `retrieval_mode` varchar(32) NOT NULL,
  `top_k` int NOT NULL,
  `result_count` int NOT NULL,
  `latency_ms` int NOT NULL DEFAULT '0',
  `results_json` json DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_owner_id` (`owner_id`),
  KEY `idx_created_at` (`created_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `skills` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `name` varchar(128) NOT NULL,
  `description` text NOT NULL,
  `skill_type` varchar(32) NOT NULL DEFAULT 'instruction',
  `source_type` varchar(32) NOT NULL DEFAULT 'inline',
  `entry_file` varchar(255) NOT NULL DEFAULT 'SKILL.md',
  `content_md` mediumtext,
  `bundle_path` varchar(1024) DEFAULT NULL,
  `tags_json` json DEFAULT NULL,
  `status` int NOT NULL DEFAULT '1',
  `version` int NOT NULL DEFAULT '1',
  `checksum` varchar(128) NOT NULL DEFAULT '',
  `last_validated_at` datetime DEFAULT NULL,
  `last_validation_error` text,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `deleted_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_skills_owner_name_version` (`owner_id`,`name`,`version`),
  KEY `idx_skills_owner_status_updated` (`owner_id`,`status`,`updated_at`),
  KEY `idx_skills_owner_deleted` (`owner_id`,`deleted_at`),
  KEY `idx_skills_owner_deleted_updated_id` (`owner_id`,`deleted_at`,`updated_at` DESC,`id` DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `tool_definitions` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `name` varchar(128) NOT NULL,
  `tool_type` varchar(64) NOT NULL,
  `description` text,
  `config_json` json NOT NULL,
  `input_schema_json` json DEFAULT NULL,
  `output_schema_json` json DEFAULT NULL,
  `status` tinyint NOT NULL DEFAULT '1',
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `deleted_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_owner_id` (`owner_id`),
  KEY `idx_tool_type` (`tool_type`),
  KEY `idx_tool_definitions_owner_deleted_updated_id` (`owner_id`,`deleted_at`,`updated_at` DESC,`id` DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `tool_invocations` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `run_id` bigint DEFAULT NULL,
  `agent_id` bigint NOT NULL DEFAULT '0',
  `tool_id` bigint DEFAULT NULL,
  `tool_name` varchar(128) DEFAULT NULL,
  `tool_type` varchar(64) DEFAULT NULL,
  `input_json` json DEFAULT NULL,
  `output_json` json DEFAULT NULL,
  `status` varchar(32) NOT NULL,
  `error_message` text,
  `latency_ms` int NOT NULL DEFAULT '0',
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  KEY `idx_owner_id` (`owner_id`),
  KEY `idx_run_id` (`run_id`),
  KEY `idx_agent_id` (`agent_id`),
  KEY `idx_tool_id` (`tool_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `tool_pack_items` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `pack_id` bigint NOT NULL,
  `tool_id` bigint NOT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_pack_tool` (`pack_id`,`tool_id`),
  KEY `idx_owner_pack` (`owner_id`,`pack_id`),
  KEY `idx_tool_id` (`tool_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `tool_packs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `name` varchar(255) NOT NULL,
  `description` text,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_owner_pack_name` (`owner_id`,`name`),
  KEY `idx_owner_id` (`owner_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `tool_policies` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `name` varchar(255) NOT NULL,
  `require_approval_for_risk` json DEFAULT NULL,
  `max_timeout_ms` int NOT NULL DEFAULT '30000',
  `max_output_bytes` int NOT NULL DEFAULT '65536',
  `allowed_hosts` json DEFAULT NULL,
  `credential_scope` varchar(255) DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_owner_policy_name` (`owner_id`,`name`),
  KEY `idx_owner_id` (`owner_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
/*!40101 SET @saved_cs_client     = @@character_set_client */;
/*!50503 SET character_set_client = utf8mb4 */;
CREATE TABLE `users` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `username` varchar(64) NOT NULL,
  `email` varchar(128) DEFAULT NULL,
  `password_hash` varchar(255) DEFAULT NULL,
  `avatar_url` varchar(512) DEFAULT NULL,
  `login_type` varchar(32) NOT NULL DEFAULT 'password',
  `status` tinyint NOT NULL DEFAULT '1',
  `last_login_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  `deleted_at` datetime DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `username` (`username`),
  UNIQUE KEY `email` (`email`),
  KEY `idx_email` (`email`),
  KEY `idx_status` (`status`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
/*!40101 SET character_set_client = @saved_cs_client */;
