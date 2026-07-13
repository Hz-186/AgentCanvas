ALTER TABLE workflow_runs
    DROP COLUMN compiled_rule_hash,
    DROP COLUMN rule_set_version,
    DROP COLUMN rule_set_id;

ALTER TABLE workflow_profiles
    DROP COLUMN rule_compiler_model,
    DROP COLUMN rule_compiler_provider_id,
    DROP COLUMN active_rule_set_id;

DROP TABLE IF EXISTS workflow_rule_compile_jobs;
DROP TABLE IF EXISTS workflow_rule_edges;
DROP TABLE IF EXISTS workflow_rule_nodes;
DROP TABLE IF EXISTS workflow_rule_sets;

