package memory_usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"agentcanvas/internal/domain/memory"

	"gorm.io/gorm"
)

// MigrationArtifactSink is the artifact write seam used by the legacy file
// importer. The SQL MemoryArtifactRepository satisfies it; the importer never
// writes durable Markdown back to disk.
type MigrationArtifactSink interface {
	Create(ctx context.Context, artifact *memory.MemoryArtifact) error
	Latest(ctx context.Context, ownerID int64, kind string) (*memory.MemoryArtifact, error)
}

// SkillImportSink hands skill files to the skill subsystem. Skills are not
// memories: the importer never creates a memory artifact for a skill file.
type SkillImportSink interface {
	ImportSkill(ctx context.Context, ownerID int64, relPath, content string) error
}

// MemoryMigrationProgress records, per owner, the checksum of every legacy
// file already imported so a failed or repeated run resumes instead of
// duplicating artifacts or silently overwriting tampered sources.
type MemoryMigrationProgress struct {
	OwnerID int64             `json:"owner_id"`
	Files   map[string]string `json:"files"` // owner-relative slash path -> sha256 hex
}

type MigrationProgressStore interface {
	LoadProgress(ctx context.Context, ownerID int64) (*MemoryMigrationProgress, error)
	SaveProgress(ctx context.Context, ownerID int64, progress *MemoryMigrationProgress) error
}

// MemoryFileMigration deterministically imports legacy durable-memory files
// into the canonical SQL artifact surface. A checksum mismatch on any file
// already imported aborts the run before any artifact or skill is written,
// reporting the exact offending path.
type MemoryFileMigration struct {
	Root      string
	Artifacts MigrationArtifactSink
	Skills    SkillImportSink
	Progress  MigrationProgressStore
}

func NewMemoryFileMigration(root string, artifacts MigrationArtifactSink, skills SkillImportSink, progress MigrationProgressStore) *MemoryFileMigration {
	return &MemoryFileMigration{Root: root, Artifacts: artifacts, Skills: skills, Progress: progress}
}

const (
	migrationKindSkill = "skill"

	migrationSourceLegacyFile = "legacy_file"
)

type migrationFile struct {
	absPath string
	relPath string // slash-separated path relative to the owner directory
	kind    string // memory.ArtifactKind* or migrationKindSkill
	content string
}

// migrationFileKind maps an owner-relative slash path to an artifact kind.
// Unrecognized files (internal scratch such as phase2_workspace_diff.md) are
// ignored and never become artifacts.
func migrationFileKind(rel string) (string, bool) {
	switch rel {
	case "MEMORY.md":
		return memory.ArtifactKindHandbook, true
	case "memory_summary.md":
		return memory.ArtifactKindSummary, true
	case "raw_memories.md":
		return memory.ArtifactKindRawInput, true
	}
	switch {
	case strings.HasPrefix(rel, "skills/"):
		return migrationKindSkill, true
	case strings.HasPrefix(rel, "rollout_summaries/") && strings.HasSuffix(rel, ".md"):
		return memory.ArtifactKindRollout, true
	case strings.HasPrefix(rel, "extensions/ad_hoc/") && strings.HasSuffix(rel, ".md"):
		return memory.ArtifactKindAdHoc, true
	}
	return "", false
}

func (m *MemoryFileMigration) ownerDir(ownerID int64) (string, error) {
	if m == nil || m.Artifacts == nil || m.Progress == nil {
		return "", fmt.Errorf("memory file migration is not configured")
	}
	root := strings.TrimSpace(m.Root)
	if root == "" {
		return "", fmt.Errorf("durable memory root is not configured")
	}
	if ownerID <= 0 {
		return "", fmt.Errorf("durable memory owner is required")
	}
	if err := checkDurableDir(root); err != nil {
		return "", err
	}
	return filepath.Join(root, fmt.Sprintf("owner-%d", ownerID)), nil
}

// scanOwnerFiles lists every recognized import candidate below the owner
// directory in deterministic order. Symlinks are rejected outright.
func (m *MemoryFileMigration) scanOwnerFiles(ownerDir string) ([]migrationFile, error) {
	files := make([]migrationFile, 0, 8)
	err := filepath.WalkDir(ownerDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("legacy durable memory path must not be a symlink: %s", path)
		}
		if entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(ownerDir, path)
		if relErr != nil {
			return relErr
		}
		relSlash := filepath.ToSlash(rel)
		kind, ok := migrationFileKind(relSlash)
		if !ok {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		content := string(data)
		if kind != migrationKindSkill {
			content = strings.TrimSpace(content)
		}
		files = append(files, migrationFile{absPath: path, relPath: relSlash, kind: kind, content: content})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].relPath < files[j].relPath })
	return files, nil
}

func fileChecksum(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// Run imports every legacy file for one owner. All files already recorded in
// progress are verified before anything is written: a single checksum
// mismatch aborts the whole run with the exact offending path. Unchanged
// files are skipped, so a rerun after a partial failure resumes cleanly.
func (m *MemoryFileMigration) Run(ctx context.Context, ownerID int64) error {
	ownerDir, err := m.ownerDir(ownerID)
	if err != nil {
		return err
	}
	if err := checkDurableDir(ownerDir); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	files, err := m.scanOwnerFiles(ownerDir)
	if err != nil {
		return err
	}
	progress, err := m.Progress.LoadProgress(ctx, ownerID)
	if err != nil {
		return err
	}
	if progress == nil {
		progress = &MemoryMigrationProgress{OwnerID: ownerID, Files: map[string]string{}}
	}
	if progress.Files == nil {
		progress.Files = map[string]string{}
	}
	for _, file := range files {
		saved, ok := progress.Files[file.relPath]
		if !ok {
			continue
		}
		if saved != fileChecksum(file.content) {
			return fmt.Errorf("legacy durable memory file changed after import: %s", file.absPath)
		}
	}
	for _, file := range files {
		if progress.Files[file.relPath] == fileChecksum(file.content) {
			continue
		}
		if file.kind == migrationKindSkill {
			if m.Skills == nil {
				return fmt.Errorf("skill import sink is not configured")
			}
			if err := m.Skills.ImportSkill(ctx, ownerID, file.relPath, file.content); err != nil {
				return fmt.Errorf("import skill %s: %w", file.relPath, err)
			}
			progress.Files[file.relPath] = fileChecksum(file.content)
			continue
		}
		version := 1
		latest, latestErr := m.Artifacts.Latest(ctx, ownerID, file.kind)
		switch {
		case latestErr == nil && latest != nil:
			version = latest.Version + 1
		case latestErr != nil && !errors.Is(latestErr, gorm.ErrRecordNotFound):
			return fmt.Errorf("read latest %s artifact for owner %d: %w", file.kind, ownerID, latestErr)
		}
		artifact := &memory.MemoryArtifact{
			Kind:           file.kind,
			Version:        version,
			Content:        file.content,
			Source:         migrationSourceLegacyFile,
			SourceRefsJSON: json.RawMessage("[]"),
			Checksum:       fileChecksum(file.content),
		}
		artifact.OwnerID = ownerID
		if err := m.Artifacts.Create(ctx, artifact); err != nil {
			return fmt.Errorf("create %s artifact for owner %d: %w", file.kind, ownerID, err)
		}
		progress.Files[file.relPath] = fileChecksum(file.content)
	}
	return m.Progress.SaveProgress(ctx, ownerID, progress)
}

// OwnerIDs scans the durable root and returns the sorted owner IDs it holds.
// The root must contain only owner-<id> directories: any foreign path or
// symlink aborts the scan before anything is imported or deleted, reporting
// the exact offending path.
func (m *MemoryFileMigration) OwnerIDs() ([]int64, error) {
	root := strings.TrimSpace(m.Root)
	if root == "" {
		return nil, fmt.Errorf("durable memory root is not configured")
	}
	if err := checkDurableDir(root); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	owners := make([]int64, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("unexpected foreign path in durable memory root: %s", filepath.Join(root, name))
		}
		if !strings.HasPrefix(name, "owner-") {
			return nil, fmt.Errorf("unexpected foreign path in durable memory root: %s", filepath.Join(root, name))
		}
		id, parseErr := strconv.ParseInt(strings.TrimPrefix(name, "owner-"), 10, 64)
		if parseErr != nil || id <= 0 {
			return nil, fmt.Errorf("unexpected foreign path in durable memory root: %s", filepath.Join(root, name))
		}
		owners = append(owners, id)
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i] < owners[j] })
	return owners, nil
}

// RunAll scans the durable root and imports every owner directory. The root
// must contain only owner-<id> directories: any foreign path aborts the whole
// scan before a single import, reporting the exact offending path.
func (m *MemoryFileMigration) RunAll(ctx context.Context) error {
	owners, err := m.OwnerIDs()
	if err != nil {
		return err
	}
	for _, ownerID := range owners {
		if err := m.Run(ctx, ownerID); err != nil {
			return err
		}
	}
	return nil
}
