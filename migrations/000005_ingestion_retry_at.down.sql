ALTER TABLE ingestion_jobs DROP INDEX idx_ingestion_retry_at;
ALTER TABLE ingestion_jobs DROP COLUMN retry_at;
