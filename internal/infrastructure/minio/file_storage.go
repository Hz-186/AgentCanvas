package minio

import (
	"context"
	"io"

	"github.com/minio/minio-go/v7"
)

type FileStorage struct {
	client *minio.Client
	bucket string
}

func NewFileStorage(client *minio.Client, bucket string) *FileStorage {
	return &FileStorage{client: client, bucket: bucket}
}

func (s *FileStorage) Put(ctx context.Context, objectKey string, reader io.Reader, size int64, contentType string) error {
	_, err := s.client.PutObject(ctx, s.bucket, objectKey, reader, size, minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (s *FileStorage) Get(ctx context.Context, objectKey string) (io.ReadCloser, error) {
	return s.client.GetObject(ctx, s.bucket, objectKey, minio.GetObjectOptions{})
}
