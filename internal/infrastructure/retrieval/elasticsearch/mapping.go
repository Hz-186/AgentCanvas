package elasticsearch

const chunkIndexMapping = `{
  "settings": {
    "analysis": {
      "analyzer": {
        "default_text_analyzer": {
          "type": "standard"
        }
      }
    }
  },
  "mappings": {
    "properties": {
      "owner_id": { "type": "long" },
      "knowledge_base_id": { "type": "long" },
      "document_id": { "type": "long" },
      "chunk_id": { "type": "long" },
      "chunk_index": { "type": "integer" },
      "document_name": { "type": "keyword" },
      "file_type": { "type": "keyword" },
      "section_title": {
        "type": "text",
        "fields": {
          "keyword": { "type": "keyword", "ignore_above": 256 }
        }
      },
      "content": {
        "type": "text",
        "analyzer": "default_text_analyzer"
      },
      "content_hash": { "type": "keyword" },
      "enabled": { "type": "boolean" },
      "embedding_vector": {
        "type": "dense_vector",
        "index": true,
        "similarity": "cosine"
      },
      "embedding_model": { "type": "keyword" },
      "embedding_dimensions": { "type": "integer" },
	  "embedding_provider_id": { "type": "long" },
	  "embedding_metric": { "type": "keyword" },
	  "embedding_profile": { "type": "keyword" },
      "page_number": { "type": "integer" },
      "token_count": { "type": "integer" },
      "metadata": { "type": "object", "enabled": true },
      "created_at": { "type": "date" },
      "updated_at": { "type": "date" }
    }
  }
}`

const chunkVectorMapping = `{
  "properties": {
    "enabled": { "type": "boolean" },
    "embedding_vector": {
      "type": "dense_vector",
      "index": true,
      "similarity": "cosine"
    },
    "embedding_model": { "type": "keyword" },
    "embedding_dimensions": { "type": "integer" },
    "embedding_provider_id": { "type": "long" },
    "embedding_metric": { "type": "keyword" },
    "embedding_profile": { "type": "keyword" }
  }
}`

const chunkVectorMappingWithoutVector = `{
  "properties": {
    "enabled": { "type": "boolean" },
    "embedding_model": { "type": "keyword" },
    "embedding_dimensions": { "type": "integer" },
    "embedding_provider_id": { "type": "long" },
    "embedding_metric": { "type": "keyword" },
    "embedding_profile": { "type": "keyword" }
  }
}`
