ALTER TABLE workflow_runs
    ADD COLUMN call_chain_json JSON NULL AFTER call_depth;
