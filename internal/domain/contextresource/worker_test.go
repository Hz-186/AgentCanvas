package contextresource

import (
	"context"
	"errors"
	"testing"
	"time"
)

type workerRepoFake struct {
	items       []OutboxItem
	document    *Document
	completedID int64
	retriedID   int64
	retryCause  error
	profile     EmbeddingProfile
}

func (f *workerRepoFake) Claim(context.Context, string, int, time.Duration) ([]OutboxItem, error) {
	return f.items, nil
}
func (f *workerRepoFake) Renew(context.Context, int64, string, time.Duration) error { return nil }
func (f *workerRepoFake) Complete(_ context.Context, id int64, _ string, profile EmbeddingProfile) error {
	f.completedID, f.profile = id, profile
	return nil
}
func (f *workerRepoFake) Retry(_ context.Context, id int64, _ string, cause error, _ time.Time) error {
	f.retriedID, f.retryCause = id, cause
	return nil
}
func (f *workerRepoFake) LoadDocument(context.Context, OutboxItem) (*Document, error) {
	return f.document, nil
}

type workerIndexFake struct {
	upserts int
	deletes int
	err     error
	profile EmbeddingProfile
}

func (f *workerIndexFake) Upsert(context.Context, Document, EmbeddingProfile) (EmbeddingProfile, error) {
	f.upserts++
	return f.profile, f.err
}
func (f *workerIndexFake) Delete(context.Context, OutboxItem) error {
	f.deletes++
	return f.err
}
func (f *workerIndexFake) Search(context.Context, SearchRequest) ([]SearchResult, error) {
	return nil, nil
}

func TestWorkerCompletesIndexedVersionWithResolvedProfile(t *testing.T) {
	content := "durable memory"
	repo := &workerRepoFake{items: []OutboxItem{{ID: 9, Operation: OperationUpsert, ContentHash: HashContent(content)}}, document: &Document{Content: content, ContentHash: HashContent(content)}}
	index := &workerIndexFake{profile: EmbeddingProfile{ProviderID: 3, Model: "embed", Dimensions: 1536}.Normalized()}
	worker := &Worker{Repository: repo, Index: index, WorkerID: "worker"}
	processed, err := worker.ProcessBatch(context.Background())
	if err != nil || processed != 1 || index.upserts != 1 || repo.completedID != 9 || repo.profile.Dimensions != 1536 {
		t.Fatalf("unexpected result processed=%d err=%v repo=%+v index=%+v", processed, err, repo, index)
	}
}

func TestWorkerCompletesStaleVersionWithoutOverwritingNewerVector(t *testing.T) {
	repo := &workerRepoFake{items: []OutboxItem{{ID: 10, Operation: OperationUpsert, ContentHash: HashContent("old")}}, document: &Document{Content: "new", ContentHash: HashContent("new")}}
	index := &workerIndexFake{}
	worker := &Worker{Repository: repo, Index: index, WorkerID: "worker"}
	if _, err := worker.ProcessBatch(context.Background()); err != nil {
		t.Fatal(err)
	}
	if index.upserts != 0 || repo.completedID != 10 {
		t.Fatalf("stale version was not safely completed: repo=%+v index=%+v", repo, index)
	}
}

func TestWorkerRetriesIndexFailure(t *testing.T) {
	indexErr := errors.New("milvus unavailable")
	content := "resource"
	repo := &workerRepoFake{items: []OutboxItem{{ID: 11, Operation: OperationUpsert, ContentHash: HashContent(content)}}, document: &Document{Content: content, ContentHash: HashContent(content)}}
	index := &workerIndexFake{err: indexErr}
	worker := &Worker{Repository: repo, Index: index, WorkerID: "worker"}
	if _, err := worker.ProcessBatch(context.Background()); !errors.Is(err, indexErr) {
		t.Fatalf("expected index error, got %v", err)
	}
	if repo.retriedID != 11 || !errors.Is(repo.retryCause, indexErr) || repo.completedID != 0 {
		t.Fatalf("failure was not retried: %+v", repo)
	}
}
