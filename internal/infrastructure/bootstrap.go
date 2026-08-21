package infrastructure

import (
	"context"
	"fmt"

	domainretrieval "agentcanvas/internal/domain/retrieval"
	cryptoinfra "agentcanvas/internal/infrastructure/crypto"
	esinfra "agentcanvas/internal/infrastructure/elasticsearch"
	minioinfra "agentcanvas/internal/infrastructure/minio"
	mysqlinfra "agentcanvas/internal/infrastructure/mysql"
	queueinfra "agentcanvas/internal/infrastructure/queue"
	redisinfra "agentcanvas/internal/infrastructure/redis"
	memoryretrievalinfra "agentcanvas/internal/infrastructure/retrieval"
	esretrieval "agentcanvas/internal/infrastructure/retrieval/elasticsearch"
	milvusretrieval "agentcanvas/internal/infrastructure/retrieval/milvus"
	"agentcanvas/internal/infrastructure/vectorstore"
	"agentcanvas/internal/pkg/config"

	elasticsearch "github.com/elastic/go-elasticsearch/v8"
	"github.com/minio/minio-go/v7"
	goredis "github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

// RetrievalStore is the infrastructure-facing retrieval backend contract.
type RetrievalStore = domainretrieval.Backend

type InitOptions struct {
	IncludeMemoryRetrieval bool
	InitializeQueue        bool
	PingRedisQueue         bool
}

type InfraDeps struct {
	DB                      *gorm.DB
	Redis                   *goredis.Client
	MinIOClient             *minio.Client
	ElasticsearchClient     *elasticsearch.Client
	RetrievalStore          RetrievalStore
	RetrievalStores         map[string]RetrievalStore
	MemoryRetrievalStore    *memoryretrievalinfra.MemoryStore
	MemoryRetrievalIndexErr error
	JobQueue                queueinfra.JobQueue
	SecretBox               *cryptoinfra.SecretBox
	FileStorage             *minioinfra.FileStorage
}

func InitInfrastructure(ctx context.Context, cfg *config.Config, opts InitOptions) (*InfraDeps, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	db, err := mysqlinfra.New(cfg.MySQL)
	if err != nil {
		return nil, fmt.Errorf("init mysql: %w", err)
	}
	redisClient := redisinfra.New(cfg.Redis)
	minioClient, err := minioinfra.New(cfg.MinIO)
	if err != nil {
		return nil, fmt.Errorf("init minio: %w", err)
	}
	if err := minioinfra.EnsureBucket(ctx, minioClient, cfg.MinIO.Bucket); err != nil {
		return nil, fmt.Errorf("ensure minio bucket: %w", err)
	}
	esClient, err := esinfra.New(cfg.Elasticsearch)
	if err != nil {
		return nil, fmt.Errorf("init elasticsearch: %w", err)
	}
	esStore := esretrieval.NewStore(esClient, cfg.Elasticsearch)
	retrievalStores := map[string]RetrievalStore{"elasticsearch": esStore}
	if cfg.Retrieval.Backend == "milvus" {
		milvusVector := vectorstore.NewMilvusStore(cfg.Milvus.Address, cfg.Milvus.Token, milvusHNSW(cfg))
		milvusStore := milvusretrieval.NewStore(milvusVector, cfg.Milvus.Collection, cfg.Milvus.Dimensions, milvusHNSW(cfg))
		retrievalStores["milvus"] = milvusStore
	}
	retrievalStore := retrievalStores[cfg.Retrieval.Backend]
	if retrievalStore == nil {
		return nil, fmt.Errorf("retrieval backend %q is not configured", cfg.Retrieval.Backend)
	}
	if err := retrievalStore.EnsureIndex(ctx); err != nil {
		return nil, fmt.Errorf("ensure %s retrieval index: %w", cfg.Retrieval.Backend, err)
	}
	secretBox, err := cryptoinfra.NewSecretBox(cfg.Security.SecretEncryptKey)
	if err != nil {
		return nil, fmt.Errorf("init secret box: %w", err)
	}
	deps := &InfraDeps{
		DB:                  db,
		Redis:               redisClient,
		MinIOClient:         minioClient,
		ElasticsearchClient: esClient,
		RetrievalStore:      retrievalStore,
		RetrievalStores:     retrievalStores,
		SecretBox:           secretBox,
		FileStorage:         minioinfra.NewFileStorage(minioClient, cfg.MinIO.Bucket),
	}
	if opts.IncludeMemoryRetrieval {
		deps.MemoryRetrievalStore = memoryretrievalinfra.NewMemoryStore(esClient)
		deps.MemoryRetrievalIndexErr = deps.MemoryRetrievalStore.EnsureIndex(ctx)
	}
	if opts.InitializeQueue {
		if opts.PingRedisQueue && cfg.Queue.Backend == "redis_stream" {
			if err := redisinfra.Ping(ctx, redisClient); err != nil {
				return nil, fmt.Errorf("init redis stream queue: %w", err)
			}
		}
		deps.JobQueue, err = queueinfra.NewConfiguredJobQueue(ctx, cfg, redisClient)
		if err != nil {
			return nil, fmt.Errorf("init job queue: %w", err)
		}
	}
	return deps, nil
}

func milvusHNSW(cfg *config.Config) vectorstore.HNSWConfig {
	return vectorstore.HNSWConfig{M: cfg.Milvus.M, EFConstruction: cfg.Milvus.EFConstruction, EFSearch: cfg.Milvus.EFSearch, MetricType: cfg.Milvus.MetricType}
}
