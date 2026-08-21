package toolruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"unicode/utf8"
)

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
