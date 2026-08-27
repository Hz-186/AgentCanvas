-- The durable-memory pipeline was renamed from its retired codex_memory
-- identity. Backfill extraction rows so phase-2 retry queries match the new
-- trigger_reason value.
UPDATE `memory_extraction_jobs`
SET `trigger_reason` = 'durable'
WHERE `trigger_reason` = 'codex';
