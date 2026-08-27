DROP TABLE IF EXISTS `memory_write_jobs`;
DROP TABLE IF EXISTS `memory_artifacts`;
ALTER TABLE `memories` DROP CHECK `ck_memories_source`;
ALTER TABLE `memories`
  CHANGE COLUMN `usage_count` `recall_count` int NOT NULL DEFAULT '0',
  CHANGE COLUMN `last_used_at` `last_recalled_at` datetime DEFAULT NULL;
