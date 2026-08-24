UPDATE `memories`
  SET `scope_type` = CASE WHEN `conversation_id` IS NULL THEN 'user' ELSE 'conversation' END,
      `scope_id` = CASE WHEN `conversation_id` IS NULL THEN `owner_id` ELSE `conversation_id` END
  WHERE `scope_type` = 'project';

ALTER TABLE `memories`
  DROP KEY `idx_memories_project`,
  DROP COLUMN `project_id`,
  MODIFY COLUMN `scope_type` enum('user','agent','conversation') NOT NULL DEFAULT 'user';
