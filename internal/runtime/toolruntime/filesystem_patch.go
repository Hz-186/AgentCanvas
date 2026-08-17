package toolruntime

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"
)

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
