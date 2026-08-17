ALTER TABLE knowledge_bases
  ADD COLUMN embedding_metric VARCHAR(16) NOT NULL DEFAULT 'COSINE' AFTER embedding_dimensions;

UPDATE knowledge_bases SET embedding_metric = 'COSINE' WHERE embedding_metric IS NULL OR embedding_metric = '';
