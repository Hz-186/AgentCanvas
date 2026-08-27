ALTER TABLE `memory_extraction_jobs`
  ADD COLUMN `phase2_attempt_count` int NOT NULL DEFAULT '0' AFTER `attempt_count`;
