-- Canonical SQL memory contracts: usage accounting, versioned artifacts and
-- idempotent leased write jobs. This migration assumes 000008 has normalized
-- the memories column names.
ALTER TABLE `memories`
  CHANGE COLUMN `recall_count` `usage_count` int NOT NULL DEFAULT '0',
  CHANGE COLUMN `last_recalled_at` `last_used_at` datetime DEFAULT NULL;

UPDATE `memories`
SET `source` = 'manual'
WHERE `source` IS NOT NULL
  AND `source` NOT IN ('extraction','ad_hoc','proposal','consolidation','reflection','manual');

ALTER TABLE `memories`
  ADD CONSTRAINT `ck_memories_source` CHECK (`source` IS NULL OR `source` IN ('extraction','ad_hoc','proposal','consolidation','reflection','manual'));

CREATE TABLE `memory_artifacts` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `kind` enum('handbook','summary','raw_input','rollout_summary','ad_hoc') NOT NULL,
  `version` int NOT NULL,
  `content` longtext NOT NULL,
  `source` varchar(64) DEFAULT NULL,
  `source_refs_json` json DEFAULT NULL,
  `checksum` varchar(128) NOT NULL,
  `protected_at` datetime DEFAULT NULL,
  `consolidated_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_memory_artifacts_owner_kind_version` (`owner_id`,`kind`,`version`),
  KEY `idx_memory_artifacts_owner_kind` (`owner_id`,`kind`,`version` DESC)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE `memory_write_jobs` (
  `id` bigint NOT NULL AUTO_INCREMENT,
  `owner_id` bigint NOT NULL,
  `idempotency_key` varchar(191) NOT NULL,
  `source` enum('extraction','ad_hoc','proposal','consolidation','reflection','manual') NOT NULL,
  `payload_json` json DEFAULT NULL,
  `status` enum('pending','running','completed','failed','dead_letter') NOT NULL DEFAULT 'pending',
  `attempt_count` int NOT NULL DEFAULT '0',
  `due_at` datetime DEFAULT NULL,
  `locked_by` varchar(128) NOT NULL DEFAULT '',
  `locked_at` datetime DEFAULT NULL,
  `lease_expires_at` datetime DEFAULT NULL,
  `error_message` text,
  `completed_at` datetime DEFAULT NULL,
  `created_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  `updated_at` datetime NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  PRIMARY KEY (`id`),
  UNIQUE KEY `uq_memory_write_jobs_owner_idempotency` (`owner_id`,`idempotency_key`),
  KEY `idx_memory_write_jobs_claim` (`status`,`due_at`,`lease_expires_at`,`id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
