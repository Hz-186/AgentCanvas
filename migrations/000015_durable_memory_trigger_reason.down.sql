UPDATE `memory_extraction_jobs`
SET `trigger_reason` = 'codex'
WHERE `trigger_reason` = 'durable';
