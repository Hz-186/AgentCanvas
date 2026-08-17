ALTER TABLE ingestion_jobs
  ADD COLUMN retry_at DATETIME NULL AFTER max_attempts,
  ADD KEY idx_ingestion_retry_at (status, retry_at, priority, created_at);
