package memory_usecase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"agentcanvas/internal/domain/memory"

	"github.com/google/uuid"
)

// CodexFileStore is the file-backed durable-memory boundary. It deliberately
// has no SQL memory categories: the owner directory and the artifact path are
// the only partitioning rules.
type CodexFileStore struct {
	Root string
}

var codexAdHocWriteMu sync.Mutex

func NewCodexFileStore(root string) *CodexFileStore {
	return &CodexFileStore{Root: strings.TrimSpace(root)}
}

func (s *CodexFileStore) ownerRoot(ownerID int64) (string, error) {
	if s == nil || ownerID <= 0 {
		return "", fmt.Errorf("codex memory owner is required")
	}
	root := strings.TrimSpace(s.Root)
	if root == "" {
		return "", fmt.Errorf("codex memory root is not configured")
	}
	owner := filepath.Join(root, fmt.Sprintf("owner-%d", ownerID))
	// Read paths are intentionally lazy: an owner with no consolidated
	// workspace is a normal empty-memory state, not an infrastructure error.
	if _, err := ensureCodexDirectory(root, false); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	// Reads must never follow a user-controlled symlink into another tenant.
	if _, err := ensureCodexDirectory(owner, false); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	return owner, nil
}

func (s *CodexFileStore) ReadSummary(ctx context.Context, ownerID int64, tokenBudget int) (string, error) {
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	default:
	}
	root, err := s.ownerRoot(ownerID)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(root, "memory_summary.md"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", nil
	}
	if tokenBudget <= 0 {
		tokenBudget = 1200
	}
	maxChars := tokenBudget * 4
	if maxChars < 512 {
		maxChars = 512
	}
	if len([]rune(value)) > maxChars {
		value = string([]rune(value)[:maxChars]) + "\n…"
	}
	return value, nil
}

func (s *CodexFileStore) Search(ctx context.Context, ownerID int64, query string, limit int) ([]memory.FileSearchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("memory search query is required")
	}
	root, err := s.ownerRoot(ownerID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(terms) == 0 {
		return nil, nil
	}
	paths := make([]string, 0, 16)
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			if path != root && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 || filepath.Ext(entry.Name()) != ".md" {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == "raw_memories.md" {
			return nil
		}
		// memory_summary.md is injected once during run assembly. Returning it
		// from read_memory would create a second recall of the same artifact;
		// ad-hoc/raw workspace inputs are consolidation-only and are not exposed
		// through the Agent detail reader.
		relSlash := filepath.ToSlash(rel)
		if relSlash == "memory_summary.md" || relSlash == "phase2_workspace_diff.md" || strings.HasPrefix(relSlash, "extensions/ad_hoc/") {
			return nil
		}
		if relSlash != "MEMORY.md" && !strings.HasPrefix(relSlash, "rollout_summaries/") && !strings.HasPrefix(relSlash, "skills/") {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	results := make([]memory.FileSearchResult, 0, minInt(limit, len(paths)))
	for _, path := range paths {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		text := string(data)
		lower := strings.ToLower(text)
		matched := true
		for _, term := range terms {
			if !strings.Contains(lower, term) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		results = append(results, memory.FileSearchResult{Path: filepath.ToSlash(rel), Content: truncateCodexDetail(text, 6000)})
		if len(results) >= limit {
			break
		}
	}
	return results, nil
}

// AppendAdHocNote is the only user-directed durable-memory mutation. It is
// append-only and intentionally writes outside MEMORY.md so consolidation can
// review the request with the same provenance rules as rollout input.
func (s *CodexFileStore) AppendAdHocNote(ctx context.Context, ownerID, conversationID, runID int64, request, answer string) (string, error) {
	if !HasExplicitMemoryIntent(request) {
		return "", fmt.Errorf("explicit memory intent is required")
	}
	root, err := s.ownerRoot(ownerID)
	if err != nil {
		return "", err
	}
	dir, err := ensureCodexDirectory(root, true, "extensions", "ad_hoc", "notes")
	if err != nil {
		return "", err
	}
	codexAdHocWriteMu.Lock()
	defer codexAdHocWriteMu.Unlock()
	if existing, err := existingAdHocNoteForRun(ctx, dir, runID); err != nil {
		return "", err
	} else if existing != "" {
		return existing, nil
	}
	if runID <= 0 {
		return "", fmt.Errorf("positive source run id is required")
	}
	// The marker is an O_EXCL reservation, so two processes cannot create a
	// second note for the same run. It is deliberately retained as an audit
	// tombstone; preferring a false negative over duplicate durable memory is
	// the safe failure mode.
	claims, err := ensureCodexDirectory(root, true, "extensions", "ad_hoc", "notes", ".claims")
	if err != nil {
		return "", err
	}
	claimPath := filepath.Join(claims, fmt.Sprintf("run-%d.claim", runID))
	now := time.Now().UTC()
	name := fmt.Sprintf("%s-%s.md", now.Format("20060102T150405.000000000Z"), uuid.NewString())
	path := filepath.Join(dir, name)
	content := fmt.Sprintf("# Ad-hoc memory note\n\n- created_at: %s\n- source_conversation_id: %d\n- source_run_id: %d\n\n## User request\n\n%s\n\n## Run answer\n\n%s\n", now.Format(time.RFC3339Nano), conversationID, runID, redactCodexSecrets(truncateCodexDetail(strings.TrimSpace(request), 12000)), redactCodexSecrets(truncateCodexDetail(strings.TrimSpace(answer), 12000)))
	claim, err := os.OpenFile(claimPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if os.IsExist(err) {
			if existing, findErr := existingAdHocNoteForRun(ctx, dir, runID); findErr == nil && existing != "" {
				return existing, nil
			}
			// A worker can die after reserving the marker but before publishing
			// the note. Reuse the reserved filename and finish that write; this
			// preserves idempotency without turning a transient I/O failure into
			// a permanent lost user request.
			reserved, readErr := readAdHocClaimNameEventually(ctx, claimPath)
			if readErr != nil {
				return "", fmt.Errorf("ad-hoc note for run %d is already reserved: %w", runID, readErr)
			}
			path = filepath.Join(dir, reserved)
			if writeErr := writeCodexAtomicExclusive(path, content); writeErr != nil && !os.IsExist(writeErr) {
				return "", writeErr
			}
			return path, nil
		}
		return "", err
	}
	if _, err := claim.WriteString(name + "\n"); err != nil {
		_ = claim.Close()
		return "", err
	}
	if err := claim.Close(); err != nil {
		return "", err
	}
	if err := writeCodexAtomicExclusive(path, content); err != nil {
		return "", err
	}
	return path, nil
}

func readAdHocClaimName(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(string(data))
	if name == "" || filepath.Base(name) != name || filepath.Ext(name) != ".md" {
		return "", fmt.Errorf("invalid ad-hoc claim target")
	}
	return name, nil
}

func readAdHocClaimNameEventually(ctx context.Context, path string) (string, error) {
	var err error
	for attempt := 0; attempt < 20; attempt++ {
		name, readErr := readAdHocClaimName(path)
		if readErr == nil {
			return name, nil
		}
		err = readErr
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	return "", err
}

// writeCodexAtomicExclusive publishes a file exactly once. Linking a closed
// temp file is atomic and returns EEXIST when another process won the claim.
func writeCodexAtomicExclusive(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".codex-memory-claim-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Link(tmpName, path)
}

// ensureCodexDirectory creates/checks only directories below the configured
// memory root and rejects symlink components. MkdirAll would otherwise follow
// a swapped symlink between the owner and notes paths.
func ensureCodexDirectory(root string, create bool, parts ...string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("codex memory root is not configured")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if create {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return "", err
		}
	}
	if err := checkCodexDir(root, create); err != nil {
		return "", err
	}
	current := root
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || filepath.Base(part) != part {
			return "", fmt.Errorf("invalid codex memory directory component %q", part)
		}
		next := filepath.Join(current, part)
		if create {
			if err := os.Mkdir(next, 0o700); err != nil && !os.IsExist(err) {
				return "", err
			}
		}
		if err := checkCodexDir(next, create); err != nil {
			return "", err
		}
		current = next
	}
	return current, nil
}

func checkCodexDir(path string, allowMissing bool) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) && allowMissing {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("codex memory path must not be a symlink: %s", path)
	}
	if !info.IsDir() {
		return fmt.Errorf("codex memory path is not a directory: %s", path)
	}
	return nil
}

func existingAdHocNoteForRun(ctx context.Context, dir string, runID int64) (string, error) {
	if runID <= 0 {
		return "", nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	needle := fmt.Sprintf("- source_run_id: %d", runID)
	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		path := filepath.Join(dir, entry.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		if strings.Contains(string(data), needle) {
			return path, nil
		}
	}
	return "", nil
}

func HasExplicitMemoryIntent(value string) bool {
	return memory.HasExplicitMemoryIntent(value)
}

func truncateCodexDetail(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	if maxChars <= 0 || len([]rune(value)) <= maxChars {
		return value
	}
	return string([]rune(value)[:maxChars]) + "\n…"
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

var _ memory.CodexReader = (*CodexFileStore)(nil)
var _ memory.AdHocWriter = (*CodexFileStore)(nil)
