DROP TABLE IF EXISTS agent_reflection_recall_logs;
DROP TABLE IF EXISTS agent_reflection_jobs;
DROP TABLE IF EXISTS agent_reflections;

ALTER TABLE workflow_profiles
    DROP COLUMN reflection_policy_json;
