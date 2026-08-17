ALTER TABLE document_chunks
  DROP INDEX uk_doc_generation_chunk_index,
  DROP INDEX idx_document_generation,
  ADD UNIQUE KEY uk_doc_chunk_index (document_id, chunk_index);
ALTER TABLE document_chunks DROP COLUMN generation;
ALTER TABLE documents DROP COLUMN active_generation;
