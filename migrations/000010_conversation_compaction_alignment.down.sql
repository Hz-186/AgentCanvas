ALTER TABLE `conversation_compactions`
  ADD COLUMN `summary_token_limit` int NOT NULL DEFAULT 0 AFTER `after_tokens`,
  DROP COLUMN `first_message_content`,
  DROP COLUMN `context_window_tokens`,
  DROP COLUMN `window_number`,
  MODIFY COLUMN `trigger_type` enum('auto','manual') NOT NULL;
