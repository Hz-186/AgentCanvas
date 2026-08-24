ALTER TABLE `memories`
  MODIFY COLUMN `scope_type` enum('user','agent','conversation','project') NOT NULL DEFAULT 'user',
  ADD COLUMN `project_id` bigint DEFAULT NULL AFTER `conversation_id`;

ALTER TABLE `memories`
  ADD KEY `idx_memories_project` (`owner_id`,`project_id`,`status`,`memory_type`);

UPDATE `context_resource_index_outbox`
  SET `status` = 'pending',
      `attempt_count` = 0,
      `available_at` = CURRENT_TIMESTAMP,
      `locked_by` = '',
      `locked_at` = NULL,
      `lease_expires_at` = NULL,
      `completed_at` = NULL,
      `last_error` = ''
  WHERE `resource_type` = 'long_term_memory'
    AND `operation` = 'upsert'
    AND `status` = 'completed';
