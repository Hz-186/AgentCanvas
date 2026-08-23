ALTER TABLE `memory_extraction_jobs`
  ADD COLUMN `project_id` bigint NOT NULL DEFAULT 0 AFTER `conversation_id`,
  MODIFY COLUMN `status` enum('pending','running','completed','failed','superseded') NOT NULL DEFAULT 'pending';

ALTER TABLE `memory_extraction_jobs`
  ADD KEY `idx_memory_extraction_project` (`owner_id`,`project_id`,`status`);
