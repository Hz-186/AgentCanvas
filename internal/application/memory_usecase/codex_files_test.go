package memory_usecase

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCodexFileStoreAdHocNoteIsIdempotentAcrossInstances(t *testing.T) {
	root := t.TempDir()
	stores := []*CodexFileStore{NewCodexFileStore(root), NewCodexFileStore(root)}
	paths := make([]string, len(stores))
	errs := make([]error, len(stores))
	var wg sync.WaitGroup
	for i, store := range stores {
		wg.Add(1)
		go func(index int, fileStore *CodexFileStore) {
			defer wg.Done()
			paths[index], errs[index] = fileStore.AppendAdHocNote(context.Background(), 7, 3, 42, "请记住我偏好简洁回答", "已记录")
		}(i, store)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("instance %d append failed: %v", i, err)
		}
	}
	if paths[0] == "" || paths[0] != paths[1] {
		t.Fatalf("instances returned different note paths: %q %q", paths[0], paths[1])
	}
	entries, err := os.ReadDir(filepath.Dir(paths[0]))
	if err != nil {
		t.Fatal(err)
	}
	var notes int
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".md" {
			notes++
		}
	}
	if notes != 1 {
		t.Fatalf("created %d ad-hoc notes, want one", notes)
	}
}

func TestCodexFileStoreResumesReservedAdHocClaim(t *testing.T) {
	root := t.TempDir()
	claims := filepath.Join(root, "owner-7", "extensions", "ad_hoc", "notes", ".claims")
	if err := os.MkdirAll(claims, 0o700); err != nil {
		t.Fatal(err)
	}
	reserved := "20260101T000000.000000000Z-reserved.md"
	claimPath := filepath.Join(claims, "run-42.claim")
	if err := os.WriteFile(claimPath, []byte(reserved+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := NewCodexFileStore(root).AppendAdHocNote(context.Background(), 7, 3, 42, "请记住这个偏好", "已记录")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != reserved {
		t.Fatalf("resumed path=%q, want %q", filepath.Base(path), reserved)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("reserved note was not published: %v", err)
	}
}
