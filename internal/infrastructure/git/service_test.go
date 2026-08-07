package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func runGitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "LANG=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestServiceSiblingWorktreesAreIsolated(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	svc := NewService(Config{WorktreeDirName: ".worktrees"})
	if _, err := svc.EnsureRepository(ctx, root, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "same.txt"), []byte("root"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Commit(ctx, root, "add fixture", []string{"same.txt"}); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(root, ".worktrees", "run-1")
	second := filepath.Join(root, ".worktrees", "run-2")
	if err := svc.AddWorktree(ctx, root, first, "demo/1-first", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddWorktree(ctx, root, second, "demo/2-second", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(first, "same.txt"), []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "same.txt"), []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{first: "first", second: "second", root: "root"} {
		data, err := os.ReadFile(filepath.Join(path, "same.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != want {
			t.Fatalf("%s: got %q, want %q", path, data, want)
		}
	}
	items, err := svc.ListWorktrees(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("got %d worktrees, want 3", len(items))
	}
}

func TestCommitUsesExactScopeAndPreservesUnrelatedStaging(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	service := NewService(Config{GitUserName: "AgentCanvas Test", GitUserEmail: "agentcanvas@example.test"})
	if _, err := service.EnsureRepository(ctx, root, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "selected.txt"), []byte("selected\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "already-staged.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, root, "add", "already-staged.txt")

	result, err := service.Commit(ctx, root, "feat: commit selected file", []string{"selected.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Paths) != 1 || result.Paths[0] != "selected.txt" {
		t.Fatalf("actual commit scope was not returned: %#v", result)
	}
	if committed := runGitTest(t, root, "show", "--format=", "--name-only", "HEAD"); committed != "selected.txt" {
		t.Fatalf("commit included files outside the approved scope: %q", committed)
	}
	if staged := runGitTest(t, root, "diff", "--cached", "--name-only"); staged != "already-staged.txt" {
		t.Fatalf("unrelated staged file was not preserved: %q", staged)
	}
}

func TestCommitRejectsProtectedFilesInImplicitOrDirectoryScope(t *testing.T) {
	for _, test := range []struct {
		name  string
		scope []string
	}{
		{name: "implicit all changes"},
		{name: "directory", scope: []string{"config"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			service := NewService(Config{GitUserName: "AgentCanvas Test", GitUserEmail: "agentcanvas@example.test"})
			if _, err := service.EnsureRepository(ctx, root, true); err != nil {
				t.Fatal(err)
			}
			targetDir := root
			if len(test.scope) > 0 {
				targetDir = filepath.Join(root, "config")
				if err := os.MkdirAll(targetDir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(targetDir, ".env"), []byte("TOKEN=secret\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			before := runGitTest(t, root, "rev-parse", "HEAD")
			if _, err := service.Commit(ctx, root, "feat: unsafe", test.scope); err == nil || !strings.Contains(err.Error(), "protected") {
				t.Fatalf("protected commit scope returned %v", err)
			}
			if after := runGitTest(t, root, "rev-parse", "HEAD"); after != before {
				t.Fatalf("HEAD changed after protected commit was rejected: before=%s after=%s", before, after)
			}
		})
	}
}

func TestEnsureSafePathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := EnsureSafePath(root, filepath.Join(link, "new.txt")); err == nil {
		t.Fatal("expected symlink escape to be rejected")
	}
	if !IsSensitivePath(root, filepath.Join(link, "new.txt")) {
		t.Fatal("escaping symlink should be sensitive")
	}
}

func TestBareRepositoryIsRejected(t *testing.T) {
	root := t.TempDir()
	if err := exec.Command("git", "init", "--bare", filepath.Join(root, "bare.git")).Run(); err != nil {
		t.Fatal(err)
	}
	svc := NewService(Config{})
	if _, err := svc.EnsureRepository(context.Background(), filepath.Join(root, "bare.git"), true); !errors.Is(err, ErrBareRepository) {
		t.Fatalf("got %v, want ErrBareRepository", err)
	}
}

func TestParseWorktreesPorcelain(t *testing.T) {
	items := ParseWorktrees("worktree /tmp/root\nHEAD abc\nbranch refs/heads/main\n\nworktree /tmp/child\nHEAD def\ndetached\nlocked reason\n")
	if len(items) != 2 || items[0].Branch != "main" || !items[1].Detached || !items[1].Locked || items[1].LockReason != "reason" {
		t.Fatalf("unexpected parse result: %#v", items)
	}
}

func TestResolveCommitUsesFetchedBaseRefInsteadOfLocalHead(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	runGitTest(t, root, "init", "--bare", remote)
	seed := filepath.Join(root, "seed")
	runGitTest(t, root, "init", seed)
	runGitTest(t, seed, "config", "user.name", "AgentCanvas Test")
	runGitTest(t, seed, "config", "user.email", "agentcanvas@example.test")
	if err := os.WriteFile(filepath.Join(seed, "base.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, seed, "add", "base.txt")
	runGitTest(t, seed, "commit", "-m", "initial")
	runGitTest(t, seed, "branch", "-M", "main")
	runGitTest(t, seed, "remote", "add", "origin", remote)
	runGitTest(t, seed, "push", "-u", "origin", "main")
	runGitTest(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")

	local := filepath.Join(root, "local")
	updater := filepath.Join(root, "updater")
	runGitTest(t, root, "clone", remote, local)
	runGitTest(t, root, "clone", remote, updater)
	runGitTest(t, updater, "config", "user.name", "AgentCanvas Test")
	runGitTest(t, updater, "config", "user.email", "agentcanvas@example.test")
	if err := os.WriteFile(filepath.Join(updater, "base.txt"), []byte("two"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, updater, "commit", "-am", "advance remote")
	runGitTest(t, updater, "push", "origin", "main")
	localHead := runGitTest(t, local, "rev-parse", "HEAD")
	remoteHead := runGitTest(t, updater, "rev-parse", "HEAD")
	if localHead == remoteHead {
		t.Fatal("fixture did not advance the remote")
	}
	fetchHead := filepath.Join(local, ".git", "FETCH_HEAD")
	if _, err := os.Stat(fetchHead); err == nil {
		stale := time.Now().Add(-10 * time.Minute)
		if err := os.Chtimes(fetchHead, stale, stale); err != nil {
			t.Fatal(err)
		}
	}

	service := NewService(Config{FetchFreshness: time.Second})
	baseRef, _ := service.ResolveBase(ctx, local)
	baseSHA, err := service.ResolveCommit(ctx, local, baseRef)
	if err != nil {
		t.Fatal(err)
	}
	if baseRef != "origin/main" || baseSHA != remoteHead || baseSHA == localHead {
		t.Fatalf("resolved ref/sha mismatch: ref=%q sha=%q local=%q remote=%q", baseRef, baseSHA, localHead, remoteHead)
	}

	runGitTest(t, local, "remote", "set-url", "origin", filepath.Join(root, "missing-remote.git"))
	freshRef, freshLabel := service.ResolveBase(ctx, local)
	if freshRef != "origin/main" || !strings.Contains(freshLabel, "cached fresh") {
		t.Fatalf("fresh FETCH_HEAD did not skip a broken fetch: ref=%q label=%q", freshRef, freshLabel)
	}
	stale := time.Now().Add(-10 * time.Minute)
	if err := os.Chtimes(fetchHead, stale, stale); err != nil {
		t.Fatal(err)
	}
	cachedRef, cachedLabel := service.ResolveBase(ctx, local)
	if cachedRef != "origin/main" || !strings.Contains(cachedLabel, "cached") || strings.Contains(cachedLabel, "fresh") {
		t.Fatalf("failed fetch did not fall back to cached remote ref: ref=%q label=%q", cachedRef, cachedLabel)
	}
}

func TestEnsureRepositoryCreatesInitialCommitAndFallsBackToHead(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	service := NewService(Config{GitUserName: "AgentCanvas Test", GitUserEmail: "agentcanvas@example.test"})
	resolved, err := service.EnsureRepository(ctx, root, true)
	if err != nil {
		t.Fatal(err)
	}
	head, err := service.Head(ctx, root)
	if err != nil || len(head) != 40 {
		t.Fatalf("initial HEAD mismatch: %q err=%v", head, err)
	}
	if message := runGitTest(t, root, "log", "-1", "--format=%s"); message != "Initial commit" {
		t.Fatalf("initial commit message = %q", message)
	}
	ref, label := service.ResolveBase(ctx, resolved)
	if ref != "HEAD" || !strings.Contains(label, "local") {
		t.Fatalf("repository without remote did not fall back to HEAD: ref=%q label=%q", ref, label)
	}
}

func TestEnsureRepositoryRepairsUnbornHeadWithoutCommittingStagedFiles(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	runGitTest(t, root, "init")
	if err := os.WriteFile(filepath.Join(root, "staged.txt"), []byte("user work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, root, "add", "staged.txt")
	service := NewService(Config{GitUserName: "AgentCanvas Test", GitUserEmail: "agentcanvas@example.test"})
	if _, err := service.EnsureRepository(ctx, root, false); err != nil {
		t.Fatal(err)
	}
	if message := runGitTest(t, root, "log", "-1", "--format=%s"); message != "Initial commit" {
		t.Fatalf("initial commit message = %q", message)
	}
	if files := runGitTest(t, root, "show", "--format=", "--name-only", "HEAD"); files != "" {
		t.Fatalf("Initial commit unexpectedly included staged user files: %q", files)
	}
	if staged := runGitTest(t, root, "diff", "--cached", "--name-only"); staged != "staged.txt" {
		t.Fatalf("staged user work was not preserved: %q", staged)
	}
}

func TestDetachedHeadAndExistingBranchWorktree(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	service := NewService(Config{})
	if _, err := service.EnsureRepository(ctx, root, true); err != nil {
		t.Fatal(err)
	}
	head := runGitTest(t, root, "rev-parse", "HEAD")
	runGitTest(t, root, "branch", "demo/existing")
	path := filepath.Join(root, ".worktrees", "existing")
	if err := service.AddWorktree(ctx, root, path, "demo/existing", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if got := runGitTest(t, path, "rev-parse", "HEAD"); got != head {
		t.Fatalf("existing branch worktree head = %q, want %q", got, head)
	}
	runGitTest(t, path, "checkout", "--detach")
	status, err := service.Status(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if status.Branch != "" || service.CurrentBranch(ctx, path) != "" {
		t.Fatalf("detached HEAD reported a branch: %#v", status)
	}
}

func TestAddWorktreeDoesNotSilentlyChangeMissingBase(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	service := NewService(Config{})
	if _, err := service.EnsureRepository(ctx, root, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".worktrees", "missing-base")
	branch := "demo/missing-base"
	if err := service.AddWorktree(ctx, root, path, branch, "refs/remotes/origin/deleted"); err == nil {
		t.Fatal("expected a missing recorded base to fail instead of falling back to HEAD")
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed worktree unexpectedly left a checkout at %q: %v", path, err)
	}
	if service.refExists(ctx, root, "refs/heads/"+branch) {
		t.Fatalf("failed worktree unexpectedly created branch %q", branch)
	}
}

func TestBranchSanitizationAndLimitedWriter(t *testing.T) {
	branch := BranchName(" Demo Project ", 42, "fix @{bad} ../ path.lock")
	if branch != "demo-project/42-fix-bad-path" {
		t.Fatalf("unexpected sanitized branch %q", branch)
	}
	var output strings.Builder
	writer := &limitedWriter{w: &output, max: 4}
	if count, err := writer.Write([]byte("abcdef")); err != nil || count != 6 || output.String() != "abcd" {
		t.Fatalf("limited writer returned count=%d output=%q err=%v", count, output.String(), err)
	}
}
