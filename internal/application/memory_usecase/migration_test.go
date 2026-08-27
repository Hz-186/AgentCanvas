package memory_usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"agentcanvas/internal/domain/memory"

	"gorm.io/gorm"
)

type fakeMigrationArtifacts struct {
	created []memory.MemoryArtifact
	latest  map[string]*memory.MemoryArtifact
}

func (f *fakeMigrationArtifacts) Create(_ context.Context, artifact *memory.MemoryArtifact) error {
	if f.latest == nil {
		f.latest = make(map[string]*memory.MemoryArtifact)
	}
	copyArtifact := *artifact
	f.created = append(f.created, copyArtifact)
	key := artifactKey(artifact.OwnerID, artifact.Kind)
	f.latest[key] = &copyArtifact
	return nil
}

func (f *fakeMigrationArtifacts) Latest(_ context.Context, ownerID int64, kind string) (*memory.MemoryArtifact, error) {
	if f.latest == nil {
		return nil, gorm.ErrRecordNotFound
	}
	item, ok := f.latest[artifactKey(ownerID, kind)]
	if !ok {
		return nil, gorm.ErrRecordNotFound
	}
	return item, nil
}

func artifactKey(ownerID int64, kind string) string {
	return filepath.Join("owner", itoa(ownerID), kind)
}

func itoa(value int64) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

type fakeMigrationSkillSink struct {
	imported []importedSkill
}

type importedSkill struct {
	OwnerID int64
	Path    string
	Content string
}

func (f *fakeMigrationSkillSink) ImportSkill(_ context.Context, ownerID int64, relPath, content string) error {
	f.imported = append(f.imported, importedSkill{OwnerID: ownerID, Path: relPath, Content: content})
	return nil
}

type fakeMigrationProgress struct {
	states map[int64]MemoryMigrationProgress
}

func (f *fakeMigrationProgress) LoadProgress(_ context.Context, ownerID int64) (*MemoryMigrationProgress, error) {
	if f.states == nil {
		return &MemoryMigrationProgress{OwnerID: ownerID, Files: map[string]string{}}, nil
	}
	state, ok := f.states[ownerID]
	if !ok {
		return &MemoryMigrationProgress{OwnerID: ownerID, Files: map[string]string{}}, nil
	}
	files := make(map[string]string, len(state.Files))
	for path, checksum := range state.Files {
		files[path] = checksum
	}
	return &MemoryMigrationProgress{OwnerID: ownerID, Files: files}, nil
}

func (f *fakeMigrationProgress) SaveProgress(_ context.Context, _ int64, progress *MemoryMigrationProgress) error {
	if f.states == nil {
		f.states = make(map[int64]MemoryMigrationProgress)
	}
	files := make(map[string]string, len(progress.Files))
	for path, checksum := range progress.Files {
		files[path] = checksum
	}
	f.states[progress.OwnerID] = MemoryMigrationProgress{OwnerID: progress.OwnerID, Files: files}
	return nil
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

// writeOwnerFile creates dirs as needed below root/owner-<id>/rel and returns
// the absolute path.
func writeOwnerFile(t *testing.T, root string, ownerID int64, rel string, content string) string {
	t.Helper()
	path := filepath.Join(root, "owner-"+itoa(ownerID), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func newTestMigration(root string, artifacts *fakeMigrationArtifacts, skills *fakeMigrationSkillSink, progress *fakeMigrationProgress) *MemoryFileMigration {
	return NewMemoryFileMigration(root, artifacts, skills, progress)
}

func TestMemoryMigrationShouldImportAllArtifactKinds(t *testing.T) {
	root := t.TempDir()
	handbook := "# Handbook\nuser prefers concise answers"
	summary := "routing summary"
	raw := "- fact one\n- fact two"
	rollout := "rollout one happened"
	skill := "# SKILL\nhow to deploy"
	adHoc := "adhoc note"
	writeOwnerFile(t, root, 42, "MEMORY.md", handbook)
	writeOwnerFile(t, root, 42, "memory_summary.md", summary)
	writeOwnerFile(t, root, 42, "raw_memories.md", raw)
	writeOwnerFile(t, root, 42, "rollout_summaries/rollout-one.md", rollout)
	writeOwnerFile(t, root, 42, "skills/deploy/SKILL.md", skill)
	writeOwnerFile(t, root, 42, "extensions/ad_hoc/note.md", adHoc)

	artifacts := &fakeMigrationArtifacts{}
	skills := &fakeMigrationSkillSink{}
	migration := newTestMigration(root, artifacts, skills, &fakeMigrationProgress{})

	if err := migration.Run(context.Background(), 42); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	kinds := map[string]int{}
	for _, artifact := range artifacts.created {
		if artifact.OwnerID != 42 {
			t.Fatalf("artifact owner = %d, want 42", artifact.OwnerID)
		}
		if artifact.Version <= 0 {
			t.Fatalf("artifact %s version = %d, want positive", artifact.Kind, artifact.Version)
		}
		if artifact.Checksum != sha256Hex(artifact.Content) {
			t.Fatalf("artifact %s checksum = %s, want content hash", artifact.Kind, artifact.Checksum)
		}
		if artifact.Source != "legacy_file" {
			t.Fatalf("artifact %s source = %q, want legacy_file", artifact.Kind, artifact.Source)
		}
		kinds[artifact.Kind]++
	}
	for _, kind := range []string{memory.ArtifactKindHandbook, memory.ArtifactKindSummary, memory.ArtifactKindRawInput, memory.ArtifactKindRollout, memory.ArtifactKindAdHoc} {
		if kinds[kind] != 1 {
			t.Fatalf("artifact kind %s count = %d, want 1", kind, kinds[kind])
		}
	}
	if len(artifacts.created) != 5 {
		t.Fatalf("artifact count = %d, want 5 (skill must not create a memory artifact)", len(artifacts.created))
	}
	if len(skills.imported) != 1 {
		t.Fatalf("skill handoff count = %d, want 1", len(skills.imported))
	}
	if skills.imported[0].OwnerID != 42 || skills.imported[0].Path != "skills/deploy/SKILL.md" || skills.imported[0].Content != skill {
		t.Fatalf("unexpected skill handoff: %+v", skills.imported[0])
	}
	for _, artifact := range artifacts.created {
		if strings.Contains(artifact.Content, "deploy") {
			t.Fatalf("skill content leaked into a memory artifact: %+v", artifact)
		}
	}

	// Rerunnable: a second run must not create duplicate artifacts.
	if err := migration.Run(context.Background(), 42); err != nil {
		t.Fatalf("rerun failed: %v", err)
	}
	if len(artifacts.created) != 5 {
		t.Fatalf("rerun artifact count = %d, want 5 (idempotent)", len(artifacts.created))
	}
	if len(skills.imported) != 1 {
		t.Fatalf("rerun skill handoff count = %d, want 1", len(skills.imported))
	}
}

func TestMemoryMigrationShouldAbortOnChecksumOrOwnerMismatch(t *testing.T) {
	ctx := context.Background()
	t.Run("checksum mismatch stops before delete and reports the exact path", func(t *testing.T) {
		root := t.TempDir()
		memoPath := writeOwnerFile(t, root, 42, "MEMORY.md", "original handbook")
		writeOwnerFile(t, root, 42, "memory_summary.md", "summary")

		artifacts := &fakeMigrationArtifacts{}
		skills := &fakeMigrationSkillSink{}
		progress := &fakeMigrationProgress{}
		migration := newTestMigration(root, artifacts, skills, progress)
		if err := migration.Run(ctx, 42); err != nil {
			t.Fatalf("first run failed: %v", err)
		}
		if err := os.WriteFile(memoPath, []byte("tampered handbook"), 0o644); err != nil {
			t.Fatalf("tamper %s: %v", memoPath, err)
		}

		// A tampered file must abort the migration naming the offending path.
		err := migration.Run(ctx, 42)
		if err == nil {
			t.Fatal("expected checksum mismatch to abort the migration")
		}
		if !strings.Contains(err.Error(), memoPath) {
			t.Fatalf("error %q does not report the offending path %q", err.Error(), memoPath)
		}
		if len(artifacts.created) != 2 {
			t.Fatalf("tampered run created artifacts: %+v", artifacts.created)
		}

		// The abort must happen before any cleanup delete: a cleanup run gated
		// on migration validation performs zero delete actions.
		deleted := 0
		cleanup := &LegacyCleanup{
			ValidateMigration: func(_ context.Context) error { return migration.Run(ctx, 42) },
			ValidateESBackfill: func(_ context.Context) error {
				return nil
			},
			Actions: []CleanupAction{
				{ID: "delete_legacy_files", Run: func(_ context.Context) error { deleted++; return nil }},
			},
			Progress: &fakeCleanupProgress{done: map[string]bool{}},
		}
		if cleanupErr := cleanup.Run(ctx); cleanupErr == nil {
			t.Fatal("expected cleanup to stop on migration validation failure")
		} else if !strings.Contains(cleanupErr.Error(), memoPath) {
			t.Fatalf("cleanup error %q does not report the offending path %q", cleanupErr.Error(), memoPath)
		}
		if deleted != 0 {
			t.Fatalf("cleanup deleted %d time(s) despite the migration abort", deleted)
		}
	})

	t.Run("foreign owner path aborts before any import", func(t *testing.T) {
		root := t.TempDir()
		writeOwnerFile(t, root, 7, "MEMORY.md", "owner seven handbook")
		foreign := filepath.Join(root, "notes")
		if err := os.MkdirAll(foreign, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", foreign, err)
		}
		if err := os.WriteFile(filepath.Join(foreign, "stray.md"), []byte("not an owner dir"), 0o644); err != nil {
			t.Fatalf("write stray file: %v", err)
		}

		artifacts := &fakeMigrationArtifacts{}
		migration := newTestMigration(root, artifacts, &fakeMigrationSkillSink{}, &fakeMigrationProgress{})
		err := migration.RunAll(ctx)
		if err == nil {
			t.Fatal("expected foreign owner path to abort the root scan")
		}
		if !strings.Contains(err.Error(), foreign) {
			t.Fatalf("error %q does not report the foreign owner path %q", err.Error(), foreign)
		}
		if len(artifacts.created) != 0 {
			t.Fatalf("foreign owner path did not stop the scan before imports: %+v", artifacts.created)
		}
	})
}

// sortedArtifacts returns the created artifacts sorted by kind for assertions.
func sortedArtifacts(artifacts []memory.MemoryArtifact) []memory.MemoryArtifact {
	items := append([]memory.MemoryArtifact(nil), artifacts...)
	sort.Slice(items, func(i, j int) bool { return items[i].Kind < items[j].Kind })
	return items
}
