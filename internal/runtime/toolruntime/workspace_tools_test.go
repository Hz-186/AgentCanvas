package toolruntime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	workspacedomain "agentcanvas/internal/domain/workspace"
)

func TestResolveWorkspacePathRejectsTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	workspace := &workspacedomain.Workspace{RootPath: root}
	pack := &workspacedomain.Pack{AllowedPaths: []string{"."}}
	if _, err := resolveWorkspacePath(workspace, pack, "../secret", false); err == nil {
		t.Fatal("traversal escaped workspace")
	}
	if _, err := resolveWorkspacePath(workspace, pack, "escape", false); err == nil {
		t.Fatal("symlink escaped workspace")
	}
}
func TestResolveWorkspacePathHonorsAllowedPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "src"), 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "private"), 0750); err != nil {
		t.Fatal(err)
	}
	workspace := &workspacedomain.Workspace{RootPath: root}
	pack := &workspacedomain.Pack{AllowedPaths: []string{"src"}}
	if _, err := resolveWorkspacePath(workspace, pack, "src", false); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveWorkspacePath(workspace, pack, "private", false); err == nil {
		t.Fatal("private path should be denied")
	}
}

func TestResolveWorkspacePathAllowsMissingNestedParentsInsideRoot(t *testing.T) {
	root := t.TempDir()
	workspace := &workspacedomain.Workspace{RootPath: root}
	pack := &workspacedomain.Pack{AllowedPaths: []string{"src"}}
	path, err := resolveWorkspacePath(workspace, pack, "src/generated/deep/file.go", true)
	if err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(resolvedRoot, "src/generated/deep/file.go") {
		t.Fatalf("unexpected path: %s", path)
	}
}

type recordingWorkspaceRunner struct{ commands [][]string }

func (r *recordingWorkspaceRunner) Run(_ context.Context, _ *workspacedomain.Workspace, _ *workspacedomain.Pack, command []string) (string, error) {
	r.commands = append(r.commands, append([]string(nil), command...))
	return "ok", nil
}

func TestWorkspaceGitCommitStagesNewFilesBeforeCommit(t *testing.T) {
	runner := &recordingWorkspaceRunner{}
	tool := WorkspaceTool{ToolName: "git_commit", Workspace: &workspacedomain.Workspace{RootPath: t.TempDir()}, Pack: &workspacedomain.Pack{CommandAllowlist: []string{"git"}}, Runner: runner}
	if _, err := tool.Execute(context.Background(), ToolRunContext{}, json.RawMessage(`{"message":"feat: test"}`)); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 2 {
		t.Fatalf("expected add and commit, got %d commands", len(runner.commands))
	}
	if got := runner.commands[0]; len(got) != 3 || got[0] != "git" || got[1] != "add" || got[2] != "--all" {
		t.Fatalf("unexpected add command: %#v", got)
	}
}

func TestEnsureGitWorktreeCreatesStableRunBranch(t *testing.T) {
	repository := t.TempDir()
	if err := runGit(context.Background(), "-C", repository, "init"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte("workspace\n"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := runGit(context.Background(), "-C", repository, "add", "--all"); err != nil {
		t.Fatal(err)
	}
	if err := runGit(context.Background(), "-C", repository, "-c", "user.name=AgentCanvas Test", "-c", "user.email=test@local", "commit", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	worktree := filepath.Join(t.TempDir(), "run-42")
	if err := ensureGitWorktree(context.Background(), repository, worktree, "agentcanvas/run-42"); err != nil {
		t.Fatal(err)
	}
	if err := ensureGitWorktree(context.Background(), repository, worktree, "agentcanvas/run-42"); err != nil {
		t.Fatalf("existing worktree should be reusable: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktree, "README.md")); err != nil {
		t.Fatal(err)
	}
	if err := removeGitWorktree(context.Background(), repository, worktree); err != nil {
		t.Fatal(err)
	}
}
