-- Retire the legacy memory surfaces: the reflection subsystem (rows were
-- converted into ordinary memories by the migration importer) and the
-- zero-caller memory write log. The tables are dropped with IF EXISTS so the
-- cleanup stage and this migration converge idempotently. The reflection API,
-- index, worker and config were removed in the same release.

DROP TABLE IF EXISTS `agent_reflection_evidence`;
DROP TABLE IF EXISTS `agent_reflection_job_outbox`;
DROP TABLE IF EXISTS `agent_reflection_jobs`;
DROP TABLE IF EXISTS `agent_reflection_recall_logs`;
DROP TABLE IF EXISTS `agent_reflections`;
DROP TABLE IF EXISTS `memory_write_logs`;
