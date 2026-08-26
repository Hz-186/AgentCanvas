ALTER TABLE `conversation_compactions`
  MODIFY COLUMN `trigger_type` enum('auto','manual','runtime') NOT NULL,
  ADD COLUMN `window_number` int NOT NULL DEFAULT 0 AFTER `snapshot_version`,
  ADD COLUMN `context_window_tokens` int NOT NULL DEFAULT 0 AFTER `window_number`,
  ADD COLUMN `first_message_content` longtext NULL AFTER `last_message_id`,
  DROP COLUMN `summary_token_limit`;
