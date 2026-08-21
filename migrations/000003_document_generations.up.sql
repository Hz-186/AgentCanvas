ALTER TABLE documents
  ADD COLUMN active_generation VARCHAR(64) NOT NULL DEFAULT 'legacy' AFTER content_hash;

ALTER TABLE document_chunks
  ADD COLUMN generation VARCHAR(64) NOT NULL DEFAULT 'legacy' AFTER document_id;

ALTER TABLE document_chunks
  DROP INDEX uk_doc_chunk_index,
  ADD UNIQUE KEY uk_doc_generation_chunk_index (document_id, generation, chunk_index),
  ADD KEY idx_document_generation (document_id, generation);

UPDATE documents SET active_generation = 'legacy' WHERE active_generation IS NULL OR active_generation = '';
UPDATE document_chunks SET generation = 'legacy' WHERE generation IS NULL OR generation = '';
