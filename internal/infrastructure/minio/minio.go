package minio

import (
	"context"

	"agentcanvas/internal/pkg/config"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

func New(cfg config.MinIOConfig) (*minio.Client, error) {
	return minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
	})
}

func EnsureBucket(ctx context.Context, client *minio.Client, bucket string) error {
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	return client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{})
}

func Ping(ctx context.Context, client *minio.Client, bucket string) error {
	_, err := client.BucketExists(ctx, bucket)
	return err
}
