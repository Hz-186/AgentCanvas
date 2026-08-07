package toolruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	gitinfra "agentcanvas/internal/infrastructure/git"
)

const defaultReadChars = 100000

var workspaceLocks sync.Map

type readFileInput struct {
	Path   string `json:"path"`
	Offset int    `json:"offset"`
	Limit  int    `json:"limit"`
}
type searchFilesInput struct {
	Pattern        string `json:"pattern"`
	Path           string `json:"path"`
	MaxResults     int    `json:"max_results"`
	MaxOutputBytes int    `json:"max_output_bytes"`
}
type listFilesInput struct {
	Path       string `json:"path"`
	MaxResults int    `json:"max_results"`
}
type writeFileInput struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	ExpectedSHA256 string `json:"expected_sha256"`
}
type patchFileInput struct {
	Path                 string            `json:"path"`
	Mode                 string            `json:"mode"`
	OldString            string            `json:"old_string"`
	NewString            string            `json:"new_string"`
	ReplaceAll           bool              `json:"replace_all"`
	Patch                string            `json:"patch"`
	ExpectedSHA256       string            `json:"expected_sha256"`
	ExpectedSHA256ByPath map[string]string `json:"expected_sha256_by_path"`
}
type moveFileInput struct {
	From           string `json:"from"`
	To             string `json:"to"`
	ExpectedSHA256 string `json:"expected_sha256"`
}
type deleteFileInput struct {
	Path           string `json:"path"`
	ExpectedSHA256 string `json:"expected_sha256"`
}

type FileTool struct {
	Kind           string
	MaxReadChars   int
	MaxOutputBytes int
}

func (t FileTool) Name() string { return t.Kind }
func (t FileTool) Description() string {
	switch t.Kind {
	case "read_file":
		return "Read UTF-8 text from the active Agent workspace with line pagination."
	case "search_files":
		return "Search text files in the active Agent workspace."
	case "list_files":
		return "List files in the active Agent workspace."
	case "write_file":
		return "Create or overwrite a UTF-8 file in the active Agent workspace."
	case "patch_file":
		return "Replace an exact string or apply a safe patch in the active Agent workspace."
	case "move_file":
		return "Move a file inside the active Agent workspace."
	case "delete_file":
		return "Delete a file inside the active Agent workspace."
	default:
		return "Unsupported workspace file operation."
	}
}
func (t FileTool) Parameters() json.RawMessage {
	switch t.Kind {
	case "read_file":
		return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"offset":{"type":"number"},"limit":{"type":"number"}},"required":["path"],"additionalProperties":false}`)
	case "search_files":
		return json.RawMessage(`{"type":"object","properties":{"pattern":{"type":"string"},"path":{"type":"string"},"max_results":{"type":"number"},"max_output_bytes":{"type":"number"}},"required":["pattern"],"additionalProperties":false}`)
	case "list_files":
		return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"max_results":{"type":"number"}},"additionalProperties":false}`)
	case "write_file":
		return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"content":{"type":"string"},"expected_sha256":{"type":"string"}},"required":["path","content"],"additionalProperties":false}`)
	case "patch_file":
		return json.RawMessage(`{"type":"object","properties":{"mode":{"type":"string","enum":["replace","patch"],"default":"replace"},"path":{"type":"string"},"old_string":{"type":"string"},"new_string":{"type":"string"},"replace_all":{"type":"boolean","default":false},"patch":{"type":"string"},"expected_sha256":{"type":"string"},"expected_sha256_by_path":{"type":"object","additionalProperties":{"type":"string"}}},"additionalProperties":false}`)
	case "move_file":
		return json.RawMessage(`{"type":"object","properties":{"from":{"type":"string"},"to":{"type":"string"},"expected_sha256":{"type":"string"}},"required":["from","to"],"additionalProperties":false}`)
	case "delete_file":
		return json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"expected_sha256":{"type":"string"}},"required":["path"],"additionalProperties":false}`)
	default:
		return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	}
}
func (t FileTool) Metadata() ToolMetadata {
	metadata := ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectRead, TimeoutMS: 10000, MaxOutputBytes: t.MaxOutputBytes}
	if t.Kind == "write_file" || t.Kind == "patch_file" {
		metadata.RiskLevel, metadata.SideEffect = RiskMedium, SideEffectWrite
	}
	if t.Kind == "move_file" || t.Kind == "delete_file" {
		metadata.RiskLevel, metadata.SideEffect = RiskHigh, SideEffectWrite
		metadata.RequiresApproval = true
	}
	return metadata
}

func workspacePath(rc ToolRunContext, raw string) (string, error) {
	if rc.Workspace == nil || strings.TrimSpace(rc.Workspace.WorkspacePath) == "" {
		return "", errors.New("workspace is not configured")
	}
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("path is required")
	}
	for _, part := range strings.Split(filepath.ToSlash(raw), "/") {
		if part == ".." {
			return "", errors.New("path traversal is not allowed")
		}
	}
	path := raw
	if !filepath.IsAbs(path) {
		path = filepath.Join(rc.Workspace.WorkspacePath, path)
	}
	path, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	if _, err := gitinfra.EnsureSafePath(rc.Workspace.WorkspacePath, path); err != nil {
		return "", err
	}
	if gitinfra.IsSensitivePath(rc.Workspace.WorkspacePath, path) {
		return "", errors.New("path is protected")
	}
	return path, nil
}

func fileHash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

func atomicWriteFile(path string, data []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, ".agentcanvas-write-*")
	if err != nil {
		return err
	}
	temporaryPath := file.Name()
	keepTemporary := true
	defer func() {
		_ = file.Close()
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := file.Chmod(mode.Perm()); err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	keepTemporary = false
	return nil
}

func pathLock(path string) *sync.Mutex {
	value, _ := workspaceLocks.LoadOrStore(path, &sync.Mutex{})
	return value.(*sync.Mutex)
}

func acquirePathLock(workspaceRoot, path string) (func(), error) {
	mutex := pathLock(path)
	mutex.Lock()
	lockRoot := workspaceFileLockRoot(workspaceRoot)
	if err := os.MkdirAll(lockRoot, 0o700); err != nil {
		mutex.Unlock()
		return nil, err
	}
	sum := sha256.Sum256([]byte(filepath.Clean(path)))
	file, err := os.OpenFile(filepath.Join(lockRoot, hex.EncodeToString(sum[:])+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		mutex.Unlock()
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		mutex.Unlock()
		return nil, err
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
		mutex.Unlock()
	}, nil
}

func workspaceFileLockRoot(workspaceRoot string) string {
	cmd := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	cmd.Dir = workspaceRoot
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	if output, err := cmd.Output(); err == nil {
		if commonDir := strings.TrimSpace(string(output)); filepath.IsAbs(commonDir) {
			return filepath.Join(filepath.Clean(commonDir), "agentcanvas-file-locks")
		}
	}
	sum := sha256.Sum256([]byte(filepath.Clean(workspaceRoot)))
	return filepath.Join(os.TempDir(), "agentcanvas-file-locks", hex.EncodeToString(sum[:]))
}

func (t FileTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if rc.Workspace == nil || !rc.Workspace.FileWriteEnabled && (t.Kind == "write_file" || t.Kind == "patch_file" || t.Kind == "move_file" || t.Kind == "delete_file") {
		return &ToolResult{ContentText: "workspace file writes are disabled", IsError: true}, errors.New("workspace file writes are disabled")
	}
	switch t.Kind {
	case "read_file":
		var in readFileInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return t.read(ctx, rc, in)
	case "search_files":
		var in searchFilesInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return t.search(ctx, rc, in)
	case "list_files":
		var in listFilesInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return t.list(ctx, rc, in)
	case "write_file":
		var in writeFileInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return t.write(ctx, rc, in)
	case "patch_file":
		var in patchFileInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return t.patch(ctx, rc, in)
	case "move_file":
		var in moveFileInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return t.move(ctx, rc, in)
	case "delete_file":
		var in deleteFileInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		return t.delete(ctx, rc, in)
	default:
		return nil, errors.New("unsupported workspace file tool: " + t.Kind)
	}
}

func (t FileTool) read(_ context.Context, rc ToolRunContext, in readFileInput) (*ToolResult, error) {
	path, err := workspacePath(rc, in.Path)
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	if !utf8.Valid(data) {
		return &ToolResult{ContentText: "file is not valid UTF-8", IsError: true}, errors.New("file is not valid UTF-8")
	}
	if in.Offset <= 0 {
		in.Offset = 1
	}
	if in.Limit <= 0 {
		in.Limit = 2000
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	start := in.Offset - 1
	if start > len(lines) {
		start = len(lines)
	}
	end := start + in.Limit
	if end > len(lines) {
		end = len(lines)
	}
	var builder strings.Builder
	characters := 0
	nextLine := start
	contentTruncated := false
	partialLine := false
	for i := start; i < end; i++ {
		line := fmt.Sprintf("%d|%s", i+1, lines[i])
		separator := 0
		if builder.Len() > 0 {
			separator = 1
		}
		lineCharacters := utf8.RuneCountInString(line)
		if characters+lineCharacters+separator > t.readLimit() {
			remaining := t.readLimit() - characters - separator
			if remaining > 0 {
				if separator > 0 {
					builder.WriteByte('\n')
				}
				builder.WriteString(truncateCharacters(line, remaining))
				nextLine = i + 1
				partialLine = true
			} else {
				nextLine = i
			}
			contentTruncated = true
			break
		}
		if builder.Len() > 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(line)
		characters += lineCharacters + separator
		nextLine = i + 1
	}
	truncated := contentTruncated || nextLine < len(lines)
	result := map[string]any{"path": path, "content": builder.String(), "total_lines": len(lines), "sha256": fileHash(data), "truncated": truncated}
	if truncated {
		result["next_offset"] = nextLine + 1
		if contentTruncated {
			result["truncated_by"] = "characters"
			hint := fmt.Sprintf("Output truncated at the %d-character read budget. Use offset=%d to continue reading", t.readLimit(), nextLine+1)
			if partialLine {
				hint += ". The last displayed line was clamped mid-line; its remainder is not retrievable with the line-based offset"
			}
			result["hint"] = hint
		} else {
			result["truncated_by"] = "lines"
			result["hint"] = fmt.Sprintf("Line limit reached. Use offset=%d to continue reading", nextLine+1)
		}
	}
	return ResultFromValue(result)
}
func truncateCharacters(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	return string([]rune(value)[:limit])
}
func (t FileTool) readLimit() int {
	if t.MaxReadChars <= 0 {
		return defaultReadChars
	}
	return t.MaxReadChars
}

func (t FileTool) search(_ context.Context, rc ToolRunContext, in searchFilesInput) (*ToolResult, error) {
	if in.Pattern == "" {
		return nil, errors.New("search pattern is required")
	}
	root := rc.Workspace.WorkspacePath
	globPattern := ""
	var err error
	if in.Path != "" {
		if strings.ContainsAny(in.Path, "*?[") {
			if filepath.IsAbs(in.Path) {
				return &ToolResult{ContentText: "search path glob must be relative", IsError: true}, errors.New("search path glob must be relative")
			}
			if _, err := gitinfra.EnsureInside(rc.Workspace.WorkspacePath, filepath.Join(rc.Workspace.WorkspacePath, filepath.Dir(in.Path))); err != nil {
				return &ToolResult{ContentText: err.Error(), IsError: true}, err
			}
			globPattern = filepath.ToSlash(in.Path)
			if _, err := matchPathGlob(globPattern, ""); err != nil {
				return nil, fmt.Errorf("invalid search path glob: %w", err)
			}
		} else {
			root, err = workspacePath(rc, in.Path)
			if err != nil {
				return &ToolResult{ContentText: err.Error(), IsError: true}, err
			}
		}
	}
	if in.MaxResults <= 0 || in.MaxResults > 500 {
		in.MaxResults = 100
	}
	if in.MaxOutputBytes <= 0 {
		in.MaxOutputBytes = t.MaxOutputBytes
		if in.MaxOutputBytes <= 0 {
			in.MaxOutputBytes = 256 * 1024
		}
	}
	var matches []map[string]any
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if len(matches) >= in.MaxResults {
			return filepath.SkipAll
		}
		if entry.IsDir() {
			if path != root && (entry.Name() == ".git" || entry.Name() == ".agentcanvas" || entry.Name() == ".worktrees") {
				return filepath.SkipDir
			}
			return nil
		}
		if gitinfra.IsSensitivePath(rc.Workspace.WorkspacePath, path) {
			return nil
		}
		if globPattern != "" {
			rel, _ := filepath.Rel(rc.Workspace.WorkspacePath, path)
			matched, matchErr := matchPathGlob(globPattern, filepath.ToSlash(rel))
			if matchErr != nil || !matched {
				return nil
			}
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
			return nil
		}
		for lineNo, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
			if strings.Contains(line, in.Pattern) {
				matches = append(matches, map[string]any{"path": path, "line": lineNo + 1, "text": line})
				if len(matches) >= in.MaxResults {
					break
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	truncated := len(matches) >= in.MaxResults
	for len(matches) > 0 {
		candidate, _ := json.Marshal(map[string]any{"pattern": in.Pattern, "matches": matches, "truncated": truncated})
		if len(candidate) <= in.MaxOutputBytes {
			break
		}
		matches = matches[:len(matches)-1]
		truncated = true
	}
	return ResultFromValue(map[string]any{"pattern": in.Pattern, "matches": matches, "truncated": truncated})
}

// matchPathGlob implements the recursive ** segment used by Hermes-style file
// searches while retaining path.Match semantics for ordinary glob segments.
func matchPathGlob(pattern, candidate string) (bool, error) {
	patternParts := strings.Split(strings.Trim(filepath.ToSlash(pattern), "/"), "/")
	for _, segment := range patternParts {
		if segment == "**" {
			continue
		}
		if _, err := pathpkg.Match(segment, ""); err != nil {
			return false, err
		}
	}
	candidateParts := []string{}
	if normalized := strings.Trim(filepath.ToSlash(candidate), "/"); normalized != "" {
		candidateParts = strings.Split(normalized, "/")
	}
	type state struct{ pattern, candidate int }
	memo := make(map[state]bool)
	visited := make(map[state]bool)
	var match func(int, int) (bool, error)
	match = func(patternIndex, candidateIndex int) (bool, error) {
		key := state{pattern: patternIndex, candidate: candidateIndex}
		if visited[key] {
			return memo[key], nil
		}
		visited[key] = true
		if patternIndex == len(patternParts) {
			memo[key] = candidateIndex == len(candidateParts)
			return memo[key], nil
		}
		if patternParts[patternIndex] == "**" {
			if matched, err := match(patternIndex+1, candidateIndex); err != nil || matched {
				memo[key] = matched
				return matched, err
			}
			if candidateIndex < len(candidateParts) {
				matched, err := match(patternIndex, candidateIndex+1)
				memo[key] = matched
				return matched, err
			}
			return false, nil
		}
		if candidateIndex >= len(candidateParts) {
			return false, nil
		}
		segmentMatch, err := pathpkg.Match(patternParts[patternIndex], candidateParts[candidateIndex])
		if err != nil || !segmentMatch {
			return false, err
		}
		matched, err := match(patternIndex+1, candidateIndex+1)
		memo[key] = matched
		return matched, err
	}
	return match(0, 0)
}

func (t FileTool) list(_ context.Context, rc ToolRunContext, in listFilesInput) (*ToolResult, error) {
	root := rc.Workspace.WorkspacePath
	var err error
	if in.Path != "" {
		root, err = workspacePath(rc, in.Path)
		if err != nil {
			return nil, err
		}
	}
	if in.MaxResults <= 0 || in.MaxResults > 1000 {
		in.MaxResults = 200
	}
	var paths []string
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() && path != root && (entry.Name() == ".git" || entry.Name() == ".agentcanvas" || entry.Name() == ".worktrees") {
			return filepath.SkipDir
		}
		if !entry.IsDir() && len(paths) < in.MaxResults {
			if gitinfra.IsSensitivePath(rc.Workspace.WorkspacePath, path) {
				return nil
			}
			rel, _ := filepath.Rel(rc.Workspace.WorkspacePath, path)
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(paths)
	truncated := len(paths) >= in.MaxResults
	outputLimit := t.MaxOutputBytes
	if outputLimit <= 0 {
		outputLimit = 256 * 1024
	}
	for len(paths) > 0 {
		candidate, _ := json.Marshal(map[string]any{"files": paths, "truncated": truncated})
		if len(candidate) <= outputLimit {
			break
		}
		paths = paths[:len(paths)-1]
		truncated = true
	}
	return ResultFromValue(map[string]any{"files": paths, "truncated": truncated})
}

func (t FileTool) write(ctx context.Context, rc ToolRunContext, in writeFileInput) (*ToolResult, error) {
	path, err := workspacePath(rc, in.Path)
	if err != nil {
		return nil, err
	}
	release, err := acquirePathLock(rc.Workspace.WorkspacePath, path)
	if err != nil {
		return nil, err
	}
	defer release()
	old, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return nil, readErr
	}
	if readErr == nil && !utf8.Valid(old) {
		return nil, errors.New("file is not valid UTF-8")
	}
	if readErr == nil && rc.Workspace.Kind == "shared" && strings.TrimSpace(in.ExpectedSHA256) == "" {
		return nil, errors.New("expected_sha256 is required when overwriting a file in a shared workspace")
	}
	if in.ExpectedSHA256 != "" && (readErr != nil || !strings.EqualFold(in.ExpectedSHA256, fileHash(old))) {
		return nil, fmt.Errorf("file changed since expected_sha256")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := atomicWriteFile(path, []byte(in.Content), mode); err != nil {
		return nil, err
	}
	beforeHash := ""
	if readErr == nil {
		beforeHash = fileHash(old)
	}
	afterHash := fileHash([]byte(in.Content))
	value := map[string]any{"path": path, "created": errors.Is(readErr, os.ErrNotExist), "before_sha256": beforeHash, "after_sha256": afterHash, "bytes": len(in.Content), "diff": map[string]any{"kind": "file_write", "changed": beforeHash != afterHash}}
	emitWorkspaceMutation(ctx, rc, value)
	return ResultFromValue(value)
}
func (t FileTool) patch(ctx context.Context, rc ToolRunContext, in patchFileInput) (*ToolResult, error) {
	mode := strings.TrimSpace(in.Mode)
	if mode == "" {
		mode = "replace"
	}
	if mode == "patch" {
		return t.patchV4A(ctx, rc, in)
	}
	if mode != "replace" {
		return nil, fmt.Errorf("unknown patch mode %q", mode)
	}
	return t.patchReplace(ctx, rc, in)
}

func (t FileTool) patchReplace(ctx context.Context, rc ToolRunContext, in patchFileInput) (*ToolResult, error) {
	if strings.TrimSpace(in.Path) == "" {
		return nil, errors.New("path is required for replace mode")
	}
	path, err := workspacePath(rc, in.Path)
	if err != nil {
		return nil, err
	}
	release, err := acquirePathLock(rc.Workspace.WorkspacePath, path)
	if err != nil {
		return nil, err
	}
	defer release()
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(old) {
		return nil, errors.New("file is not valid UTF-8")
	}
	if rc.Workspace.Kind == "shared" && strings.TrimSpace(in.ExpectedSHA256) == "" {
		return nil, errors.New("expected_sha256 is required when patching a file in a shared workspace")
	}
	if in.ExpectedSHA256 != "" && !strings.EqualFold(in.ExpectedSHA256, fileHash(old)) {
		return nil, fmt.Errorf("file changed since expected_sha256")
	}
	patched, count, strategy, err := fuzzyFindAndReplace(string(old), in.OldString, in.NewString, in.ReplaceAll)
	if err != nil {
		if patchAlreadyApplied(string(old), in.OldString, in.NewString) {
			value := map[string]any{
				"path": path, "replacements": 0, "already_applied": true,
				"before_sha256": fileHash(old), "after_sha256": fileHash(old),
				"diff": map[string]any{"kind": "patch", "strategy": "already_applied"},
			}
			return ResultFromValue(value)
		}
		return nil, fmt.Errorf("%w; read the file before retrying", err)
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := atomicWriteFile(path, []byte(patched), mode); err != nil {
		return nil, err
	}
	value := map[string]any{
		"path": path, "replacements": count, "strategy": strategy,
		"before_sha256": fileHash(old), "after_sha256": fileHash([]byte(patched)),
		"diff": map[string]any{"kind": "patch", "replacements": count, "strategy": strategy},
	}
	emitWorkspaceMutation(ctx, rc, value)
	return ResultFromValue(value)
}

type textMatch struct {
	start int
	end   int
}

type fuzzyStrategy struct {
	name        string
	approximate bool
	find        func(string, string) []textMatch
}

var hermesUnicodeMap = map[rune]string{
	'\u201c': `"`, '\u201d': `"`, '\u2018': "'", '\u2019': "'", '\u2014': "--", '\u2013': "-",
	'\u2026': "...", '\u00a0': " ", '\u2212': "-", '\u2000': " ", '\u2001': " ", '\u2002': " ",
	'\u2003': " ", '\u2004': " ", '\u2005': " ", '\u2006': " ", '\u2007': " ", '\u2008': " ",
	'\u2009': " ", '\u200a': " ", '\u202f': " ", '\u205f': " ", '\u3000': " ",
}

func fuzzyFindAndReplace(content, oldString, newString string, replaceAll bool) (string, int, string, error) {
	if oldString == "" {
		return content, 0, "", errors.New("old_string cannot be empty")
	}
	if strings.TrimSpace(oldString) == "" {
		return content, 0, "", errors.New("old_string is only whitespace; provide non-blank text to match")
	}
	if oldString == newString {
		return content, 0, "", errors.New("old_string and new_string are identical")
	}
	strategies := []fuzzyStrategy{
		{name: "exact", find: exactMatches},
		{name: "line_trimmed", find: lineTrimmedMatches},
		{name: "whitespace_normalized", find: whitespaceNormalizedMatches},
		{name: "indentation_flexible", find: indentationFlexibleMatches},
		{name: "escape_normalized", find: escapeNormalizedMatches},
		{name: "trimmed_boundary", find: trimmedBoundaryMatches},
		{name: "unicode_normalized", find: unicodeNormalizedMatches},
		{name: "block_anchor", approximate: true, find: blockAnchorMatches},
		{name: "context_aware", approximate: true, find: contextAwareMatches},
	}
	for _, strategy := range strategies {
		matches := strategy.find(content, oldString)
		if len(matches) == 0 {
			continue
		}
		if len(matches) > 1 && !replaceAll {
			return content, 0, "", fmt.Errorf("found %d matches for old_string; provide more context or use replace_all: %s", len(matches), formatMatchLocations(content, matches))
		}
		if len(matches) > 1 && replaceAll && strategy.approximate {
			return content, 0, "", fmt.Errorf("found %d approximate matches via %s; replace_all requires a precise strategy", len(matches), strategy.name)
		}
		if strategy.name != "exact" {
			if err := detectEscapeDrift(content, matches, oldString, newString); err != nil {
				return content, 0, "", err
			}
		}
		effectiveNew := maybeUnescapeReplacement(newString, content, matches)
		if strategy.name == "unicode_normalized" && len(matches) == 1 {
			effectiveNew = preserveUnicodeReplacement(content[matches[0].start:matches[0].end], oldString, effectiveNew)
		}
		return applyTextMatches(content, matches, effectiveNew, oldString, strategy.name != "exact"), len(matches), strategy.name, nil
	}
	return content, 0, "", errors.New("could not find a match for old_string in the file")
}

func exactMatches(content, pattern string) []textMatch {
	if pattern == "" {
		return nil
	}
	var matches []textMatch
	for offset := 0; offset <= len(content)-len(pattern); {
		index := strings.Index(content[offset:], pattern)
		if index < 0 {
			break
		}
		start := offset + index
		matches = append(matches, textMatch{start: start, end: start + len(pattern)})
		offset = start + len(pattern)
	}
	return matches
}

func lineTrimmedMatches(content, pattern string) []textMatch {
	return normalizedLineMatches(content, pattern, func(line string) string { return strings.TrimSpace(line) })
}

func whitespaceNormalizedMatches(content, pattern string) []textMatch {
	return normalizedLineMatches(content, pattern, collapseHorizontalWhitespace)
}

func indentationFlexibleMatches(content, pattern string) []textMatch {
	return normalizedLineMatches(content, pattern, func(line string) string { return strings.TrimLeft(line, " \t") })
}

func escapeNormalizedMatches(content, pattern string) []textMatch {
	unescaped := strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\r`, "\r").Replace(pattern)
	if unescaped == pattern {
		return nil
	}
	return exactMatches(content, unescaped)
}

func trimmedBoundaryMatches(content, pattern string) []textMatch {
	patternLines := strings.Split(pattern, "\n")
	if len(patternLines) == 0 {
		return nil
	}
	patternLines[0] = strings.TrimSpace(patternLines[0])
	if len(patternLines) > 1 {
		patternLines[len(patternLines)-1] = strings.TrimSpace(patternLines[len(patternLines)-1])
	}
	contentLines := strings.Split(content, "\n")
	var matches []textMatch
	for start := 0; start+len(patternLines) <= len(contentLines); start++ {
		candidate := append([]string(nil), contentLines[start:start+len(patternLines)]...)
		candidate[0] = strings.TrimSpace(candidate[0])
		if len(candidate) > 1 {
			candidate[len(candidate)-1] = strings.TrimSpace(candidate[len(candidate)-1])
		}
		if strings.Join(candidate, "\n") == strings.Join(patternLines, "\n") {
			matches = append(matches, lineMatchSpan(content, contentLines, start, len(patternLines)))
		}
	}
	return matches
}

func unicodeNormalizedMatches(content, pattern string) []textMatch {
	normalizedContent, boundaries := normalizeUnicodeWithBoundaries(content)
	normalizedPattern := normalizeUnicode(pattern)
	if normalizedContent == content && normalizedPattern == pattern {
		return nil
	}
	normalizedMatches := exactMatches(normalizedContent, normalizedPattern)
	if len(normalizedMatches) == 0 {
		return normalizedLineMatches(content, pattern, func(line string) string { return strings.TrimSpace(normalizeUnicode(line)) })
	}
	matches := make([]textMatch, 0, len(normalizedMatches))
	for _, match := range normalizedMatches {
		if match.start < 0 || match.end >= len(boundaries) {
			continue
		}
		mapped := textMatch{start: boundaries[match.start], end: boundaries[match.end]}
		if mapped.end > mapped.start {
			matches = append(matches, mapped)
		}
	}
	return matches
}

func blockAnchorMatches(content, pattern string) []textMatch {
	patternLines := strings.Split(normalizeUnicode(pattern), "\n")
	if len(patternLines) < 2 {
		return nil
	}
	normalizedLines := strings.Split(normalizeUnicode(content), "\n")
	originalLines := strings.Split(content, "\n")
	first, last := strings.TrimSpace(patternLines[0]), strings.TrimSpace(patternLines[len(patternLines)-1])
	var candidates []int
	for start := 0; start+len(patternLines) <= len(normalizedLines); start++ {
		if strings.TrimSpace(normalizedLines[start]) == first && strings.TrimSpace(normalizedLines[start+len(patternLines)-1]) == last {
			candidates = append(candidates, start)
		}
	}
	threshold := 0.5
	if len(candidates) > 1 {
		threshold = 0.7
	}
	var matches []textMatch
	for _, start := range candidates {
		ratio := 1.0
		if len(patternLines) > 2 {
			ratio = sequenceSimilarity(strings.Join(normalizedLines[start+1:start+len(patternLines)-1], "\n"), strings.Join(patternLines[1:len(patternLines)-1], "\n"))
		}
		if ratio >= threshold {
			matches = append(matches, lineMatchSpan(content, originalLines, start, len(patternLines)))
		}
	}
	return matches
}

func contextAwareMatches(content, pattern string) []textMatch {
	patternLines := strings.Split(pattern, "\n")
	contentLines := strings.Split(content, "\n")
	if len(patternLines) == 0 || len(patternLines) > len(contentLines) {
		return nil
	}
	var matches []textMatch
	for start := 0; start+len(patternLines) <= len(contentLines); start++ {
		block := contentLines[start : start+len(patternLines)]
		if sequenceSimilarity(strings.TrimSpace(patternLines[0]), strings.TrimSpace(block[0])) < 0.8 ||
			sequenceSimilarity(strings.TrimSpace(patternLines[len(patternLines)-1]), strings.TrimSpace(block[len(block)-1])) < 0.8 {
			continue
		}
		valid := true
		for index, patternLine := range patternLines {
			if strings.TrimSpace(patternLine) == "" {
				continue
			}
			if sequenceSimilarity(strings.TrimSpace(patternLine), strings.TrimSpace(block[index])) < 0.8 {
				valid = false
				break
			}
		}
		if valid {
			matches = append(matches, lineMatchSpan(content, contentLines, start, len(patternLines)))
		}
	}
	return matches
}

func normalizedLineMatches(content, pattern string, normalize func(string) string) []textMatch {
	contentLines := strings.Split(content, "\n")
	patternLines := strings.Split(pattern, "\n")
	normalizedPattern := make([]string, len(patternLines))
	for index, line := range patternLines {
		normalizedPattern[index] = normalize(line)
	}
	var matches []textMatch
	for start := 0; start+len(patternLines) <= len(contentLines); start++ {
		matched := true
		for index := range patternLines {
			if normalize(contentLines[start+index]) != normalizedPattern[index] {
				matched = false
				break
			}
		}
		if matched {
			matches = append(matches, lineMatchSpan(content, contentLines, start, len(patternLines)))
		}
	}
	return matches
}

func lineMatchSpan(content string, lines []string, start, count int) textMatch {
	startByte := 0
	for index := 0; index < start; index++ {
		startByte += len(lines[index]) + 1
	}
	endByte := startByte
	for index := start; index < start+count; index++ {
		endByte += len(lines[index])
		if index+1 < start+count {
			endByte++
		}
	}
	if endByte > len(content) {
		endByte = len(content)
	}
	return textMatch{start: startByte, end: endByte}
}

func collapseHorizontalWhitespace(value string) string {
	var builder strings.Builder
	inWhitespace := false
	for _, char := range value {
		if char == ' ' || char == '\t' {
			if !inWhitespace {
				builder.WriteByte(' ')
				inWhitespace = true
			}
			continue
		}
		inWhitespace = false
		builder.WriteRune(char)
	}
	return builder.String()
}

func normalizeUnicode(value string) string {
	var builder strings.Builder
	for _, char := range value {
		if replacement, ok := hermesUnicodeMap[char]; ok {
			builder.WriteString(replacement)
		} else {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

func normalizeUnicodeWithBoundaries(value string) (string, []int) {
	var builder strings.Builder
	boundaries := make([]int, 0, len(value)+1)
	for originalByte, char := range value {
		replacement, ok := hermesUnicodeMap[char]
		if !ok {
			replacement = string(char)
		}
		for range []byte(replacement) {
			boundaries = append(boundaries, originalByte)
		}
		builder.WriteString(replacement)
	}
	boundaries = append(boundaries, len(value))
	return builder.String(), boundaries
}

func sequenceSimilarity(left, right string) float64 {
	if left == right {
		return 1
	}
	a, b := []rune(left), []rune(right)
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	if len(a)*len(b) > 2_000_000 {
		prefix := 0
		for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
			prefix++
		}
		suffix := 0
		for suffix+prefix < len(a) && suffix+prefix < len(b) && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
			suffix++
		}
		return float64(2*(prefix+suffix)) / float64(len(a)+len(b))
	}
	if len(b) > len(a) {
		a, b = b, a
	}
	row := make([]int, len(b)+1)
	for _, leftRune := range a {
		previous := 0
		for index, rightRune := range b {
			old := row[index+1]
			if leftRune == rightRune {
				row[index+1] = previous + 1
			} else if row[index] > row[index+1] {
				row[index+1] = row[index]
			}
			previous = old
		}
	}
	return float64(2*row[len(b)]) / float64(len(a)+len(b))
}

func applyTextMatches(content string, matches []textMatch, replacement, oldString string, reindent bool) string {
	sorted := append([]textMatch(nil), matches...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].start > sorted[j].start })
	result := content
	for _, match := range sorted {
		adjusted := replacement
		if reindent {
			adjusted = reindentReplacement(content[match.start:match.end], oldString, replacement)
		}
		result = result[:match.start] + adjusted + result[match.end:]
	}
	return result
}

func reindentReplacement(fileRegion, oldString, newString string) string {
	if newString == "" {
		return newString
	}
	oldIndent, oldOK := firstMeaningfulIndent(oldString)
	fileIndent, fileOK := firstMeaningfulIndent(fileRegion)
	if !oldOK || !fileOK || oldIndent == fileIndent {
		return newString
	}
	lines := strings.Split(newString, "\n")
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, oldIndent) {
			lines[index] = fileIndent + strings.TrimPrefix(line, oldIndent)
		} else {
			lines[index] = fileIndent + strings.TrimLeft(line, " \t")
		}
	}
	return strings.Join(lines, "\n")
}

func firstMeaningfulIndent(value string) (string, bool) {
	for _, line := range strings.Split(value, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		return line[:len(line)-len(strings.TrimLeft(line, " \t"))], true
	}
	return "", false
}

func detectEscapeDrift(content string, matches []textMatch, oldString, newString string) error {
	for _, suspect := range []string{`\'`, `\"`} {
		if !strings.Contains(newString, suspect) || !strings.Contains(oldString, suspect) {
			continue
		}
		present := false
		for _, match := range matches {
			if strings.Contains(content[match.start:match.end], suspect) {
				present = true
				break
			}
		}
		if !present {
			return fmt.Errorf("escape-drift detected for %q; re-read the file and remove spurious backslashes", suspect)
		}
	}
	return nil
}

func maybeUnescapeReplacement(replacement, content string, matches []textMatch) string {
	var regions strings.Builder
	for _, match := range matches {
		regions.WriteString(content[match.start:match.end])
	}
	result := replacement
	if strings.Contains(regions.String(), "\t") {
		result = strings.ReplaceAll(result, `\t`, "\t")
	}
	if strings.Contains(regions.String(), "\r") {
		result = strings.ReplaceAll(result, `\r`, "\r")
	}
	return result
}

func preserveUnicodeReplacement(fileRegion, oldString, newString string) string {
	normalizedRegion, boundaries := normalizeUnicodeWithBoundaries(fileRegion)
	normalizedOld := normalizeUnicode(oldString)
	if normalizedRegion != normalizedOld {
		return newString
	}
	prefix := 0
	for prefix < len(normalizedOld) && prefix < len(newString) && normalizedOld[prefix] == newString[prefix] {
		prefix++
	}
	suffix := 0
	for suffix+prefix < len(normalizedOld) && suffix+prefix < len(newString) && normalizedOld[len(normalizedOld)-1-suffix] == newString[len(newString)-1-suffix] {
		suffix++
	}
	if prefix >= len(boundaries) || len(normalizedRegion)-suffix >= len(boundaries) {
		return newString
	}
	return fileRegion[:boundaries[prefix]] + newString[prefix:len(newString)-suffix] + fileRegion[boundaries[len(normalizedRegion)-suffix]:]
}

func patchAlreadyApplied(content, oldString, newString string) bool {
	if len(strings.TrimSpace(newString)) < 8 || !strings.Contains(content, newString) {
		return false
	}
	return oldString == newString || !strings.Contains(content, oldString)
}

func formatMatchLocations(content string, matches []textMatch) string {
	limit := len(matches)
	if limit > 5 {
		limit = 5
	}
	rows := make([]string, 0, limit)
	for _, match := range matches[:limit] {
		line := strings.Count(content[:match.start], "\n") + 1
		rows = append(rows, fmt.Sprintf("L%d", line))
	}
	return strings.Join(rows, ", ")
}

type v4aOperationKind string

const (
	v4aUpdate v4aOperationKind = "update"
	v4aAdd    v4aOperationKind = "add"
	v4aDelete v4aOperationKind = "delete"
	v4aMove   v4aOperationKind = "move"
)

type v4aHunkLine struct {
	prefix  byte
	content string
}

type v4aHunk struct {
	contextHint string
	lines       []v4aHunkLine
}

type v4aOperation struct {
	kind       v4aOperationKind
	rawPath    string
	rawNewPath string
	path       string
	newPath    string
	hunks      []v4aHunk
}

type v4aFileState struct {
	exists          bool
	content         string
	mode            os.FileMode
	originalExists  bool
	originalContent string
}

var (
	v4aBeginPattern  = regexp.MustCompile(`^\*\*\*\s*Begin\s+Patch\s*$`)
	v4aEndPattern    = regexp.MustCompile(`^\*\*\*\s*End\s+Patch\s*$`)
	v4aUpdatePattern = regexp.MustCompile(`^\*\*\*\s*Update\s+File:\s*(.+)$`)
	v4aAddPattern    = regexp.MustCompile(`^\*\*\*\s*Add\s+File:\s*(.+)$`)
	v4aDeletePattern = regexp.MustCompile(`^\*\*\*\s*Delete\s+File:\s*(.+)$`)
	v4aMovePattern   = regexp.MustCompile(`^\*\*\*\s*Move\s+File:\s*(.+?)\s*->\s*(.+)$`)
	v4aHintPattern   = regexp.MustCompile(`^@@\s*(.*?)\s*@@`)
)

func (t FileTool) patchV4A(ctx context.Context, rc ToolRunContext, in patchFileInput) (*ToolResult, error) {
	if strings.TrimSpace(in.Patch) == "" {
		return nil, errors.New("patch content is required for patch mode")
	}
	operations, err := parseV4APatch(in.Patch)
	if err != nil {
		return nil, err
	}
	allPaths := make(map[string]struct{})
	for index := range operations {
		operation := &operations[index]
		operation.path, err = workspacePath(rc, operation.rawPath)
		if err != nil {
			return nil, fmt.Errorf("unsafe V4A path %q: %w", operation.rawPath, err)
		}
		allPaths[operation.path] = struct{}{}
		if operation.kind == v4aMove {
			operation.newPath, err = workspacePath(rc, operation.rawNewPath)
			if err != nil {
				return nil, fmt.Errorf("unsafe V4A move destination %q: %w", operation.rawNewPath, err)
			}
			if operation.path == operation.newPath {
				return nil, errors.New("V4A move source and destination are the same path")
			}
			allPaths[operation.newPath] = struct{}{}
		}
	}

	paths := make([]string, 0, len(allPaths))
	for path := range allPaths {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	releases := make([]func(), 0, len(paths))
	for _, path := range paths {
		release, lockErr := acquirePathLock(rc.Workspace.WorkspacePath, path)
		if lockErr != nil {
			for index := len(releases) - 1; index >= 0; index-- {
				releases[index]()
			}
			return nil, lockErr
		}
		releases = append(releases, release)
	}
	defer func() {
		for index := len(releases) - 1; index >= 0; index-- {
			releases[index]()
		}
	}()

	states, err := simulateV4AOperations(operations)
	if err != nil {
		return nil, fmt.Errorf("patch validation failed (no files were modified): %w", err)
	}
	if err := validateV4AExpectedHashes(rc, in, states); err != nil {
		return nil, fmt.Errorf("patch version validation failed (no files were modified): %w", err)
	}
	if err := applyV4AStates(states); err != nil {
		return nil, fmt.Errorf("patch apply failed: %w", err)
	}

	created := make([]string, 0)
	modified := make([]string, 0)
	deleted := make([]string, 0)
	diffFiles := make([]map[string]any, 0)
	statePaths := make([]string, 0, len(states))
	for path := range states {
		statePaths = append(statePaths, path)
	}
	sort.Strings(statePaths)
	for _, path := range statePaths {
		state := states[path]
		if state.originalExists == state.exists && state.originalContent == state.content {
			continue
		}
		kind := "modified"
		switch {
		case !state.originalExists && state.exists:
			kind = "created"
			created = append(created, path)
		case state.originalExists && !state.exists:
			kind = "deleted"
			deleted = append(deleted, path)
		default:
			modified = append(modified, path)
		}
		beforeHash, afterHash := "", ""
		if state.originalExists {
			beforeHash = fileHash([]byte(state.originalContent))
		}
		if state.exists {
			afterHash = fileHash([]byte(state.content))
		}
		diffFiles = append(diffFiles, map[string]any{
			"path": path, "kind": kind, "before_sha256": beforeHash, "after_sha256": afterHash,
		})
	}
	value := map[string]any{
		"mode": "patch", "files_modified": modified, "files_created": created, "files_deleted": deleted,
		"diff": map[string]any{"kind": "v4a", "files": diffFiles},
	}
	emitWorkspaceMutation(ctx, rc, value)
	return ResultFromValue(value)
}

func validateV4AExpectedHashes(rc ToolRunContext, in patchFileInput, states map[string]*v4aFileState) error {
	expected := make(map[string]string, len(in.ExpectedSHA256ByPath)+1)
	for rawPath, hash := range in.ExpectedSHA256ByPath {
		path, err := workspacePath(rc, rawPath)
		if err != nil {
			return fmt.Errorf("unsafe expected_sha256_by_path entry %q: %w", rawPath, err)
		}
		if strings.TrimSpace(hash) == "" {
			return fmt.Errorf("expected SHA-256 is empty for %q", rawPath)
		}
		if _, ok := states[path]; !ok {
			return fmt.Errorf("expected SHA-256 path %q is not modified by the patch", rawPath)
		}
		expected[path] = hash
	}
	changedExisting := make([]string, 0)
	for path, state := range states {
		if state.originalExists && (state.originalExists != state.exists || state.originalContent != state.content) {
			changedExisting = append(changedExisting, path)
		}
	}
	if strings.TrimSpace(in.ExpectedSHA256) != "" {
		if len(changedExisting) != 1 {
			return errors.New("expected_sha256 requires exactly one modified existing file; use expected_sha256_by_path for multi-file patches")
		}
		expected[changedExisting[0]] = in.ExpectedSHA256
	}
	for _, path := range changedExisting {
		hash, ok := expected[path]
		if rc.Workspace.Kind == "shared" && !ok {
			relative, _ := filepath.Rel(rc.Workspace.WorkspacePath, path)
			return fmt.Errorf("expected SHA-256 is required for shared workspace file %q", filepath.ToSlash(relative))
		}
		if ok && !strings.EqualFold(strings.TrimSpace(hash), fileHash([]byte(states[path].originalContent))) {
			return fmt.Errorf("file %q changed since expected SHA-256", path)
		}
	}
	return nil
}

func parseV4APatch(patchContent string) ([]v4aOperation, error) {
	lines := strings.Split(strings.ReplaceAll(patchContent, "\r\n", "\n"), "\n")
	for index := range lines {
		lines[index] = strings.TrimSuffix(lines[index], "\r")
	}
	start, end := -1, len(lines)
	for index, line := range lines {
		if v4aBeginPattern.MatchString(line) {
			start = index
		} else if v4aEndPattern.MatchString(line) {
			end = index
			break
		}
	}
	var operations []v4aOperation
	var current *v4aOperation
	var currentHunk *v4aHunk
	finishCurrent := func() {
		if current == nil {
			return
		}
		if currentHunk != nil && len(currentHunk.lines) > 0 {
			current.hunks = append(current.hunks, *currentHunk)
		}
		operations = append(operations, *current)
		current = nil
		currentHunk = nil
	}
	for index := start + 1; index < end; index++ {
		line := lines[index]
		switch {
		case v4aUpdatePattern.MatchString(line):
			finishCurrent()
			match := v4aUpdatePattern.FindStringSubmatch(line)
			current = &v4aOperation{kind: v4aUpdate, rawPath: strings.TrimSpace(match[1])}
		case v4aAddPattern.MatchString(line):
			finishCurrent()
			match := v4aAddPattern.FindStringSubmatch(line)
			current = &v4aOperation{kind: v4aAdd, rawPath: strings.TrimSpace(match[1])}
			currentHunk = &v4aHunk{}
		case v4aDeletePattern.MatchString(line):
			finishCurrent()
			match := v4aDeletePattern.FindStringSubmatch(line)
			operations = append(operations, v4aOperation{kind: v4aDelete, rawPath: strings.TrimSpace(match[1])})
		case v4aMovePattern.MatchString(line):
			finishCurrent()
			match := v4aMovePattern.FindStringSubmatch(line)
			operations = append(operations, v4aOperation{kind: v4aMove, rawPath: strings.TrimSpace(match[1]), rawNewPath: strings.TrimSpace(match[2])})
		case strings.HasPrefix(line, "@@"):
			if current == nil {
				continue
			}
			if currentHunk != nil && len(currentHunk.lines) > 0 {
				current.hunks = append(current.hunks, *currentHunk)
			}
			currentHunk = &v4aHunk{}
			if match := v4aHintPattern.FindStringSubmatch(line); len(match) > 1 {
				currentHunk.contextHint = strings.TrimSpace(match[1])
			}
		case current != nil && line != "":
			if currentHunk == nil {
				currentHunk = &v4aHunk{}
			}
			prefix, content := byte(' '), line
			if line[0] == '+' || line[0] == '-' || line[0] == ' ' {
				prefix, content = line[0], line[1:]
			} else if line[0] == '\\' {
				continue
			}
			currentHunk.lines = append(currentHunk.lines, v4aHunkLine{prefix: prefix, content: content})
		}
	}
	finishCurrent()
	if len(operations) == 0 {
		return nil, errors.New("patch contains no file operations")
	}
	for _, operation := range operations {
		if operation.rawPath == "" {
			return nil, errors.New("patch operation has an empty path")
		}
		if operation.kind == v4aUpdate && len(operation.hunks) == 0 {
			return nil, fmt.Errorf("update %q contains no hunks", operation.rawPath)
		}
		if operation.kind == v4aMove && operation.rawNewPath == "" {
			return nil, fmt.Errorf("move %q has no destination", operation.rawPath)
		}
	}
	return operations, nil
}

func simulateV4AOperations(operations []v4aOperation) (map[string]*v4aFileState, error) {
	states := make(map[string]*v4aFileState)
	load := func(path string) (*v4aFileState, error) {
		if state, ok := states[path]; ok {
			return state, nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				state := &v4aFileState{mode: 0o644}
				states[path] = state
				return state, nil
			}
			return nil, err
		}
		if !utf8.Valid(data) {
			return nil, fmt.Errorf("%s is not valid UTF-8", path)
		}
		mode := os.FileMode(0o644)
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
		state := &v4aFileState{exists: true, content: string(data), mode: mode, originalExists: true, originalContent: string(data)}
		states[path] = state
		return state, nil
	}
	realChanges := 0
	for _, operation := range operations {
		switch operation.kind {
		case v4aAdd:
			state, err := load(operation.path)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", operation.rawPath, err)
			}
			if state.exists {
				return nil, fmt.Errorf("%s: file already exists", operation.rawPath)
			}
			var lines []string
			for _, hunk := range operation.hunks {
				for _, line := range hunk.lines {
					if line.prefix == '+' {
						lines = append(lines, line.content)
					}
				}
			}
			state.exists = true
			state.content = strings.Join(lines, "\n")
			realChanges++
		case v4aDelete:
			state, err := load(operation.path)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", operation.rawPath, err)
			}
			if !state.exists {
				return nil, fmt.Errorf("%s: file not found for deletion", operation.rawPath)
			}
			state.exists = false
			state.content = ""
			realChanges++
		case v4aMove:
			source, err := load(operation.path)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", operation.rawPath, err)
			}
			destination, err := load(operation.newPath)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", operation.rawNewPath, err)
			}
			if !source.exists {
				return nil, fmt.Errorf("%s: source file not found for move", operation.rawPath)
			}
			if destination.exists {
				return nil, fmt.Errorf("%s: move destination already exists", operation.rawNewPath)
			}
			destination.exists, destination.content, destination.mode = true, source.content, source.mode
			source.exists, source.content = false, ""
			realChanges++
		case v4aUpdate:
			state, err := load(operation.path)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", operation.rawPath, err)
			}
			if !state.exists {
				return nil, fmt.Errorf("%s: file not found for update", operation.rawPath)
			}
			updated, changes, err := applyV4AHunks(state.content, operation.hunks)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", operation.rawPath, err)
			}
			state.content = updated
			realChanges += changes
		}
	}
	if realChanges == 0 {
		return nil, errors.New("patch contains no changes")
	}
	return states, nil
}

func applyV4AHunks(content string, hunks []v4aHunk) (string, int, error) {
	changes := 0
	result := content
	for index, hunk := range hunks {
		searchLines := make([]string, 0)
		replaceLines := make([]string, 0)
		removed, added := 0, 0
		for _, line := range hunk.lines {
			switch line.prefix {
			case ' ':
				searchLines = append(searchLines, line.content)
				replaceLines = append(replaceLines, line.content)
			case '-':
				searchLines = append(searchLines, line.content)
				removed++
			case '+':
				replaceLines = append(replaceLines, line.content)
				added++
			}
		}
		if removed == 0 && added == 0 {
			continue
		}
		changes++
		if len(searchLines) == 0 {
			insertText := strings.Join(replaceLines, "\n")
			var err error
			result, err = insertV4AText(result, insertText, hunk.contextHint)
			if err != nil {
				return content, 0, fmt.Errorf("hunk %d: %w", index+1, err)
			}
			continue
		}
		searchPattern := strings.Join(searchLines, "\n")
		replacement := strings.Join(replaceLines, "\n")
		if searchPattern == replacement {
			continue
		}
		updated, count, _, err := fuzzyFindAndReplace(result, searchPattern, replacement, false)
		if err != nil {
			if patchAlreadyApplied(result, searchPattern, replacement) {
				continue
			}
			return content, 0, fmt.Errorf("hunk %d %q could not be applied: %w", index+1, hunk.contextHint, err)
		}
		if count == 0 {
			return content, 0, fmt.Errorf("hunk %d %q made no change", index+1, hunk.contextHint)
		}
		result = updated
	}
	return result, changes, nil
}

func insertV4AText(content, insertion, contextHint string) (string, error) {
	if contextHint == "" {
		return strings.TrimRight(content, "\n") + "\n" + insertion + "\n", nil
	}
	count := strings.Count(content, contextHint)
	if count == 0 {
		return content, fmt.Errorf("addition-only context hint %q not found", contextHint)
	}
	if count > 1 {
		return content, fmt.Errorf("addition-only context hint %q is ambiguous (%d occurrences)", contextHint, count)
	}
	position := strings.Index(content, contextHint)
	endOfLine := strings.Index(content[position:], "\n")
	if endOfLine < 0 {
		return content + "\n" + insertion, nil
	}
	endOfLine += position
	return content[:endOfLine+1] + insertion + "\n" + content[endOfLine+1:], nil
}

func applyV4AStates(states map[string]*v4aFileState) error {
	paths := make([]string, 0, len(states))
	for path := range states {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		state := states[path]
		if !state.exists || state.originalExists && state.originalContent == state.content {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return rollbackV4AStates(states, err)
		}
		mode := state.mode
		if mode == 0 {
			mode = 0o644
		}
		if err := atomicWriteFile(path, []byte(state.content), mode); err != nil {
			return rollbackV4AStates(states, err)
		}
	}
	for _, path := range paths {
		state := states[path]
		if state.exists || !state.originalExists {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return rollbackV4AStates(states, err)
		}
	}
	return nil
}

func rollbackV4AStates(states map[string]*v4aFileState, applyErr error) error {
	paths := make([]string, 0, len(states))
	for path := range states {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var rollbackErrors []error
	for _, path := range paths {
		state := states[path]
		if !state.originalExists {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore directory for %s: %w", path, err))
			continue
		}
		mode := state.mode
		if mode == 0 {
			mode = 0o644
		}
		if err := atomicWriteFile(path, []byte(state.originalContent), mode); err != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restore %s: %w", path, err))
		}
	}
	for index := len(paths) - 1; index >= 0; index-- {
		path := paths[index]
		if states[path].originalExists {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("remove newly created %s: %w", path, err))
		}
	}
	if rollbackErr := errors.Join(rollbackErrors...); rollbackErr != nil {
		return fmt.Errorf("%w; rollback failed: %v", applyErr, rollbackErr)
	}
	return fmt.Errorf("%w; all file changes were rolled back", applyErr)
}
func (t FileTool) move(ctx context.Context, rc ToolRunContext, in moveFileInput) (*ToolResult, error) {
	from, err := workspacePath(rc, in.From)
	if err != nil {
		return nil, err
	}
	to, err := workspacePath(rc, in.To)
	if err != nil {
		return nil, err
	}
	if from == to {
		return nil, errors.New("source and destination are the same path")
	}
	firstPath, secondPath := from, to
	if secondPath < firstPath {
		firstPath, secondPath = secondPath, firstPath
	}
	releaseFirst, err := acquirePathLock(rc.Workspace.WorkspacePath, firstPath)
	if err != nil {
		return nil, err
	}
	defer releaseFirst()
	releaseSecond, err := acquirePathLock(rc.Workspace.WorkspacePath, secondPath)
	if err != nil {
		return nil, err
	}
	defer releaseSecond()
	if _, statErr := os.Lstat(to); statErr == nil {
		return nil, errors.New("destination already exists")
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, statErr
	}
	old, err := os.ReadFile(from)
	if err != nil {
		return nil, err
	}
	if rc.Workspace.Kind == "shared" && strings.TrimSpace(in.ExpectedSHA256) == "" {
		return nil, errors.New("expected_sha256 is required when moving a file in a shared workspace")
	}
	if in.ExpectedSHA256 != "" && !strings.EqualFold(in.ExpectedSHA256, fileHash(old)) {
		return nil, fmt.Errorf("file changed since expected_sha256")
	}
	if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return nil, err
	}
	if err := os.Rename(from, to); err != nil {
		return nil, err
	}
	hash := fileHash(old)
	value := map[string]any{"from": from, "to": to, "before_sha256": hash, "after_sha256": hash, "diff": map[string]any{"kind": "move"}}
	emitWorkspaceMutation(ctx, rc, value)
	return ResultFromValue(value)
}
func (t FileTool) delete(ctx context.Context, rc ToolRunContext, in deleteFileInput) (*ToolResult, error) {
	path, err := workspacePath(rc, in.Path)
	if err != nil {
		return nil, err
	}
	release, err := acquirePathLock(rc.Workspace.WorkspacePath, path)
	if err != nil {
		return nil, err
	}
	defer release()
	old, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if rc.Workspace.Kind == "shared" && strings.TrimSpace(in.ExpectedSHA256) == "" {
		return nil, errors.New("expected_sha256 is required when deleting a file in a shared workspace")
	}
	if in.ExpectedSHA256 != "" && !strings.EqualFold(in.ExpectedSHA256, fileHash(old)) {
		return nil, fmt.Errorf("file changed since expected_sha256")
	}
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	value := map[string]any{"path": path, "deleted": true, "before_sha256": fileHash(old), "after_sha256": "", "diff": map[string]any{"kind": "delete"}}
	emitWorkspaceMutation(ctx, rc, value)
	return ResultFromValue(value)
}

func emitWorkspaceMutation(ctx context.Context, rc ToolRunContext, payload map[string]any) {
	rc.Workspace.Dirty = true
	if rc.EmitEvent == nil {
		return
	}
	eventPayload := map[string]any{
		"workspace_id": rc.Workspace.ID, "project_id": rc.Workspace.ProjectID, "run_id": rc.RunID, "kind": rc.Workspace.Kind, "repo_root": rc.Workspace.RepositoryRoot,
		"path": rc.Workspace.WorkspacePath, "branch": rc.Workspace.BranchName, "base_sha": rc.Workspace.BaseSHA, "head_sha": rc.Workspace.HeadSHA,
		"dirty": rc.Workspace.Dirty, "unpushed": rc.Workspace.Unpushed, "status": "ready", "error": "", "mutation": payload,
	}
	_ = rc.EmitEvent(ctx, "workspace.status_changed", eventPayload)
}

type WorkspaceExecTool struct {
	MaxOutputBytes int
	Timeout        time.Duration
}

func (WorkspaceExecTool) Name() string { return "workspace_exec" }
func (WorkspaceExecTool) Description() string {
	return "Run a non-interactive shell command with cwd fixed to the active Agent workspace."
}
func (WorkspaceExecTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"timeout_ms":{"type":"number"}},"required":["command"],"additionalProperties":false}`)
}
func (WorkspaceExecTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskHigh, RequiresApproval: true, SideEffect: SideEffectExternalAction, TimeoutMS: 30000, MaxOutputBytes: 256 * 1024}
}
func (t WorkspaceExecTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if rc.Workspace == nil || !rc.Workspace.ExecEnabled {
		return nil, errors.New("workspace execution is disabled")
	}
	var in struct {
		Command   string `json:"command"`
		TimeoutMS int    `json:"timeout_ms"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Command) == "" {
		return nil, errors.New("command is required")
	}
	if err := validateWorkspaceCommand(in.Command, rc.Workspace.WorkspacePath); err != nil {
		return nil, err
	}
	timeout := t.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if in.TimeoutMS > 0 && time.Duration(in.TimeoutMS)*time.Millisecond < timeout {
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := execCommand(commandCtx, in.Command, rc.Workspace.WorkspacePath)
	output := &cappedCommandOutput{max: t.outputLimit()}
	cmd.Stdout, cmd.Stderr = output, output
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			exitCode = -1
		}
	}
	value := map[string]any{"command": in.Command, "cwd": rc.Workspace.WorkspacePath, "output": output.String(), "exit_code": exitCode, "truncated": output.truncated}
	if err != nil {
		value["error"] = err.Error()
	}
	result, marshalErr := ResultFromValue(value)
	if marshalErr != nil {
		return nil, marshalErr
	}
	result.IsError = err != nil
	return result, err
}

type cappedCommandOutput struct {
	mu        sync.Mutex
	data      []byte
	max       int
	truncated bool
}

func (w *cappedCommandOutput) Write(value []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	written := len(value)
	remaining := w.max - len(w.data)
	if remaining <= 0 {
		w.truncated = w.truncated || written > 0
		return written, nil
	}
	if len(value) > remaining {
		w.data = append(w.data, value[:remaining]...)
		w.truncated = true
		return written, nil
	}
	w.data = append(w.data, value...)
	return written, nil
}

func (w *cappedCommandOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(w.data)
}
func (t WorkspaceExecTool) outputLimit() int {
	if t.MaxOutputBytes <= 0 {
		return 256 * 1024
	}
	return t.MaxOutputBytes
}

func execCommand(ctx context.Context, command, cwd string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = cwd
	allowed := map[string]bool{
		"CI": true, "CLICOLOR": true, "CLICOLOR_FORCE": true, "COLORTERM": true,
		"DEVELOPER_DIR": true, "NO_COLOR": true, "PATH": true, "SDKROOT": true,
		"TERM": true, "TERM_PROGRAM": true, "TERM_PROGRAM_VERSION": true, "TZ": true,
	}
	environment := make([]string, 0, len(allowed)+8)
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		if allowed[name] || strings.HasPrefix(name, "LC_") {
			environment = append(environment, value)
		}
	}
	cmd.Env = append(environment, "HOME="+cwd, "PWD="+cwd, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "LC_ALL=en_US.UTF-8", "LANG=en_US.UTF-8")
	return cmd
}

var commandAbsolutePathPattern = regexp.MustCompile(`(?:^|[\s'"(=,:])(/[^\s'"();|&<>]*)`)
var commandExternalURLPattern = regexp.MustCompile(`(?:https?|ssh|git)://[^\s'"();|&<>]*`)

func validateWorkspaceCommand(command, workspace string) error {
	if strings.IndexByte(command, 0) >= 0 {
		return errors.New("command contains NUL")
	}
	lower := strings.ToLower(command)
	for _, forbidden := range []string{"cd ", "cd\t", "pushd ", "popd ", "git -c", "git -c ", "git --git-dir", "git --work-tree", " -c core.worktree", "../"} {
		if strings.Contains(lower, forbidden) {
			return fmt.Errorf("command would bypass the active workspace: %s", forbidden)
		}
	}
	workspace, _ = filepath.Abs(filepath.Clean(workspace))
	pathScan := commandExternalURLPattern.ReplaceAllString(command, "")
	for _, match := range commandAbsolutePathPattern.FindAllStringSubmatch(pathScan, -1) {
		candidate := strings.TrimRight(match[1], ",:")
		if candidate == "" {
			continue
		}
		if _, err := gitinfra.EnsureSafePath(workspace, candidate); err != nil {
			return errors.New("absolute command paths must remain inside the active workspace")
		}
	}
	for _, token := range strings.FieldsFunc(command, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ';' || r == '|' || r == '&' || r == '(' || r == ')'
	}) {
		token = strings.Trim(token, "\"'`<>=,")
		if strings.HasPrefix(token, "/") && token != workspace && !strings.HasPrefix(token, workspace+string(filepath.Separator)) {
			return errors.New("absolute command paths must remain inside the active workspace")
		}
	}
	return nil
}
