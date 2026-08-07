CREATE TABLE `projects` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `slug` varchar(64) NOT NULL,
  `name` varchar(255) NOT NULL,
  `description` text,
  `icon` varchar(255) NOT NULL DEFAULT '',
  `color` varchar(64) NOT NULL DEFAULT '',
  `primary_path` varchar(2048) NOT NULL,
  `primary_path_hash` binary(32) GENERATED ALWAYS AS (UNHEX(SHA2(`primary_path`, 256))) STORED,
  `archived` tinyint(1) NOT NULL DEFAULT '0',
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_projects_owner_slug` (`owner_id`,`slug`),
  UNIQUE KEY `uk_projects_owner_path` (`owner_id`,`primary_path_hash`),
  KEY `idx_projects_owner_archived` (`owner_id`,`archived`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE `project_folders` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `project_id` bigint NOT NULL,
  `path` varchar(2048) NOT NULL,
  `path_hash` binary(32) GENERATED ALWAYS AS (UNHEX(SHA2(`path`, 256))) STORED,
  `label` varchar(255) NOT NULL DEFAULT '',
  `is_primary` tinyint(1) NOT NULL DEFAULT '0',
  `added_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_project_folders_project_path` (`project_id`,`path_hash`),
  KEY `idx_project_folders_owner_project` (`owner_id`,`project_id`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE `agent_workspaces` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `project_id` bigint NOT NULL,
  `run_id` bigint NOT NULL,
  `parent_workspace_id` bigint DEFAULT NULL,
  `kind` enum('shared','worktree') NOT NULL,
  `repository_root` varchar(2048) NOT NULL,
  `repository_root_hash` binary(32) GENERATED ALWAYS AS (UNHEX(SHA2(`repository_root`, 256))) STORED,
  `workspace_path` varchar(2048) NOT NULL,
  `workspace_path_hash` binary(32) GENERATED ALWAYS AS (UNHEX(SHA2(`workspace_path`, 256))) STORED,
  `branch_name` varchar(255) NOT NULL DEFAULT '',
  `worktree_path_hash` binary(32) GENERATED ALWAYS AS (IF(`kind` = 'worktree', UNHEX(SHA2(`workspace_path`, 256)), NULL)) STORED,
  `worktree_branch_name` varchar(255) GENERATED ALWAYS AS (IF(`kind` = 'worktree', `branch_name`, NULL)) STORED,
  `base_ref` varchar(255) NOT NULL DEFAULT '',
  `base_sha` char(40) NOT NULL DEFAULT '',
  `head_sha` char(40) NOT NULL DEFAULT '',
  `status` varchar(32) NOT NULL DEFAULT 'creating',
  `dirty` tinyint(1) NOT NULL DEFAULT '0',
  `unpushed` tinyint(1) NOT NULL DEFAULT '0',
  `locked` tinyint(1) NOT NULL DEFAULT '0',
  `lock_reason` varchar(255) NOT NULL DEFAULT '',
  `cleanup_reason` varchar(512) NOT NULL DEFAULT '',
  `error_message` text,
  `last_checked_at` datetime DEFAULT NULL,
  `cleaned_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_workspace_owner_run` (`owner_id`,`run_id`),
  UNIQUE KEY `uk_workspace_repo_path` (`repository_root_hash`,`worktree_path_hash`),
  UNIQUE KEY `uk_workspace_repo_branch` (`repository_root_hash`,`worktree_branch_name`),
  KEY `idx_workspace_repo_path` (`repository_root_hash`,`workspace_path_hash`),
  KEY `idx_workspace_project_status` (`owner_id`,`project_id`,`status`,`id`),
  KEY `idx_workspace_parent` (`owner_id`,`parent_workspace_id`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

ALTER TABLE `conversations`
  ADD COLUMN `project_id` bigint DEFAULT NULL AFTER `agent_release_id`,
  ADD COLUMN `workspace_mode` varchar(32) NOT NULL DEFAULT 'shared' AFTER `project_id`,
  ADD KEY `idx_conversations_project` (`owner_id`,`project_id`,`id`);

ALTER TABLE `agent_runs`
  ADD COLUMN `workspace_id` bigint DEFAULT NULL AFTER `conversation_id`,
  ADD KEY `idx_agent_runs_workspace` (`owner_id`,`workspace_id`,`id`);
