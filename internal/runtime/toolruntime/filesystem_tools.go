package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
		"workspace_id": rc.Workspace.ID, "project_id": rc.Workspace.ProjectID, "run_id": rc.RunID, "kind": rc.Workspace.Kind, "repository_root": rc.Workspace.RepositoryRoot,
		"workspace_path": rc.Workspace.WorkspacePath, "branch_name": rc.Workspace.BranchName, "base_sha": rc.Workspace.BaseSHA, "head_sha": rc.Workspace.HeadSHA,
		"dirty": rc.Workspace.Dirty, "has_unpushed_commits": rc.Workspace.HasUnpushedCommits, "status": "ready", "error_message": "", "mutation": payload,
	}
	_ = rc.EmitEvent(ctx, "workspace.status_changed", eventPayload)
}
