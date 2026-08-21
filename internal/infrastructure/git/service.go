package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	workspacedomain "agentcanvas/internal/domain/workspace"
)

var ErrNotRepository = errors.New("path is not a git repository")
var ErrBareRepository = errors.New("bare git repositories are not supported")

type Config struct {
	CommandTimeout  time.Duration
	FetchTimeout    time.Duration
	FetchFreshness  time.Duration
	MaxOutputBytes  int
	WorktreeDirName string
	GitUserName     string
	GitUserEmail    string
}

type Service struct{ cfg Config }

type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type Worktree = workspacedomain.GitWorktree
type Status = workspacedomain.GitStatus
type CommitResult = workspacedomain.GitCommitResult

func NewService(cfg Config) *Service {
	if cfg.CommandTimeout <= 0 {
		cfg.CommandTimeout = 30 * time.Second
	}
	if cfg.FetchTimeout <= 0 {
		cfg.FetchTimeout = 5 * time.Second
	}
	if cfg.FetchFreshness <= 0 {
		cfg.FetchFreshness = 5 * time.Minute
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 256 * 1024
	}
	if cfg.WorktreeDirName == "" {
		cfg.WorktreeDirName = ".worktrees"
	}
	if cfg.GitUserName == "" {
		cfg.GitUserName = "AgentCanvas"
	}
	if cfg.GitUserEmail == "" {
		cfg.GitUserEmail = "agentcanvas@localhost"
	}
	return &Service{cfg: cfg}
}

func (s *Service) run(ctx context.Context, cwd string, timeout time.Duration, args ...string) (CommandResult, error) {
	if timeout <= 0 {
		timeout = s.cfg.CommandTimeout
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, "git", args...)
	cmd.Dir = cwd
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never", "LC_ALL=C", "LANG=C")
	var stdout, stderr strings.Builder
	cmd.Stdout = &limitedWriter{w: &stdout, max: s.cfg.MaxOutputBytes}
	cmd.Stderr = &limitedWriter{w: &stderr, max: s.cfg.MaxOutputBytes}
	err := cmd.Run()
	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: 0}
	if err != nil {
		result.ExitCode = 1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			result.ExitCode = exitErr.ExitCode()
		}
		if commandCtx.Err() != nil {
			return result, fmt.Errorf("git %s: %w", strings.Join(args, " "), commandCtx.Err())
		}
		return result, fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(result.Stderr))
	}
	return result, nil
}

type limitedWriter struct {
	w   *strings.Builder
	max int
	n   int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	originalLength := len(p)
	remain := w.max - w.n
	if remain <= 0 {
		return originalLength, nil
	}
	if len(p) > remain {
		p = p[:remain]
	}
	w.w.Write(p)
	w.n += len(p)
	return originalLength, nil
}

func (s *Service) RepoRoot(ctx context.Context, path string) (string, error) {
	path, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", err
	}
	result, err := s.run(ctx, path, 5*time.Second, "rev-parse", "--show-toplevel")
	if err != nil {
		bare, bareErr := s.run(ctx, path, 5*time.Second, "rev-parse", "--is-bare-repository")
		if bareErr == nil && strings.TrimSpace(bare.Stdout) == "true" {
			return "", ErrBareRepository
		}
		return "", fmt.Errorf("%w: %s", ErrNotRepository, strings.TrimSpace(result.Stderr))
	}
	root := filepath.Clean(strings.TrimSpace(result.Stdout))
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = filepath.Clean(resolved)
	}
	bare, _ := s.run(ctx, root, 5*time.Second, "rev-parse", "--is-bare-repository")
	if strings.TrimSpace(bare.Stdout) == "true" {
		return "", ErrBareRepository
	}
	return root, nil
}

func (s *Service) EnsureRepository(ctx context.Context, dir string, allowInit bool) (string, error) {
	root, err := s.RepoRoot(ctx, dir)
	if err == nil {
		if _, headErr := s.Head(ctx, root); headErr == nil {
			return root, nil
		}
		if commitErr := s.createInitialCommit(ctx, root); commitErr != nil {
			return "", commitErr
		}
		return root, nil
	}
	if errors.Is(err, ErrBareRepository) {
		return "", err
	}
	if !allowInit {
		return "", err
	}
	dir, err = filepath.Abs(filepath.Clean(dir))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if _, err := s.run(ctx, dir, 10*time.Second, "init"); err != nil {
		return "", err
	}
	if err := s.createInitialCommit(ctx, dir); err != nil {
		return "", err
	}
	return s.RepoRoot(ctx, dir)
}

func (s *Service) createInitialCommit(ctx context.Context, dir string) error {
	// --only with no pathspec creates a truly empty root commit even when the
	// user already has staged files in an unborn repository. Their index is
	// intentionally left untouched for later review.
	_, err := s.run(ctx, dir, 10*time.Second, "-c", "user.name="+s.cfg.GitUserName, "-c", "user.email="+s.cfg.GitUserEmail, "commit", "--allow-empty", "--only", "-m", "Initial commit")
	return err
}

func (s *Service) Head(ctx context.Context, root string) (string, error) {
	return s.ResolveCommit(ctx, root, "HEAD")
}

func (s *Service) ResolveCommit(ctx context.Context, root, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		ref = "HEAD"
	}
	result, err := s.run(ctx, root, 5*time.Second, "rev-parse", "--verify", ref+"^{commit}")
	return strings.TrimSpace(result.Stdout), err
}

func (s *Service) CurrentBranch(ctx context.Context, path string) string {
	result, _ := s.run(ctx, path, 5*time.Second, "branch", "--show-current")
	return strings.TrimSpace(result.Stdout)
}

func (s *Service) ListWorktrees(ctx context.Context, root string) ([]Worktree, error) {
	result, err := s.run(ctx, root, s.cfg.CommandTimeout, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return ParseWorktrees(result.Stdout), nil
}

func ParseWorktrees(output string) []Worktree {
	var items []Worktree
	var current *Worktree
	flush := func() {
		if current != nil {
			items = append(items, *current)
			current = nil
		}
	}
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current = &Worktree{Path: strings.TrimSpace(strings.TrimPrefix(line, "worktree "))}
		case current == nil:
		case strings.HasPrefix(line, "HEAD "):
			current.Head = strings.TrimSpace(strings.TrimPrefix(line, "HEAD "))
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(line, "branch ")), "refs/heads/")
		case line == "detached":
			current.Detached = true
		case line == "bare":
			current.Bare = true
		case line == "locked" || strings.HasPrefix(line, "locked "):
			current.Locked = true
			current.LockReason = strings.TrimSpace(strings.TrimPrefix(line, "locked"))
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			current.Prunable = true
		case line == "":
			flush()
		}
	}
	flush()
	return items
}

func SanitizeBranch(value string) string {
	return workspacedomain.SanitizeBranch(value)
}

func Slugify(value string) string {
	return workspacedomain.Slugify(value)
}

func BranchName(projectSlug string, runID int64, task string) string {
	return workspacedomain.BranchName(projectSlug, runID, task)
}

func (s *Service) ResolveBase(ctx context.Context, root string) (string, string) {
	up, _ := s.run(ctx, root, 5*time.Second, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if ref := strings.TrimSpace(up.Stdout); ref != "" && strings.Contains(ref, "/") {
		return s.refreshRef(ctx, root, ref)
	}
	defaultRef := ""
	if result, err := s.run(ctx, root, 5*time.Second, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil {
		defaultRef = strings.TrimPrefix(strings.TrimSpace(result.Stdout), "refs/remotes/")
	}
	if defaultRef == "" {
		if result, err := s.run(ctx, root, s.cfg.FetchTimeout, "remote", "show", "origin"); err == nil {
			for _, line := range strings.Split(result.Stdout, "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "HEAD branch:") {
					branch := strings.TrimSpace(strings.TrimPrefix(line, "HEAD branch:"))
					if branch != "" && branch != "(unknown)" {
						defaultRef = "origin/" + branch
					}
					break
				}
			}
		}
	}
	if defaultRef != "" {
		return s.refreshRef(ctx, root, defaultRef)
	}
	return "HEAD", "HEAD (local)"
}

func (s *Service) refreshRef(ctx context.Context, root, ref string) (string, string) {
	remote, branch, ok := strings.Cut(ref, "/")
	if !ok {
		return "HEAD", "HEAD (local)"
	}
	gitDir, _ := s.run(ctx, root, 5*time.Second, "rev-parse", "--git-dir")
	fetchHead := strings.TrimSpace(gitDir.Stdout)
	if !filepath.IsAbs(fetchHead) {
		fetchHead = filepath.Join(root, fetchHead)
	}
	if info, err := os.Stat(filepath.Join(fetchHead, "FETCH_HEAD")); err == nil && time.Since(info.ModTime()) < s.cfg.FetchFreshness {
		if s.refExists(ctx, root, ref) {
			return ref, ref + " (cached fresh)"
		}
	}
	if _, err := s.run(ctx, root, s.cfg.FetchTimeout, "fetch", remote, branch); err == nil && s.refExists(ctx, root, ref) {
		return ref, ref + " (fetched)"
	}
	if s.refExists(ctx, root, ref) {
		return ref, ref + " (cached)"
	}
	return "HEAD", "HEAD (local fallback)"
}

func (s *Service) refExists(ctx context.Context, root, ref string) bool {
	_, err := s.run(ctx, root, 5*time.Second, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

func (s *Service) AddWorktree(ctx context.Context, root, path, branch, base string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	args := []string{"worktree", "add", "-b", branch, path}
	if base != "" {
		args = append(args, base)
	}
	branchExisted := s.refExists(ctx, root, "refs/heads/"+branch)
	if _, err := s.run(ctx, root, s.cfg.CommandTimeout, args...); err != nil {
		if branchExisted || strings.Contains(strings.ToLower(err.Error()), "already exists") {
			_, reuseErr := s.run(ctx, root, s.cfg.CommandTimeout, "worktree", "add", path, branch)
			return reuseErr
		}
		return err
	}
	return nil
}

func (s *Service) LockWorktree(ctx context.Context, root, path, reason string) error {
	_, err := s.run(ctx, root, 10*time.Second, "worktree", "lock", "--reason", reason, path)
	return err
}

func (s *Service) UnlockWorktree(ctx context.Context, root, path string) error {
	_, err := s.run(ctx, root, 10*time.Second, "worktree", "unlock", path)
	return err
}

func (s *Service) Prune(ctx context.Context, root string) error {
	_, err := s.run(ctx, root, s.cfg.CommandTimeout, "worktree", "prune")
	return err
}

func (s *Service) RemoveWorktree(ctx context.Context, root, path string, force bool) error {
	_, _ = s.run(ctx, root, 10*time.Second, "worktree", "unlock", path)
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	_, err := s.run(ctx, root, s.cfg.CommandTimeout, args...)
	return err
}

func (s *Service) Status(ctx context.Context, path string) (Status, error) {
	root, err := s.RepoRoot(ctx, path)
	if err != nil {
		return Status{}, err
	}
	head, err := s.Head(ctx, path)
	if err != nil {
		return Status{}, err
	}
	result, err := s.run(ctx, path, 10*time.Second, "status", "--porcelain=v1")
	if err != nil {
		return Status{}, err
	}
	status := Status{Root: root, Branch: s.CurrentBranch(ctx, path), Head: head}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if len(line) < 3 {
			continue
		}
		item := strings.TrimSpace(line[3:])
		if item == "" {
			continue
		}
		if line[0] == '?' && line[1] == '?' {
			status.Untracked = append(status.Untracked, item)
			continue
		}
		if line[0] != ' ' {
			status.Staged = append(status.Staged, item)
		}
		if line[1] != ' ' {
			status.Changed = append(status.Changed, item)
		}
	}
	status.Dirty = len(status.Staged)+len(status.Changed)+len(status.Untracked) > 0
	remoteRefs, err := s.run(ctx, path, 10*time.Second, "for-each-ref", "--format=%(refname)", "refs/remotes")
	if err != nil {
		return Status{}, err
	}
	if strings.TrimSpace(remoteRefs.Stdout) != "" {
		unpushed, err := s.run(ctx, path, 10*time.Second, "log", "--oneline", "HEAD", "--not", "--remotes")
		if err != nil {
			return Status{}, err
		}
		status.Unpushed = strings.TrimSpace(unpushed.Stdout) != ""
	}
	return status, nil
}

func (s *Service) RuntimeStatus(ctx context.Context, path string) (branch, head string, dirty, unpushed bool, err error) {
	status, err := s.Status(ctx, path)
	if err != nil {
		return "", "", false, false, err
	}
	return status.Branch, status.Head, status.Dirty, status.Unpushed, nil
}

func (s *Service) Diff(ctx context.Context, path string, staged bool) (string, error) {
	args := []string{"diff"}
	if staged {
		args = append(args, "--cached")
	}
	result, err := s.run(ctx, path, s.cfg.CommandTimeout, args...)
	return result.Stdout, err
}

func (s *Service) Log(ctx context.Context, path string, limit int) (string, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	result, err := s.run(ctx, path, s.cfg.CommandTimeout, "log", "--oneline", fmt.Sprintf("-%d", limit))
	return result.Stdout, err
}

func (s *Service) Branches(ctx context.Context, path string) ([]string, error) {
	result, err := s.run(ctx, path, s.cfg.CommandTimeout, "for-each-ref", "--format=%(refname:short)", "--sort=-committerdate", "refs/heads")
	if err != nil {
		return nil, err
	}
	items := make([]string, 0)
	for _, line := range strings.Split(result.Stdout, "\n") {
		if item := strings.TrimSpace(line); item != "" {
			items = append(items, item)
		}
	}
	return items, nil
}

func (s *Service) Commit(ctx context.Context, path, message string, paths []string) (CommitResult, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return CommitResult{}, errors.New("commit message is required")
	}
	root, err := s.RepoRoot(ctx, path)
	if err != nil {
		return CommitResult{}, err
	}
	branch := s.CurrentBranch(ctx, path)
	if strings.TrimSpace(branch) == "" {
		return CommitResult{}, errors.New("cannot commit on detached HEAD")
	}
	workspacePath, absErr := filepath.Abs(filepath.Clean(path))
	if absErr != nil {
		return CommitResult{}, absErr
	}
	if resolved, resolveErr := filepath.EvalSymlinks(workspacePath); resolveErr == nil {
		workspacePath = filepath.Clean(resolved)
	}
	changed, untracked, err := s.changedPaths(ctx, path)
	if err != nil {
		return CommitResult{}, err
	}
	if len(changed) == 0 {
		return CommitResult{}, errors.New("workspace has no changes to commit")
	}
	requested := make([]string, 0, len(paths))
	for _, candidate := range paths {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" || filepath.IsAbs(candidate) {
			return CommitResult{}, errors.New("commit paths must be relative to the workspace")
		}
		for _, part := range strings.Split(filepath.ToSlash(candidate), "/") {
			if part == ".." {
				return CommitResult{}, errors.New("commit paths cannot traverse outside the workspace")
			}
		}
		target, safeErr := EnsureSafePath(workspacePath, filepath.Join(workspacePath, candidate))
		if safeErr != nil {
			return CommitResult{}, errors.New("commit path is outside the workspace policy")
		}
		relative, relErr := filepath.Rel(root, target)
		if relErr != nil {
			return CommitResult{}, relErr
		}
		requested = append(requested, filepath.ToSlash(relative))
	}
	selected, err := selectCommitPaths(changed, requested)
	if err != nil {
		return CommitResult{}, err
	}
	for _, relative := range selected {
		target, safeErr := EnsureSafePath(workspacePath, filepath.Join(workspacePath, filepath.FromSlash(relative)))
		if safeErr != nil || IsSensitivePath(workspacePath, target) {
			return CommitResult{}, fmt.Errorf("commit path %q is protected by the workspace policy", relative)
		}
	}
	intentPaths := make([]string, 0)
	for _, relative := range selected {
		if untracked[relative] {
			intentPaths = append(intentPaths, relative)
		}
	}
	if len(intentPaths) > 0 {
		args := append([]string{"add", "--intent-to-add", "--"}, intentPaths...)
		if _, err := s.run(ctx, path, s.cfg.CommandTimeout, args...); err != nil {
			return CommitResult{}, err
		}
	}
	args := []string{"-c", "user.name=" + s.cfg.GitUserName, "-c", "user.email=" + s.cfg.GitUserEmail, "commit", "--only", "-m", message, "--"}
	args = append(args, selected...)
	if _, err := s.run(ctx, path, s.cfg.CommandTimeout, args...); err != nil {
		if len(intentPaths) > 0 {
			resetArgs := append([]string{"reset", "--"}, intentPaths...)
			_, _ = s.run(context.Background(), path, 5*time.Second, resetArgs...)
		}
		return CommitResult{}, err
	}
	header, err := s.run(ctx, path, 5*time.Second, "rev-parse", "HEAD")
	return CommitResult{Hash: strings.TrimSpace(header.Stdout), Message: message, Paths: selected}, err
}

func (s *Service) changedPaths(ctx context.Context, path string) ([]string, map[string]bool, error) {
	commands := [][]string{
		{"diff", "--no-renames", "--name-only", "-z"},
		{"diff", "--cached", "--no-renames", "--name-only", "-z"},
		{"ls-files", "--others", "--exclude-standard", "-z"},
	}
	seen := make(map[string]bool)
	untracked := make(map[string]bool)
	for index, args := range commands {
		result, err := s.run(ctx, path, s.cfg.CommandTimeout, args...)
		if err != nil {
			return nil, nil, err
		}
		for _, candidate := range strings.Split(result.Stdout, "\x00") {
			candidate = filepath.ToSlash(strings.TrimSpace(candidate))
			if candidate == "" {
				continue
			}
			seen[candidate] = true
			if index == len(commands)-1 {
				untracked[candidate] = true
			}
		}
	}
	items := make([]string, 0, len(seen))
	for candidate := range seen {
		items = append(items, candidate)
	}
	sort.Strings(items)
	return items, untracked, nil
}

func selectCommitPaths(changed, requested []string) ([]string, error) {
	if len(requested) == 0 {
		return append([]string(nil), changed...), nil
	}
	selected := make(map[string]bool)
	for _, scope := range requested {
		matched := false
		for _, candidate := range changed {
			if scope == "." || candidate == scope || strings.HasPrefix(candidate, strings.TrimSuffix(scope, "/")+"/") {
				selected[candidate] = true
				matched = true
			}
		}
		if !matched {
			return nil, fmt.Errorf("commit path %q has no changes", scope)
		}
	}
	items := make([]string, 0, len(selected))
	for candidate := range selected {
		items = append(items, candidate)
	}
	sort.Strings(items)
	return items, nil
}

func (s *Service) HashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func EnsureInside(root, target string) (string, error) {
	return workspacedomain.EnsureInside(root, target)
}

// EnsureSafePath validates both the lexical path and every existing symlink
// ancestor. This also protects writes to new files below an escaping symlink.
func EnsureSafePath(root, target string) (string, error) {
	return workspacedomain.EnsureSafePath(root, target)
}

func IsSensitivePath(root, target string) bool {
	return workspacedomain.IsSensitivePath(root, target)
}

func SortedWorktrees(items []Worktree) []Worktree {
	sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
	return items
}
