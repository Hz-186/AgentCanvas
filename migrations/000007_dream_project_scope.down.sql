UPDATE `memory_extraction_jobs`
  SET `status` = 'completed'
  WHERE `status` = 'superseded';

ALTER TABLE `memory_extraction_jobs`
  DROP KEY `idx_memory_extraction_project`,
  DROP COLUMN `project_id`,
  MODIFY COLUMN `status` enum('pending','running','completed','failed') NOT NULL DEFAULT 'pending';
