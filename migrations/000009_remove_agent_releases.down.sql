CREATE TABLE `agent_releases` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `agent_id` bigint NOT NULL,
  `version_number` int NOT NULL,
  `definition_json` json NOT NULL,
  `checksum` varchar(64) NOT NULL,
  `rule_hash` varchar(64) NOT NULL DEFAULT '',
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uk_agent_release_version` (`agent_id`,`version_number`),
  KEY `idx_agent_releases_owner_agent` (`owner_id`,`agent_id`),
  KEY `idx_agent_releases_checksum` (`checksum`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

ALTER TABLE `agent_improvement_reviews` ADD COLUMN `agent_release_id` bigint NOT NULL DEFAULT 0 AFTER `agent_id`;
ALTER TABLE `agent_turns` ADD COLUMN `agent_release_id` bigint NOT NULL DEFAULT 0 AFTER `agent_id`;
ALTER TABLE `agents` ADD COLUMN `current_release_id` bigint DEFAULT NULL AFTER `draft_definition_json`;
ALTER TABLE `agent_runs` ADD COLUMN `agent_release_id` bigint DEFAULT NULL AFTER `agent_id`;
ALTER TABLE `conversations` ADD COLUMN `agent_release_id` bigint DEFAULT NULL AFTER `agent_id`, ADD KEY `idx_agent_release_id` (`agent_release_id`);
