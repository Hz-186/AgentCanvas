package workspace_usecase

import (
	"agentcanvas/internal/domain"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain/audit"
	projectdomain "agentcanvas/internal/domain/project"
	workspacedomain "agentcanvas/internal/domain/workspace"
	gitinfra "agentcanvas/internal/infrastructure/git"
	agenterrors "agentcanvas/internal/pkg/errors"
)

type memoryWorkspaceAuditRepository struct {
	logs []audit.Log
}

func (r *memoryWorkspaceAuditRepository) Create(_ context.Context, item *audit.Log) error {
	r.logs = append(r.logs, *item)
	return nil
}

func (r *memoryWorkspaceAuditRepository) ListByOwner(_ context.Context, ownerID int64, _, _ int) ([]audit.Log, error) {
	items := make([]audit.Log, 0, len(r.logs))
	for index := range r.logs {
		if r.logs[index].OwnerID == ownerID {
			items = append(items, r.logs[index])
		}
	}
	return items, nil
}

type memoryProjectRepository struct {
	items        map[int64]projectdomain.Project
	folders      map[int64]projectdomain.ProjectFolder
	nextFolderID int64
}

func (r *memoryProjectRepository) Create(_ context.Context, item *projectdomain.Project) error {
	if r.items == nil {
		r.items = make(map[int64]projectdomain.Project)
	}
	if item.ID == 0 {
		item.ID = int64(len(r.items) + 1)
	}
	r.items[item.ID] = *item
	return nil
}
func (r *memoryProjectRepository) CreateWithPrimaryFolder(ctx context.Context, item *projectdomain.Project, folder *projectdomain.ProjectFolder) error {
	if err := r.Create(ctx, item); err != nil {
		return err
	}
	folder.OwnerID = item.OwnerID
	folder.ProjectID = item.ID
	folder.Path = item.RepositoryRoot
	folder.IsRepositoryRoot = true
	return r.AddFolder(ctx, folder)
}
func (r *memoryProjectRepository) ListByOwner(_ context.Context, ownerID int64, includeArchived bool) ([]projectdomain.Project, error) {
	var result []projectdomain.Project
	for _, item := range r.items {
		if item.OwnerID == ownerID && (includeArchived || !item.Archived) {
			result = append(result, item)
		}
	}
	return result, nil
}
func (r *memoryProjectRepository) FindByID(_ context.Context, ownerID, id int64) (*projectdomain.Project, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	copy := item
	return &copy, nil
}
func (r *memoryProjectRepository) Update(_ context.Context, item *projectdomain.Project) error {
	r.items[item.ID] = *item
	return nil
}
func (r *memoryProjectRepository) Archive(_ context.Context, ownerID, id int64) error {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return errors.New("project not found")
	}
	item.Archived = true
	r.items[id] = item
	return nil
}
func (r *memoryProjectRepository) ListFolders(_ context.Context, ownerID, projectID int64) ([]projectdomain.ProjectFolder, error) {
	var result []projectdomain.ProjectFolder
	for _, item := range r.folders {
		if item.OwnerID == ownerID && item.ProjectID == projectID {
			result = append(result, item)
		}
	}
	return result, nil
}
func (r *memoryProjectRepository) AddFolder(_ context.Context, item *projectdomain.ProjectFolder) error {
	if r.folders == nil {
		r.folders = make(map[int64]projectdomain.ProjectFolder)
	}
	if item.ID == 0 {
		r.nextFolderID++
		item.ID = r.nextFolderID
	}
	r.folders[item.ID] = *item
	return nil
}
func (r *memoryProjectRepository) AddPrimaryFolder(ctx context.Context, item *projectdomain.ProjectFolder) error {
	if _, err := r.FindByID(ctx, item.OwnerID, item.ProjectID); err != nil {
		return err
	}
	if err := r.AddFolder(ctx, item); err != nil {
		return err
	}
	for id, folder := range r.folders {
		if folder.OwnerID == item.OwnerID && folder.ProjectID == item.ProjectID {
			folder.IsRepositoryRoot = id == item.ID
			r.folders[id] = folder
		}
	}
	projectItem := r.items[item.ProjectID]
	projectItem.RepositoryRoot = item.Path
	r.items[item.ProjectID] = projectItem
	return nil
}
func (r *memoryProjectRepository) DeleteFolder(context.Context, int64, int64, int64) error {
	return nil
}
func (r *memoryProjectRepository) SetPrimaryFolder(context.Context, int64, int64, int64) error {
	return nil
}

type memoryWorkspaceRepository struct {
	nextID               int64
	items                map[int64]workspacedomain.Workspace
	lastRecoverableLimit int
}

type findFailWorkspaceRepository struct {
	*memoryWorkspaceRepository
	failRunID int64
	err       error
}

func (r *findFailWorkspaceRepository) FindByRunID(ctx context.Context, ownerID, runID int64) (*workspacedomain.Workspace, error) {
	if runID == r.failRunID {
		return nil, r.err
	}
	return r.memoryWorkspaceRepository.FindByRunID(ctx, ownerID, runID)
}

type conflictOnceWorkspaceRepository struct {
	*memoryWorkspaceRepository
	conflicted bool
}

type updateFailWorkspaceRepository struct {
	*memoryWorkspaceRepository
	updateCalls int
	failAt      int
	alwaysFail  bool
	err         error
}

func (r *updateFailWorkspaceRepository) Update(ctx context.Context, item *workspacedomain.Workspace) error {
	r.updateCalls++
	if r.alwaysFail || r.failAt > 0 && r.updateCalls == r.failAt {
		return r.err
	}
	return r.memoryWorkspaceRepository.Update(ctx, item)
}

func (r *conflictOnceWorkspaceRepository) Create(ctx context.Context, item *workspacedomain.Workspace) error {
	if item.Kind == workspacedomain.KindWorktree && !r.conflicted {
		r.conflicted = true
		return agenterrors.ErrConflict
	}
	return r.memoryWorkspaceRepository.Create(ctx, item)
}

func newMemoryWorkspaceRepository() *memoryWorkspaceRepository {
	return &memoryWorkspaceRepository{items: make(map[int64]workspacedomain.Workspace)}
}
func (r *memoryWorkspaceRepository) Create(_ context.Context, item *workspacedomain.Workspace) error {
	r.nextID++
	item.ID = r.nextID
	now := time.Now().UTC()
	item.CreatedAt, item.UpdatedAt = now, now
	r.items[item.ID] = *item
	return nil
}
func (r *memoryWorkspaceRepository) FindByID(_ context.Context, ownerID, id int64) (*workspacedomain.Workspace, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	copy := item
	return &copy, nil
}
func (r *memoryWorkspaceRepository) FindByRunID(_ context.Context, ownerID, runID int64) (*workspacedomain.Workspace, error) {
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.RunID == runID {
			copy := item
			return &copy, nil
		}
	}
	return nil, agenterrors.ErrNotFound
}
func (r *memoryWorkspaceRepository) ListByProject(_ context.Context, ownerID, projectID int64) ([]workspacedomain.Workspace, error) {
	var result []workspacedomain.Workspace
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.ProjectID == projectID {
			result = append(result, item)
		}
	}
	return result, nil
}
func (r *memoryWorkspaceRepository) ListRecoverable(_ context.Context, limit int) ([]workspacedomain.Workspace, error) {
	r.lastRecoverableLimit = limit
	var result []workspacedomain.Workspace
	for _, item := range r.items {
		if item.Status == workspacedomain.StatusReady || item.Status == workspacedomain.StatusCreating {
			result = append(result, item)
			if limit > 0 && len(result) >= limit {
				break
			}
		}
	}
	return result, nil
}
func (r *memoryWorkspaceRepository) Update(_ context.Context, item *workspacedomain.Workspace) error {
	item.UpdatedAt = time.Now().UTC()
	r.items[item.ID] = *item
	return nil
}
func (r *memoryWorkspaceRepository) ListStale(_ context.Context, before time.Time, limit int) ([]workspacedomain.Workspace, error) {
	var result []workspacedomain.Workspace
	for _, item := range r.items {
		if item.UpdatedAt.Before(before) && len(result) < limit {
			result = append(result, item)
		}
	}
	return result, nil
}

func testWorkspaceService(t *testing.T) (*Service, *memoryWorkspaceRepository, *gitinfra.Service, string) {
	t.Helper()
	root := t.TempDir()
	gitService := gitinfra.NewService(gitinfra.Config{WorktreeDirName: ".worktrees", GitUserName: "AgentCanvas Test", GitUserEmail: "agentcanvas@example.test"})
	if _, err := gitService.EnsureRepository(context.Background(), root, true); err != nil {
		t.Fatal(err)
	}
	projects := &memoryProjectRepository{items: map[int64]projectdomain.Project{
		1: {BaseModel: domain.BaseModel{ID: 1, OwnerID: 7}, Slug: "demo", Name: "Demo", RepositoryRoot: root},
	}}
	workspaces := newMemoryWorkspaceRepository()
	service := NewService(projects, workspaces, gitService, Config{
		Enabled: true, AllowedRoots: []string{root}, WorktreeDirName: ".worktrees",
		MaxWorkspacesPerProject: 32, PreserveDirty: true, PreserveUnpushed: true, AutoInitRepository: true,
	})
	return service, workspaces, gitService, root
}

func TestLockProcessStateIsFailSafe(t *testing.T) {
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	live, known := lockProcessState(runWorkspaceLockReason(1))
	if !known || !live {
		t.Fatalf("current process should be live and known: live=%t known=%t", live, known)
	}
	live, known = lockProcessState(fmt.Sprintf("agentcanvas pid=99999999 host=%s", host))
	if !known || live {
		t.Fatalf("dead process should be known and not live: live=%t known=%t", live, known)
	}
	live, known = lockProcessState("manual lock without pid")
	if known || live {
		t.Fatalf("unknown lock must be fail-safe unknown: live=%t known=%t", live, known)
	}
	live, known = lockProcessState(fmt.Sprintf("agentcanvas pid=%d host=another-container", os.Getpid()))
	if known || live {
		t.Fatalf("cross-container PID must remain unknown: live=%t known=%t", live, known)
	}
}

func TestPrepareChildWorkspaceHermesIsolationPolicy(t *testing.T) {
	ctx := context.Background()
	service, repository, _, _ := testWorkspaceService(t)

	shared, err := service.PrepareRunWorkspace(ctx, 7, 1, 1, workspacedomain.KindShared, "demo", "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	inherited, err := service.PrepareChildWorkspace(ctx, 7, 1, 2, "inherit", "demo", "shared-child", shared)
	if err != nil {
		t.Fatal(err)
	}
	if inherited.ID != shared.ID || inherited.WorkspacePath != shared.WorkspacePath || inherited.RunID != 2 {
		t.Fatalf("shared child did not inherit the same physical workspace: parent=%#v child=%#v", shared, inherited)
	}
	items, err := repository.ListByProject(ctx, 7, 1)
	if err != nil || len(items) != 1 {
		t.Fatalf("shared inheritance should retain one workspace row: count=%d err=%v", len(items), err)
	}

	isolatedFromShared, err := service.PrepareChildWorkspace(ctx, 7, 1, 3, workspacedomain.KindWorktree, "demo", "isolated", shared)
	if err != nil {
		t.Fatal(err)
	}
	if isolatedFromShared.Kind != workspacedomain.KindWorktree || isolatedFromShared.WorkspacePath == shared.WorkspacePath {
		t.Fatalf("explicit shared child worktree was not isolated: %#v", isolatedFromShared)
	}

	worktreeParent, err := service.PrepareRunWorkspace(ctx, 7, 1, 4, workspacedomain.KindWorktree, "demo", "parent", nil)
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := service.PrepareChildWorkspace(ctx, 7, 1, 5, "inherit", "demo", "sibling", worktreeParent)
	if err != nil {
		t.Fatal(err)
	}
	if sibling.Kind != workspacedomain.KindWorktree || sibling.ID == worktreeParent.ID || sibling.WorkspacePath == worktreeParent.WorkspacePath {
		t.Fatalf("worktree child must receive a sibling checkout: parent=%#v child=%#v", worktreeParent, sibling)
	}
	if _, err := service.PrepareChildWorkspace(ctx, 7, 1, 6, workspacedomain.KindShared, "demo", "downgrade", worktreeParent); err == nil {
		t.Fatal("worktree child must not downgrade to shared")
	}
}

func TestAcquireRunWorkspaceLockReestablishesResumeLock(t *testing.T) {
	ctx := context.Background()
	service, repository, gitService, _ := testWorkspaceService(t)
	item, err := service.PrepareRunWorkspace(ctx, 7, 1, 88, workspacedomain.KindWorktree, "demo", "resume", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ReleaseRunWorkspaceLock(ctx, item.OwnerID, item.RunID); err != nil {
		t.Fatal(err)
	}
	item, err = repository.FindByRunID(ctx, item.OwnerID, item.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if item.Locked {
		t.Fatal("released workspace unexpectedly remains locked")
	}
	if err := service.AcquireRunWorkspaceLock(ctx, item, item.RunID); err != nil {
		t.Fatal(err)
	}
	trees, err := gitService.ListWorktrees(ctx, item.RepositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, tree := range trees {
		if filepath.Clean(tree.Path) == filepath.Clean(item.WorkspacePath) {
			if !tree.Locked || !strings.Contains(tree.LockReason, "run=88") || !strings.Contains(tree.LockReason, "pid=") {
				t.Fatalf("resume lock was not established: %#v", tree)
			}
			return
		}
	}
	t.Fatal("resumed worktree is missing")
}

func TestReleaseRunWorkspaceLockNeverUnlocksUnknownOwner(t *testing.T) {
	ctx := context.Background()
	service, repository, gitService, _ := testWorkspaceService(t)
	item, err := service.PrepareRunWorkspace(ctx, 7, 1, 23, workspacedomain.KindWorktree, "demo", "locked", nil)
	if err != nil {
		t.Fatal(err)
	}
	item.Locked, item.LockReason = true, "manual lock without verifiable owner"
	if err := repository.Update(ctx, item); err != nil {
		t.Fatal(err)
	}
	if err := service.ReleaseRunWorkspaceLock(ctx, 7, 23); err != nil {
		t.Fatal(err)
	}
	trees, err := gitService.ListWorktrees(ctx, item.RepositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, tree := range trees {
		if sameWorkspacePath(tree.Path, item.WorkspacePath) {
			if !tree.Locked {
				t.Fatalf("foreign lock was removed: %#v", tree)
			}
			return
		}
	}
	t.Fatal("prepared worktree was not found")
}

func TestCleanupRunWorkspaceIsFailSafe(t *testing.T) {
	tests := []struct {
		name      string
		arrange   func(context.Context, *Service, *memoryWorkspaceRepository, *gitinfra.Service, *workspacedomain.Workspace)
		force     bool
		wantClean bool
	}{
		{
			name: "clean checkout is removed",
			arrange: func(ctx context.Context, service *Service, _ *memoryWorkspaceRepository, _ *gitinfra.Service, item *workspacedomain.Workspace) {
				if err := service.ReleaseRunWorkspaceLock(ctx, item.OwnerID, item.RunID); err != nil {
					t.Fatal(err)
				}
			},
			wantClean: true,
		},
		{
			name: "dirty checkout is preserved even with force",
			arrange: func(ctx context.Context, service *Service, _ *memoryWorkspaceRepository, _ *gitinfra.Service, item *workspacedomain.Workspace) {
				if err := service.ReleaseRunWorkspaceLock(ctx, item.OwnerID, item.RunID); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(item.WorkspacePath, "dirty.txt"), []byte("dirty"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			force: true,
		},
		{
			name: "unpushed commit is preserved",
			arrange: func(ctx context.Context, service *Service, _ *memoryWorkspaceRepository, gitService *gitinfra.Service, item *workspacedomain.Workspace) {
				if err := service.ReleaseRunWorkspaceLock(ctx, item.OwnerID, item.RunID); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(item.WorkspacePath, "commit.txt"), []byte("commit"), 0o644); err != nil {
					t.Fatal(err)
				}
				if _, err := gitService.Commit(ctx, item.WorkspacePath, "test commit", []string{"commit.txt"}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "live lock is preserved",
			arrange: func(context.Context, *Service, *memoryWorkspaceRepository, *gitinfra.Service, *workspacedomain.Workspace) {
			},
		},
		{
			name: "unknown lock is preserved",
			arrange: func(ctx context.Context, _ *Service, repository *memoryWorkspaceRepository, gitService *gitinfra.Service, item *workspacedomain.Workspace) {
				if err := gitService.UnlockWorktree(ctx, item.RepositoryRoot, item.WorkspacePath); err != nil {
					t.Fatal(err)
				}
				if err := gitService.LockWorktree(ctx, item.RepositoryRoot, item.WorkspacePath, "manual lock without pid"); err != nil {
					t.Fatal(err)
				}
				item.Locked, item.LockReason = true, "manual lock without pid"
				if err := repository.Update(ctx, item); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			service, repository, gitService, root := testWorkspaceService(t)
			item, err := service.PrepareRunWorkspace(ctx, 7, 1, 100, workspacedomain.KindWorktree, "demo", test.name, nil)
			if err != nil {
				t.Fatal(err)
			}
			branch := item.BranchName
			test.arrange(ctx, service, repository, gitService, item)
			result, err := service.CleanupRunWorkspace(ctx, 7, 100, test.force)
			if err != nil {
				t.Fatal(err)
			}
			if test.wantClean {
				if result.Status != workspacedomain.StatusCleaned {
					t.Fatalf("got status %q, want cleaned: %#v", result.Status, result)
				}
				if _, statErr := os.Stat(item.WorkspacePath); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("clean checkout still exists: %v", statErr)
				}
				branches, branchErr := gitService.Branches(ctx, root)
				if branchErr != nil || !containsString(branches, branch) {
					t.Fatalf("cleanup deleted review branch %q: branches=%v err=%v", branch, branches, branchErr)
				}
				return
			}
			if result.Status != workspacedomain.StatusPreserved {
				t.Fatalf("unsafe workspace was not preserved: %#v", result)
			}
			if _, statErr := os.Stat(item.WorkspacePath); statErr != nil {
				t.Fatalf("preserved checkout is missing: %v", statErr)
			}
		})
	}
}

func TestCleanupReportsWorkspaceStatePersistenceFailures(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	gitService := gitinfra.NewService(gitinfra.Config{WorktreeDirName: ".worktrees", GitUserName: "AgentCanvas Test", GitUserEmail: "agentcanvas@example.test"})
	if _, err := gitService.EnsureRepository(ctx, root, true); err != nil {
		t.Fatal(err)
	}
	projects := &memoryProjectRepository{items: map[int64]projectdomain.Project{
		1: {BaseModel: domain.BaseModel{ID: 1, OwnerID: 7}, Slug: "demo", Name: "Demo", RepositoryRoot: root},
	}}
	repository := &updateFailWorkspaceRepository{memoryWorkspaceRepository: newMemoryWorkspaceRepository(), err: errors.New("persist failed")}
	service := NewService(projects, repository, gitService, Config{
		Enabled: true, AllowedRoots: []string{root}, WorktreeDirName: ".worktrees", MaxWorkspacesPerProject: 32, AutoInitRepository: true,
	})
	item, err := service.PrepareRunWorkspace(ctx, 7, 1, 101, workspacedomain.KindShared, "demo", "shared", nil)
	if err != nil {
		t.Fatal(err)
	}
	repository.updateCalls, repository.failAt = 0, 1
	result, err := service.CleanupRunWorkspace(ctx, 7, item.RunID, false)
	if err == nil || result == nil || result.Status != workspacedomain.StatusPreserved {
		t.Fatalf("cleanup hid preservation persistence failure: result=%#v err=%v", result, err)
	}
}

func TestPrepareWorktreeUnlocksCheckoutWhenReadyStateCannotPersist(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	gitService := gitinfra.NewService(gitinfra.Config{WorktreeDirName: ".worktrees", GitUserName: "AgentCanvas Test", GitUserEmail: "agentcanvas@example.test"})
	if _, err := gitService.EnsureRepository(ctx, root, true); err != nil {
		t.Fatal(err)
	}
	projects := &memoryProjectRepository{items: map[int64]projectdomain.Project{
		1: {BaseModel: domain.BaseModel{ID: 1, OwnerID: 7}, Slug: "demo", Name: "Demo", RepositoryRoot: root},
	}}
	persistErr := errors.New("ready state unavailable")
	repository := &updateFailWorkspaceRepository{memoryWorkspaceRepository: newMemoryWorkspaceRepository(), failAt: 1, err: persistErr}
	service := NewService(projects, repository, gitService, Config{
		Enabled: true, AllowedRoots: []string{root}, WorktreeDirName: ".worktrees", MaxWorkspacesPerProject: 32, AutoInitRepository: true,
	})
	if _, err := service.PrepareRunWorkspace(ctx, 7, 1, 102, workspacedomain.KindWorktree, "demo", "persist-failure", nil); !errors.Is(err, persistErr) {
		t.Fatalf("unexpected worktree preparation error: %v", err)
	}
	items, err := gitService.ListWorktrees(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if !sameWorkspacePath(item.Path, root) && item.Locked {
			t.Fatalf("failed ready-state persistence left a live worktree lock: %#v", item)
		}
	}
}

func TestWorktreeIgnoreAndIncludesAreSafe(t *testing.T) {
	service, _, _, root := testWorkspaceService(t)
	ignorePath := filepath.Join(root, ".gitignore")
	if err := os.WriteFile(ignorePath, []byte("\ufeffnode_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.ensureWorktreeIgnore(root); err != nil {
		t.Fatal(err)
	}
	if err := service.ensureWorktreeIgnore(root); err != nil {
		t.Fatal(err)
	}
	ignore, err := os.ReadFile(ignorePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(ignore), ".worktrees/\n") != 1 || !strings.HasPrefix(string(ignore), "\ufeff") {
		t.Fatalf(".gitignore BOM or de-duplication was not preserved: %q", ignore)
	}

	workspaceRoot := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "safe.txt"), []byte("safe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "safe-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "safe-dir", "nested.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Fatal(err)
	}
	include := "safe.txt\nsafe-dir\n../escape\noutside-link/secret.txt\n"
	if err := os.WriteFile(filepath.Join(root, ".worktreeinclude"), []byte(include), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := service.copyWorktreeIncludes(root, workspaceRoot); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{"safe.txt": "safe", "safe-dir/nested.txt": "nested"} {
		data, readErr := os.ReadFile(filepath.Join(workspaceRoot, path))
		if readErr != nil || string(data) != want {
			t.Fatalf("safe include %s not copied: %q err=%v", path, data, readErr)
		}
	}
	if _, err := os.Stat(filepath.Join(workspaceRoot, "outside-link", "secret.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("external symlink include escaped into worktree: %v", err)
	}
}

func TestEnsureWorktreeIgnoreIsCrossWorkerSafe(t *testing.T) {
	service, _, _, root := testWorkspaceService(t)
	path := filepath.Join(root, ".gitignore")
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	errorsByWorker := make(chan error, 16)
	for index := 0; index < 16; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			errorsByWorker <- service.ensureWorktreeIgnore(root)
		}()
	}
	group.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		if err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(string(data), ".worktrees/\n"); count != 1 {
		t.Fatalf("concurrent workers wrote %d ignore entries: %q", count, data)
	}
}

func TestProjectPathsAndOwnerIsolation(t *testing.T) {
	ctx := context.Background()
	service, _, _, root := testWorkspaceService(t)
	if _, err := service.GetProject(ctx, 99, 1); !errors.Is(err, agenterrors.ErrNotFound) {
		t.Fatalf("cross-owner project lookup returned %v, want not found", err)
	}

	nested := filepath.Join(root, "packages", "app")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	folder, err := service.AddFolder(ctx, 7, 1, AddFolderRequest{Path: nested, Label: "App", IsRepositoryRoot: true})
	if err != nil {
		t.Fatal(err)
	}
	if !sameWorkspacePath(folder.Path, root) {
		t.Fatalf("primary folder was not normalized to repository root: got %q want %q", folder.Path, root)
	}
}

func TestProjectFolderChangesAreAudited(t *testing.T) {
	ctx := context.Background()
	service, _, _, root := testWorkspaceService(t)
	audits := &memoryWorkspaceAuditRepository{}
	service.ConfigureAudits(audits)
	nested := filepath.Join(root, "packages", "audited")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	folder, err := service.AddFolder(ctx, 7, 1, AddFolderRequest{Path: nested, Label: "Audited"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteFolder(ctx, 7, 1, folder.ID); err != nil {
		t.Fatal(err)
	}
	if len(audits.logs) != 2 || audits.logs[0].Action != "project.folder_added" || audits.logs[1].Action != "project.folder_removed" {
		t.Fatalf("folder audit trail mismatch: %#v", audits.logs)
	}
	for index := range audits.logs {
		if audits.logs[index].OwnerID != 7 || audits.logs[index].ResourceType != "project" || audits.logs[index].ResourceID != "1" {
			t.Fatalf("folder audit ownership mismatch: %#v", audits.logs[index])
		}
	}
}

func TestRefreshGitStatusRejectsWorkspaceBranchDrift(t *testing.T) {
	ctx := context.Background()
	service, repository, _, root := testWorkspaceService(t)
	item, err := service.PrepareRunWorkspace(ctx, 7, 1, 21, workspacedomain.KindShared, "demo", "edit", nil)
	if err != nil {
		t.Fatal(err)
	}
	originalBranch := item.BranchName
	command := exec.Command("git", "checkout", "-b", "unexpected")
	command.Dir = root
	if output, commandErr := command.CombinedOutput(); commandErr != nil {
		t.Fatalf("checkout unexpected branch: %v: %s", commandErr, output)
	}
	if _, err := service.RefreshGitStatus(ctx, item); !errors.Is(err, agenterrors.ErrConflict) {
		t.Fatalf("branch drift error = %v, want conflict", err)
	}
	stored, err := repository.FindByID(ctx, 7, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.BranchName != originalBranch || stored.ErrorMessage == "" {
		t.Fatalf("workspace branch binding was overwritten: %#v", stored)
	}
}

func TestRefreshGitStatusFailurePersistsFailSafeSnapshot(t *testing.T) {
	ctx := context.Background()
	service, repository, _, _ := testWorkspaceService(t)
	item, err := service.PrepareRunWorkspace(ctx, 7, 1, 70, workspacedomain.KindWorktree, "demo", "status-failure", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(item.WorkspacePath, ".git")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.RefreshGitStatus(ctx, item); err == nil {
		t.Fatal("expected Git status failure after removing the worktree Git binding")
	}
	stored, err := repository.FindByRunID(ctx, item.OwnerID, item.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Dirty || !stored.HasUnpushedCommits || stored.LastCheckedAt == nil || stored.ErrorMessage == "" {
		t.Fatalf("status failure was not persisted as fail-safe state: %#v", stored)
	}
}

func TestSharedChildCommitDoesNotRebindPhysicalWorkspaceRow(t *testing.T) {
	ctx := context.Background()
	service, repository, _, root := testWorkspaceService(t)
	rootWorkspace, err := service.PrepareRunWorkspace(ctx, 7, 1, 30, workspacedomain.KindShared, "demo", "root", nil)
	if err != nil {
		t.Fatal(err)
	}
	childView := *rootWorkspace
	childView.RunID = 31
	if err := os.WriteFile(filepath.Join(root, "child.txt"), []byte("child\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Commit(ctx, &childView, "feat: add child file", []string{"child.txt"}); err != nil {
		t.Fatal(err)
	}
	stored, err := repository.FindByID(ctx, 7, rootWorkspace.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.RunID != rootWorkspace.RunID || childView.RunID != 31 || childView.HeadSHA == rootWorkspace.HeadSHA {
		t.Fatalf("shared child commit corrupted workspace ownership or status: root=%#v child=%#v stored=%#v", rootWorkspace, childView, stored)
	}
}

func TestCommitReturnsPostCommitStatusPersistenceFailure(t *testing.T) {
	ctx := context.Background()
	service, repository, _, root := testWorkspaceService(t)
	item, err := service.PrepareRunWorkspace(ctx, 7, 1, 32, workspacedomain.KindShared, "demo", "commit-persistence", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "persist.txt"), []byte("persist\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	persistErr := errors.New("workspace update unavailable")
	service.workspaces = &updateFailWorkspaceRepository{memoryWorkspaceRepository: repository, alwaysFail: true, err: persistErr}

	result, err := service.Commit(ctx, item, "test: persist commit status", []string{"persist.txt"})
	if result.Hash == "" || !errors.Is(err, persistErr) {
		t.Fatalf("Commit() result=%+v error=%v, want committed hash and persistence error", result, err)
	}
}

func TestCreateProjectRejectsNonexistentSymlinkEscape(t *testing.T) {
	ctx := context.Background()
	allowed := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(allowed, "escape")); err != nil {
		t.Fatal(err)
	}
	projects := &memoryProjectRepository{items: make(map[int64]projectdomain.Project)}
	workspaces := newMemoryWorkspaceRepository()
	gitService := gitinfra.NewService(gitinfra.Config{})
	service := NewService(projects, workspaces, gitService, Config{Enabled: true, AllowedRoots: []string{allowed}, AutoInitRepository: true})
	target := filepath.Join(allowed, "escape", "new-repository")
	initialize := true
	if _, err := service.CreateProject(ctx, 7, CreateProjectRequest{Name: "Escape", RepositoryRoot: target, InitializeGit: &initialize}); !errors.Is(err, agenterrors.ErrForbidden) {
		t.Fatalf("symlink escape returned %v, want forbidden", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "new-repository")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("escaped repository was unexpectedly created: %v", err)
	}
}

func TestPrepareRunWorkspaceRejectsTamperedProjectBeforeGitInit(t *testing.T) {
	ctx := context.Background()
	allowed := t.TempDir()
	outside := t.TempDir()
	projects := &memoryProjectRepository{items: map[int64]projectdomain.Project{
		1: {BaseModel: domain.BaseModel{ID: 1, OwnerID: 7}, Slug: "tampered", Name: "Tampered", RepositoryRoot: outside},
	}}
	repository := newMemoryWorkspaceRepository()
	service := NewService(projects, repository, gitinfra.NewService(gitinfra.Config{}), Config{
		Enabled: true, AllowedRoots: []string{allowed}, AutoInitRepository: true,
	})

	item, err := service.PrepareRunWorkspace(ctx, 7, 1, 42, workspacedomain.KindWorktree, "tampered", "edit", nil)
	if !errors.Is(err, agenterrors.ErrForbidden) || item == nil || item.Status != workspacedomain.StatusFailed {
		t.Fatalf("tampered project path was not rejected safely: item=%#v err=%v", item, err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, ".git")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("Git was initialized outside allowed_roots before validation: %v", statErr)
	}
}

func TestResolveExistingWorkspaceRejectsPersistedPathEscape(t *testing.T) {
	ctx := context.Background()
	service, _, _, root := testWorkspaceService(t)
	outside := t.TempDir()
	item := &workspacedomain.Workspace{BaseModel: domain.BaseModel{ID: 99, OwnerID: 7}, ProjectID: 1, RunID: 43, Kind: workspacedomain.KindShared,
		RepositoryRoot: root, WorkspacePath: outside, BranchName: "main", Status: workspacedomain.StatusReady,
	}

	if _, err := service.ResolveExistingWorkspace(ctx, item); !errors.Is(err, agenterrors.ErrForbidden) {
		t.Fatalf("persisted workspace escape returned %v, want forbidden", err)
	}
}

func TestResolveExistingWorkspaceRejectsDifferentAllowedProjectRepository(t *testing.T) {
	ctx := context.Background()
	allowed := t.TempDir()
	projectRoot := filepath.Join(allowed, "project")
	otherRoot := filepath.Join(allowed, "other")
	gitService := gitinfra.NewService(gitinfra.Config{GitUserName: "AgentCanvas Test", GitUserEmail: "agentcanvas@example.test"})
	for _, root := range []string{projectRoot, otherRoot} {
		if _, err := gitService.EnsureRepository(ctx, root, true); err != nil {
			t.Fatal(err)
		}
	}
	projects := &memoryProjectRepository{items: map[int64]projectdomain.Project{
		1: {BaseModel: domain.BaseModel{ID: 1, OwnerID: 7}, Slug: "project", Name: "Project", RepositoryRoot: projectRoot},
	}}
	service := NewService(projects, newMemoryWorkspaceRepository(), gitService, Config{Enabled: true, AllowedRoots: []string{allowed}, AutoInitRepository: true})
	status, err := gitService.Status(ctx, otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	item := &workspacedomain.Workspace{BaseModel: domain.BaseModel{ID: 100, OwnerID: 7}, ProjectID: 1, RunID: 44, Kind: workspacedomain.KindShared,
		RepositoryRoot: otherRoot, WorkspacePath: otherRoot, BranchName: status.Branch, Status: workspacedomain.StatusReady,
	}

	if _, err := service.ResolveExistingWorkspace(ctx, item); !errors.Is(err, agenterrors.ErrConflict) {
		t.Fatalf("cross-project repository binding returned %v, want conflict", err)
	}
}

func TestRefreshGitStatusRejectsUnregisteredReplacementCheckout(t *testing.T) {
	ctx := context.Background()
	service, repository, gitService, _ := testWorkspaceService(t)
	item, err := service.PrepareRunWorkspace(ctx, 7, 1, 45, workspacedomain.KindWorktree, "demo", "replacement", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := gitService.UnlockWorktree(ctx, item.RepositoryRoot, item.WorkspacePath); err != nil {
		t.Fatal(err)
	}
	if err := gitService.RemoveWorktree(ctx, item.RepositoryRoot, item.WorkspacePath, false); err != nil {
		t.Fatal(err)
	}
	if _, err := gitService.EnsureRepository(ctx, item.WorkspacePath, true); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "checkout", "-b", item.BranchName)
	command.Dir = item.WorkspacePath
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("create replacement branch: %v: %s", err, output)
	}

	if _, err := service.RefreshGitStatus(ctx, item); !errors.Is(err, agenterrors.ErrConflict) {
		t.Fatalf("unregistered replacement checkout returned %v, want conflict", err)
	}
	stored, err := repository.FindByID(ctx, 7, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ErrorMessage == "" {
		t.Fatalf("unregistered checkout failure was not persisted: %#v", stored)
	}
}

func TestPrepareRunWorkspacePersistsPreflightFailure(t *testing.T) {
	ctx := context.Background()
	allowed := t.TempDir()
	missing := filepath.Join(allowed, "missing")
	projects := &memoryProjectRepository{items: map[int64]projectdomain.Project{1: {BaseModel: domain.BaseModel{ID: 1, OwnerID: 7}, Slug: "demo", Name: "Demo", RepositoryRoot: missing}}}
	repository := newMemoryWorkspaceRepository()
	service := NewService(projects, repository, gitinfra.NewService(gitinfra.Config{}), Config{Enabled: true, AllowedRoots: []string{allowed}, AutoInitRepository: false})
	item, err := service.PrepareRunWorkspace(ctx, 7, 1, 41, workspacedomain.KindWorktree, "demo", "failure", nil)
	if err == nil || item == nil || item.ID == 0 || item.Status != workspacedomain.StatusFailed {
		t.Fatalf("workspace failure was not persisted: item=%#v err=%v", item, err)
	}
	stored, findErr := repository.FindByRunID(ctx, 7, 41)
	if findErr != nil || stored.Status != workspacedomain.StatusFailed || stored.ErrorMessage == "" {
		t.Fatalf("stored failure mismatch: item=%#v err=%v", stored, findErr)
	}
}

func TestOccupiedWorktreeBranchAndPathUseUniqueFallback(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name    string
		arrange func(*testing.T, string, string)
	}{
		{name: "branch", arrange: func(t *testing.T, root, branch string) {
			cmd := exec.Command("git", "branch", branch)
			cmd.Dir = root
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("create occupied branch: %v: %s", err, output)
			}
		}},
		{name: "path", arrange: func(t *testing.T, root, _ string) {
			if err := os.MkdirAll(filepath.Join(root, ".worktrees", "202-occupied"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _, _, root := testWorkspaceService(t)
			branch := gitinfra.BranchName("demo", 202, "occupied")
			test.arrange(t, root, branch)
			item, err := service.PrepareRunWorkspace(ctx, 7, 1, 202, workspacedomain.KindWorktree, "demo", "occupied", nil)
			if err != nil {
				t.Fatal(err)
			}
			if item.BranchName == branch || filepath.Base(item.WorkspacePath) == "202-occupied" {
				t.Fatalf("occupied target was reused: %#v", item)
			}
		})
	}
}

func TestConcurrentWorkspaceReservationConflictUsesUniqueFallback(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	gitService := gitinfra.NewService(gitinfra.Config{GitUserName: "AgentCanvas Test", GitUserEmail: "agentcanvas@example.test"})
	if _, err := gitService.EnsureRepository(ctx, root, true); err != nil {
		t.Fatal(err)
	}
	projects := &memoryProjectRepository{items: map[int64]projectdomain.Project{
		1: {BaseModel: domain.BaseModel{ID: 1, OwnerID: 7}, Slug: "demo", Name: "Demo", RepositoryRoot: root},
	}}
	repository := &conflictOnceWorkspaceRepository{memoryWorkspaceRepository: newMemoryWorkspaceRepository()}
	service := NewService(projects, repository, gitService, Config{Enabled: true, AllowedRoots: []string{root}, WorktreeDirName: ".worktrees", MaxWorkspacesPerProject: 32})
	item, err := service.PrepareRunWorkspace(ctx, 7, 1, 220, workspacedomain.KindWorktree, "demo", "parallel edit", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !repository.conflicted || !strings.HasSuffix(item.BranchName, "-2") || !strings.HasSuffix(item.WorkspacePath, "-2") {
		t.Fatalf("reservation conflict did not persist a unique fallback: %#v", item)
	}
	trees, err := gitService.ListWorktrees(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tree := range trees {
		if sameWorkspacePath(tree.Path, item.WorkspacePath) && tree.Branch == item.BranchName {
			found = true
		}
	}
	if !found {
		t.Fatalf("persisted fallback does not match a real checkout: %#v trees=%#v", item, trees)
	}
}

func TestRecoverAfterRestartCompletesCreatingWorktree(t *testing.T) {
	ctx := context.Background()
	service, repository, gitService, root := testWorkspaceService(t)
	baseSHA, err := gitService.Head(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	item := &workspacedomain.Workspace{BaseModel: domain.BaseModel{OwnerID: 7}, ProjectID: 1, RunID: 303, Kind: workspacedomain.KindWorktree,
		RepositoryRoot: root, WorkspacePath: filepath.Join(root, ".worktrees", "303-recover"),
		BranchName: "demo/303-recover", BaseRef: "HEAD", BaseSHA: baseSHA, Status: workspacedomain.StatusCreating,
	}
	if err := repository.Create(ctx, item); err != nil {
		t.Fatal(err)
	}
	if err := service.RecoverAfterRestart(ctx, 0, 0); err != nil {
		t.Fatal(err)
	}
	recovered, err := repository.FindByRunID(ctx, 7, 303)
	if err != nil || recovered.Status != workspacedomain.StatusReady {
		t.Fatalf("creating workspace was not recovered: item=%#v err=%v", recovered, err)
	}
	if _, err := os.Stat(recovered.WorkspacePath); err != nil {
		t.Fatalf("recovered checkout is missing: %v", err)
	}
	prepared, err := service.PrepareRunWorkspace(ctx, 7, 1, 303, workspacedomain.KindWorktree, "demo", "recover", nil)
	if err != nil || !prepared.Locked {
		t.Fatalf("recovered workspace was not locked for retry: item=%#v err=%v", prepared, err)
	}
}

func TestRecoverAfterRestartIsUnboundedAndAggregatesErrors(t *testing.T) {
	ctx := context.Background()
	service, repository, gitService, root := testWorkspaceService(t)
	status, err := gitService.Status(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	valid := &workspacedomain.Workspace{BaseModel: domain.BaseModel{OwnerID: 7}, ProjectID: 1, RunID: 304, Kind: workspacedomain.KindShared,
		RepositoryRoot: root, WorkspacePath: root, BranchName: status.Branch, Status: workspacedomain.StatusReady,
	}
	invalidOne := &workspacedomain.Workspace{BaseModel: domain.BaseModel{OwnerID: 7}, ProjectID: 1, RunID: 305, Kind: workspacedomain.KindShared,
		RepositoryRoot: root, WorkspacePath: filepath.Join(root, "missing-one"), BranchName: status.Branch, Status: workspacedomain.StatusReady,
	}
	invalidTwo := &workspacedomain.Workspace{BaseModel: domain.BaseModel{OwnerID: 7}, ProjectID: 1, RunID: 306, Kind: workspacedomain.KindShared,
		RepositoryRoot: root, WorkspacePath: filepath.Join(root, "missing-two"), BranchName: status.Branch, Status: workspacedomain.StatusCreating,
	}
	for _, item := range []*workspacedomain.Workspace{valid, invalidOne, invalidTwo} {
		if err := repository.Create(ctx, item); err != nil {
			t.Fatal(err)
		}
	}

	err = service.RecoverAfterRestart(ctx, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "run 305") || !strings.Contains(err.Error(), "run 306") {
		t.Fatalf("expected joined recovery errors for both invalid workspaces, got %v", err)
	}
	if repository.lastRecoverableLimit != 0 {
		t.Fatalf("startup recovery must request every workspace, limit=%d", repository.lastRecoverableLimit)
	}
	recovered, findErr := repository.FindByRunID(ctx, 7, valid.RunID)
	if findErr != nil || recovered.LastCheckedAt == nil {
		t.Fatalf("valid workspace was not recovered after another item failed: item=%#v err=%v", recovered, findErr)
	}
}

func TestPruneStaleWorkspacesContinuesAndAggregatesErrors(t *testing.T) {
	ctx := context.Background()
	service, repository, _, root := testWorkspaceService(t)
	failing := &workspacedomain.Workspace{BaseModel: domain.BaseModel{OwnerID: 7}, ProjectID: 1, RunID: 401, Kind: workspacedomain.KindShared,
		RepositoryRoot: root, WorkspacePath: root, Status: workspacedomain.StatusReady,
	}
	successful := &workspacedomain.Workspace{BaseModel: domain.BaseModel{OwnerID: 7}, ProjectID: 1, RunID: 402, Kind: workspacedomain.KindShared,
		RepositoryRoot: root, WorkspacePath: root, Status: workspacedomain.StatusReady,
	}
	for _, item := range []*workspacedomain.Workspace{failing, successful} {
		if err := repository.Create(ctx, item); err != nil {
			t.Fatal(err)
		}
		stored := repository.items[item.ID]
		stored.UpdatedAt = time.Now().UTC().Add(-48 * time.Hour)
		repository.items[item.ID] = stored
	}
	sentinel := errors.New("workspace lookup failed")
	service.workspaces = &findFailWorkspaceRepository{
		memoryWorkspaceRepository: repository,
		failRunID:                 failing.RunID,
		err:                       sentinel,
	}

	err := service.PruneStaleWorkspaces(ctx)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected prune error to be returned, got %v", err)
	}
	preserved, findErr := repository.FindByRunID(ctx, 7, successful.RunID)
	if findErr != nil || preserved.Status != workspacedomain.StatusPreserved {
		t.Fatalf("prune stopped before processing remaining workspace: item=%#v err=%v", preserved, findErr)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
