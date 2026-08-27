ALTER TABLE `messages`
  DROP INDEX `uk_messages_transcript_entry`,
  DROP COLUMN `transcript_entry_id`;
