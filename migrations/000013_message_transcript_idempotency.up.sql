ALTER TABLE `messages`
  ADD COLUMN `transcript_entry_id` varchar(191) DEFAULT NULL AFTER `run_id`,
  ADD UNIQUE KEY `uk_messages_transcript_entry` (`owner_id`,`conversation_id`,`run_id`,`transcript_entry_id`);
