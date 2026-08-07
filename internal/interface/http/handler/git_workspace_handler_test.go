package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentusecase "agentcanvas/internal/application/agent_usecase"
	workspaceusecase "agentcanvas/internal/application/workspace_usecase"
	agentdomain "agentcanvas/internal/domain/agent"
	projectdomain "agentcanvas/internal/domain/project"
	workspacedomain "agentcanvas/internal/domain/workspace"
	gitinfra "agentcanvas/internal/infrastructure/git"
	agenterrors "agentcanvas/internal/pkg/errors"

	"github.com/gin-gonic/gin"
)

type handlerProjectRepository struct {
	item projectdomain.Project
}

func (r *handlerProjectRepository) Create(_ context.Context, item *projectdomain.Project) error {
	r.item = *item
	if r.item.ID == 0 {
		r.item.ID = 1
		item.ID = r.item.ID
	}
	return nil
}
func (r *handlerProjectRepository) CreateWithPrimaryFolder(ctx context.Context, item *projectdomain.Project, folder *projectdomain.ProjectFolder) error {
	if err := r.Create(ctx, item); err != nil {
		return err
	}
	folder.OwnerID = item.OwnerID
	folder.ProjectID = item.ID
	folder.Path = item.PrimaryPath
	folder.IsPrimary = true
	return r.AddFolder(ctx, folder)
}
func (r *handlerProjectRepository) ListByOwner(_ context.Context, ownerID int64, includeArchived bool) ([]projectdomain.Project, error) {
	if r.item.OwnerID != ownerID || (r.item.Archived && !includeArchived) {
		return []projectdomain.Project{}, nil
	}
	return []projectdomain.Project{r.item}, nil
}
func (r *handlerProjectRepository) FindByID(_ context.Context, ownerID, id int64) (*projectdomain.Project, error) {
	if r.item.ID != id || r.item.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	item := r.item
	return &item, nil
}
func (r *handlerProjectRepository) Update(_ context.Context, item *projectdomain.Project) error {
	if item.ID != r.item.ID || item.OwnerID != r.item.OwnerID {
		return agenterrors.ErrNotFound
	}
	r.item = *item
	return nil
}
func (r *handlerProjectRepository) Archive(_ context.Context, ownerID, id int64) error {
	if r.item.ID != id || r.item.OwnerID != ownerID {
		return agenterrors.ErrNotFound
	}
	r.item.Archived = true
	return nil
}
func (r *handlerProjectRepository) ListFolders(_ context.Context, ownerID, projectID int64) ([]projectdomain.ProjectFolder, error) {
	if r.item.ID != projectID || r.item.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	return append([]projectdomain.ProjectFolder(nil), r.item.Folders...), nil
}
func (r *handlerProjectRepository) AddFolder(_ context.Context, item *projectdomain.ProjectFolder) error {
	if item.OwnerID != r.item.OwnerID || item.ProjectID != r.item.ID {
		return agenterrors.ErrNotFound
	}
	if item.ID == 0 {
		item.ID = int64(len(r.item.Folders) + 1)
	}
	r.item.Folders = append(r.item.Folders, *item)
	return nil
}
func (r *handlerProjectRepository) AddPrimaryFolder(ctx context.Context, item *projectdomain.ProjectFolder) error {
	if err := r.AddFolder(ctx, item); err != nil {
		return err
	}
	return r.SetPrimaryFolder(ctx, item.OwnerID, item.ProjectID, item.ID)
}
func (r *handlerProjectRepository) DeleteFolder(_ context.Context, ownerID, projectID, folderID int64) error {
	if r.item.ID != projectID || r.item.OwnerID != ownerID {
		return agenterrors.ErrNotFound
	}
	for index := range r.item.Folders {
		if r.item.Folders[index].ID == folderID {
			r.item.Folders = append(r.item.Folders[:index], r.item.Folders[index+1:]...)
			return nil
		}
	}
	return agenterrors.ErrNotFound
}
func (r *handlerProjectRepository) SetPrimaryFolder(_ context.Context, ownerID, projectID, folderID int64) error {
	if r.item.ID != projectID || r.item.OwnerID != ownerID {
		return agenterrors.ErrNotFound
	}
	for index := range r.item.Folders {
		r.item.Folders[index].IsPrimary = r.item.Folders[index].ID == folderID
		if r.item.Folders[index].IsPrimary {
			r.item.PrimaryPath = r.item.Folders[index].Path
		}
	}
	return nil
}

type handlerWorkspaceRepository struct {
	item *workspacedomain.Workspace
}

func (r *handlerWorkspaceRepository) Create(_ context.Context, item *workspacedomain.Workspace) error {
	copy := *item
	if copy.ID == 0 {
		copy.ID = 70
		item.ID = copy.ID
	}
	r.item = &copy
	return nil
}
func (r *handlerWorkspaceRepository) FindByID(_ context.Context, ownerID, id int64) (*workspacedomain.Workspace, error) {
	if r.item == nil || r.item.ID != id || r.item.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	item := *r.item
	return &item, nil
}
func (r *handlerWorkspaceRepository) FindByRunID(_ context.Context, ownerID, runID int64) (*workspacedomain.Workspace, error) {
	if r.item == nil || r.item.RunID != runID || r.item.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	item := *r.item
	return &item, nil
}
func (r *handlerWorkspaceRepository) ListByProject(_ context.Context, ownerID, projectID int64) ([]workspacedomain.Workspace, error) {
	if r.item == nil || r.item.OwnerID != ownerID || r.item.ProjectID != projectID {
		return []workspacedomain.Workspace{}, nil
	}
	return []workspacedomain.Workspace{*r.item}, nil
}
func (r *handlerWorkspaceRepository) ListRecoverable(context.Context, int) ([]workspacedomain.Workspace, error) {
	if r.item == nil {
		return []workspacedomain.Workspace{}, nil
	}
	return []workspacedomain.Workspace{*r.item}, nil
}
func (r *handlerWorkspaceRepository) Update(_ context.Context, item *workspacedomain.Workspace) error {
	if r.item == nil || r.item.ID != item.ID || r.item.OwnerID != item.OwnerID {
		return agenterrors.ErrNotFound
	}
	copy := *item
	r.item = &copy
	return nil
}
func (r *handlerWorkspaceRepository) ListStale(context.Context, time.Time, int) ([]workspacedomain.Workspace, error) {
	return []workspacedomain.Workspace{}, nil
}

type handlerRunRepository struct {
	items map[int64]agentdomain.Run
}

type handlerRunEventRepository struct {
	items []agentdomain.RunEvent
}

func (r *handlerRunEventRepository) Create(_ context.Context, item *agentdomain.RunEvent) error {
	item.ID = int64(len(r.items) + 1)
	r.items = append(r.items, *item)
	return nil
}

func (r *handlerRunEventRepository) ListByRun(_ context.Context, ownerID, runID int64) ([]agentdomain.RunEvent, error) {
	items := make([]agentdomain.RunEvent, 0, len(r.items))
	for index := range r.items {
		if r.items[index].OwnerID == ownerID && r.items[index].RunID == runID {
			items = append(items, r.items[index])
		}
	}
	return items, nil
}

func (r *handlerRunRepository) Create(_ context.Context, item *agentdomain.Run) error {
	if r.items == nil {
		r.items = map[int64]agentdomain.Run{}
	}
	r.items[item.ID] = *item
	return nil
}
func (r *handlerRunRepository) FindByID(_ context.Context, ownerID, id int64) (*agentdomain.Run, error) {
	item, ok := r.items[id]
	if !ok || item.OwnerID != ownerID {
		return nil, agenterrors.ErrNotFound
	}
	return &item, nil
}
func (r *handlerRunRepository) ListByParent(_ context.Context, ownerID, parentID int64) ([]agentdomain.Run, error) {
	items := make([]agentdomain.Run, 0)
	for _, item := range r.items {
		if item.OwnerID == ownerID && item.ParentRunID != nil && *item.ParentRunID == parentID {
			items = append(items, item)
		}
	}
	return items, nil
}
func (r *handlerRunRepository) Update(_ context.Context, item *agentdomain.Run) error {
	if _, ok := r.items[item.ID]; !ok {
		return agenterrors.ErrNotFound
	}
	r.items[item.ID] = *item
	return nil
}

func handlerGitRepository(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = root
		command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C", "LANG=C")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	runGit("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "README.md")
	runGit("-c", "user.name=AgentCanvas Test", "-c", "user.email=test@example.com", "commit", "-m", "Initial commit")
	return root, runGit("rev-parse", "HEAD")
}

func handlerContext(method, path string, ownerID int64, params gin.Params, body string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		ctx.Request.Header.Set("Content-Type", "application/json")
	}
	ctx.Params = params
	ctx.Set("user_id", ownerID)
	return ctx, recorder
}

func handlerWorkspaceServices(t *testing.T, kind string) (*ProjectHandler, *AgentHandler, *handlerProjectRepository, *handlerWorkspaceRepository, string) {
	t.Helper()
	root, head := handlerGitRepository(t)
	projectRepo := &handlerProjectRepository{item: projectdomain.Project{ID: 11, OwnerID: 7, Slug: "agent-canvas", Name: "AgentCanvas", PrimaryPath: root}}
	workspaceID := int64(70)
	workspaceRepo := &handlerWorkspaceRepository{item: &workspacedomain.Workspace{
		ID: workspaceID, OwnerID: 7, ProjectID: 11, RunID: 20, Kind: kind,
		RepositoryRoot: root, WorkspacePath: root, BranchName: "main", BaseRef: "HEAD", BaseSHA: head, HeadSHA: head, Status: workspacedomain.StatusReady,
	}}
	gitService := gitinfra.NewService(gitinfra.Config{GitUserName: "AgentCanvas Test", GitUserEmail: "test@example.com"})
	workspaceService := workspaceusecase.NewService(projectRepo, workspaceRepo, gitService, workspaceusecase.Config{Enabled: true, AllowedRoots: []string{root}})
	runRepo := &handlerRunRepository{items: map[int64]agentdomain.Run{20: {ID: 20, OwnerID: 7, AgentID: 3, WorkspaceID: &workspaceID, Status: agentdomain.RunStatusSucceeded}}}
	agentService := agentusecase.NewService(nil, nil, nil, nil, runRepo, nil, nil, nil, nil)
	agentService.ConfigureWorkspace(workspaceService)
	agentHandler := NewAgentHandler(agentService)
	agentHandler.ConfigureWorkspace(workspaceService)
	return NewProjectHandler(workspaceService), agentHandler, projectRepo, workspaceRepo, root
}

func TestProjectHandlerEnforcesOwnerIsolation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	projectHandler, _, _, _, _ := handlerWorkspaceServices(t, workspacedomain.KindShared)

	ctx, recorder := handlerContext(http.MethodGet, "/projects/11", 8, gin.Params{{Key: "id", Value: "11"}}, "")
	projectHandler.Get(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("cross-owner Project read status = %d, want %d: %s", recorder.Code, http.StatusNotFound, recorder.Body.String())
	}
}

func TestProjectHandlerExposesGitStatusBranchesAndWorktrees(t *testing.T) {
	gin.SetMode(gin.TestMode)
	projectHandler, _, _, _, _ := handlerWorkspaceServices(t, workspacedomain.KindShared)

	tests := []struct {
		path     string
		handle   func(*gin.Context)
		contains string
	}{
		{path: "/projects/11/git/status", handle: projectHandler.GitStatus, contains: `"branch":"main"`},
		{path: "/projects/11/git/branches", handle: projectHandler.GitBranches, contains: `"main"`},
		{path: "/projects/11/git/worktrees", handle: projectHandler.GitWorktrees, contains: `"branch":"main"`},
	}
	for _, test := range tests {
		ctx, recorder := handlerContext(http.MethodGet, test.path, 7, gin.Params{{Key: "id", Value: "11"}}, "")
		test.handle(ctx)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), test.contains) {
			t.Fatalf("%s response = %d %s", test.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestRunWorkspaceGitAndLifecycleHandlers(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, agentHandler, _, workspaceRepo, root := handlerWorkspaceServices(t, workspacedomain.KindShared)
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	readTests := []struct {
		path     string
		handle   func(*gin.Context)
		contains string
	}{
		{path: "/runs/20/workspace", handle: agentHandler.GetRunWorkspace, contains: `"workspace_path":"` + root + `"`},
		{path: "/runs/20/git/status", handle: agentHandler.RunGitStatus, contains: `"dirty":true`},
		{path: "/runs/20/git/diff", handle: agentHandler.RunGitDiff, contains: `+after`},
		{path: "/runs/20/git/log?limit=5", handle: agentHandler.RunGitLog, contains: `Initial commit`},
	}
	for _, test := range readTests {
		ctx, recorder := handlerContext(http.MethodGet, test.path, 7, gin.Params{{Key: "id", Value: "20"}}, "")
		test.handle(ctx)
		if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), test.contains) {
			t.Fatalf("%s response = %d %s", test.path, recorder.Code, recorder.Body.String())
		}
	}

	ctx, recorder := handlerContext(http.MethodPost, "/workspaces/70/refresh", 7, gin.Params{{Key: "id", Value: "70"}}, "")
	agentHandler.RefreshWorkspace(ctx)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"dirty":true`) {
		t.Fatalf("refresh response = %d %s", recorder.Code, recorder.Body.String())
	}

	ctx, recorder = handlerContext(http.MethodPost, "/runs/20/git/commit", 7, gin.Params{{Key: "id", Value: "20"}}, `{"message":"feat: update readme","paths":["README.md"]}`)
	agentHandler.RunGitCommit(ctx)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"message":"feat: update readme"`) {
		t.Fatalf("commit response = %d %s", recorder.Code, recorder.Body.String())
	}
	if workspaceRepo.item.HeadSHA == workspaceRepo.item.BaseSHA || workspaceRepo.item.Dirty {
		t.Fatalf("commit did not refresh workspace HEAD and dirty state: %#v", workspaceRepo.item)
	}

	ctx, recorder = handlerContext(http.MethodPost, "/workspaces/70/cleanup", 7, gin.Params{{Key: "id", Value: "70"}}, "")
	agentHandler.CleanupWorkspace(ctx)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"status":"preserved"`) || !strings.Contains(recorder.Body.String(), "shared workspace is never removed") {
		t.Fatalf("cleanup response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestRunGitStatusFailureEmitsFailSafeEvent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, baseHandler, _, workspaceRepo, root := handlerWorkspaceServices(t, workspacedomain.KindShared)
	events := &handlerRunEventRepository{}
	workspaceID := workspaceRepo.item.ID
	runs := &handlerRunRepository{items: map[int64]agentdomain.Run{
		20: {ID: 20, OwnerID: 7, AgentID: 3, WorkspaceID: &workspaceID, Status: agentdomain.RunStatusSucceeded},
	}}
	service := agentusecase.NewService(nil, nil, nil, nil, runs, events, nil, nil, nil)
	service.ConfigureWorkspace(baseHandler.workspace)
	handler := NewAgentHandler(service)
	handler.ConfigureWorkspace(baseHandler.workspace)
	if err := os.Rename(filepath.Join(root, ".git"), filepath.Join(root, ".git-disabled")); err != nil {
		t.Fatal(err)
	}

	ctx, recorder := handlerContext(http.MethodGet, "/runs/20/git/status", 7, gin.Params{{Key: "id", Value: "20"}}, "")
	handler.RunGitStatus(ctx)
	if recorder.Code == http.StatusOK {
		t.Fatalf("Git status failure returned 200: %s", recorder.Body.String())
	}
	if len(events.items) != 1 || events.items[0].EventType != "git.status_changed" {
		t.Fatalf("Git status failure event missing: %#v", events.items)
	}
	var payload map[string]any
	if err := json.Unmarshal(events.items[0].PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"workspace_id", "project_id", "run_id", "kind", "repo_root", "path", "branch", "base_sha", "head_sha", "dirty", "unpushed", "status", "error"} {
		if _, ok := payload[key]; !ok {
			t.Fatalf("Git status failure payload is missing %q: %#v", key, payload)
		}
	}
	if payload["dirty"] != true || payload["unpushed"] != true || payload["error"] == "" {
		t.Fatalf("Git status failure was not fail-safe: %#v", payload)
	}
}

func TestWorkspaceEventPayloadUsesCanonicalHeadSHAField(t *testing.T) {
	payload := workspaceEventPayload(&workspacedomain.Workspace{ID: 70, ProjectID: 11, RunID: 20, HeadSHA: "abc123"})
	if payload["head_sha"] != "abc123" {
		t.Fatalf("workspace event head_sha = %#v", payload["head_sha"])
	}
	if _, exists := payload["head"]; exists {
		t.Fatalf("workspace event exposed non-canonical head field: %#v", payload)
	}
}

func TestRunWorkspaceHandlerRejectsMissingAndCrossOwnerWorkspace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	_, agentHandler, _, workspaceRepo, _ := handlerWorkspaceServices(t, workspacedomain.KindShared)

	ctx, recorder := handlerContext(http.MethodGet, "/runs/20/workspace", 8, gin.Params{{Key: "id", Value: "20"}}, "")
	agentHandler.GetRunWorkspace(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("cross-owner Run workspace status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}

	workspaceRepo.item = nil
	ctx, recorder = handlerContext(http.MethodGet, "/runs/20/workspace", 7, gin.Params{{Key: "id", Value: "20"}}, "")
	agentHandler.GetRunWorkspace(ctx)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing Run workspace status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
}
