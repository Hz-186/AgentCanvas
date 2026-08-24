package toolruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"agentcanvas/internal/domain/audit"
	gitinfra "agentcanvas/internal/infrastructure/git"
)

type memoryWorkspaceAuditRepository struct{ logs []audit.Log }

func (r *memoryWorkspaceAuditRepository) Create(_ context.Context, item *audit.Log) error {
	r.logs = append(r.logs, *item)
	return nil
}
func (r *memoryWorkspaceAuditRepository) ListByOwner(context.Context, int64, int, int) ([]audit.Log, error) {
	return append([]audit.Log(nil), r.logs...), nil
}

func testWorkspace(t *testing.T) (string, ToolRunContext) {
	t.Helper()
	root := t.TempDir()
	return root, ToolRunContext{RunID: 1, Workspace: &WorkspaceContext{WorkspacePath: root, RepositoryRoot: root, FileWriteEnabled: true, GitEnabled: false, ExecEnabled: false}}
}

func TestFileToolsHashAndPatchConflict(t *testing.T) {
	root, rc := testWorkspace(t)
	rc.Workspace.ID, rc.Workspace.ProjectID, rc.Workspace.Kind = 10, 20, "worktree"
	rc.Workspace.BranchName, rc.Workspace.BaseSHA, rc.Workspace.HeadSHA = "demo/1-edit", "base-sha", "head-sha"
	var eventType string
	var eventPayload map[string]any
	rc.EmitEvent = func(_ context.Context, kind string, payload map[string]any) error {
		eventType, eventPayload = kind, payload
		return nil
	}
	tool := FileTool{Kind: "write_file"}
	result, err := tool.Execute(context.Background(), rc, json.RawMessage(`{"path":"note.txt","content":"hello"}`))
	if err != nil || result == nil || result.IsError {
		t.Fatalf("write failed: %v %#v", err, result)
	}
	data, _ := os.ReadFile(filepath.Join(root, "note.txt"))
	var payload map[string]any
	if err := json.Unmarshal(result.ContentJSON, &payload); err != nil {
		t.Fatal(err)
	}
	hash, _ := payload["after_sha256"].(string)
	if hash == "" || string(data) != "hello" || !rc.Workspace.Dirty {
		t.Fatalf("unexpected write result: %#v", payload)
	}
	if eventType != "workspace.status_changed" {
		t.Fatalf("file mutation event type = %q", eventType)
	}
	for _, key := range []string{"workspace_id", "project_id", "run_id", "kind", "repository_root", "workspace_path", "branch_name", "base_sha", "head_sha", "dirty", "has_unpushed_commits", "status", "error_message", "mutation"} {
		if _, ok := eventPayload[key]; !ok {
			t.Fatalf("file mutation event is missing %q: %#v", key, eventPayload)
		}
	}
	if eventPayload["head_sha"] != "head-sha" {
		t.Fatalf("file mutation event lost the workspace HEAD snapshot: %#v", eventPayload)
	}
	_, err = tool.Execute(context.Background(), rc, json.RawMessage(`{"path":"note.txt","content":"changed","expected_sha256":"bad"}`))
	if err == nil {
		t.Fatal("expected optimistic concurrency conflict")
	}
	patch := FileTool{Kind: "patch_file"}
	result, err = patch.Execute(context.Background(), rc, json.RawMessage(`{"path":"note.txt","old_string":"hello","new_string":"world","expected_sha256":"`+hash+`"}`))
	if err != nil || result == nil {
		t.Fatalf("patch failed: %v %#v", err, result)
	}
	data, _ = os.ReadFile(filepath.Join(root, "note.txt"))
	if string(data) != "world" {
		t.Fatalf("got %q after patch", data)
	}
	moveResult, err := (FileTool{Kind: "move_file"}).Execute(context.Background(), rc, json.RawMessage(`{"from":"note.txt","to":"moved.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	var movePayload map[string]any
	if err := json.Unmarshal(moveResult.ContentJSON, &movePayload); err != nil {
		t.Fatal(err)
	}
	if movePayload["before_sha256"] == "" || movePayload["after_sha256"] != movePayload["before_sha256"] {
		t.Fatalf("move result is missing before/after hashes: %#v", movePayload)
	}
	deleteResult, err := (FileTool{Kind: "delete_file"}).Execute(context.Background(), rc, json.RawMessage(`{"path":"moved.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	var deletePayload map[string]any
	if err := json.Unmarshal(deleteResult.ContentJSON, &deletePayload); err != nil {
		t.Fatal(err)
	}
	if deletePayload["before_sha256"] == "" || deletePayload["after_sha256"] != "" {
		t.Fatalf("delete result is missing before/after hashes: %#v", deletePayload)
	}
}

func TestAuditedFileToolRedactsSourceContent(t *testing.T) {
	root, rc := testWorkspace(t)
	rc.OwnerID, rc.AgentID, rc.Workspace.ID = 7, 9, 11
	repository := &memoryWorkspaceAuditRepository{}
	tool := AuditedTool{Tool: FileTool{Kind: "write_file"}, Audits: repository}
	input := json.RawMessage(`{"path":"note.txt","content":"private source body"}`)
	if _, err := tool.Execute(context.Background(), rc, input); err != nil {
		t.Fatal(err)
	}
	if len(repository.logs) != 1 {
		t.Fatalf("got %d audit logs, want 1", len(repository.logs))
	}
	detail := string(repository.logs[0].DetailJSON)
	if strings.Contains(detail, "private source body") || !strings.Contains(detail, "content_sha256") || !strings.Contains(detail, `"run_id":1`) {
		t.Fatalf("source content was not safely summarized: %s", detail)
	}
	data, err := os.ReadFile(filepath.Join(root, "note.txt"))
	if err != nil || string(data) != "private source body" {
		t.Fatalf("audited tool changed write behavior: %q err=%v", data, err)
	}
}

func TestWorkspaceExecCapsOutputWhileCommandRuns(t *testing.T) {
	_, rc := testWorkspace(t)
	rc.Workspace.ExecEnabled = true
	tool := WorkspaceExecTool{MaxOutputBytes: 8, Timeout: time.Second}
	result, err := tool.Execute(context.Background(), rc, json.RawMessage(`{"command":"printf 12345678901234567890"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.ContentJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["output"] != "12345678" || payload["truncated"] != true {
		t.Fatalf("workspace_exec output was not capped: %#v", payload)
	}
}

func TestWorkspaceExecFixesCWDAndRejectsLiteralPathEscape(t *testing.T) {
	root, rc := testWorkspace(t)
	rc.Workspace.ExecEnabled = true
	tool := WorkspaceExecTool{Timeout: time.Second}
	result, err := tool.Execute(context.Background(), rc, json.RawMessage(`{"command":"printf '%s\\n%s' \"$PWD\" \"$HOME\""}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.ContentJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if output := payload["output"]; output != root+"\n"+root {
		t.Fatalf("workspace command environment escaped cwd: %#v", payload)
	}
	commands := []string{
		"cd .. && pwd",
		"printf x > ../outside.txt",
		"printf x > /tmp/agentcanvas-outside.txt",
		`python3 -c "open('/tmp/agentcanvas-outside.txt','w').write('x')"`,
	}
	for _, command := range commands {
		if _, err := tool.Execute(context.Background(), rc, mustJSON(map[string]any{"command": command})); err == nil {
			t.Fatalf("workspace_exec accepted escaping command %q", command)
		}
	}
}

func TestWorkspaceExecScrubsServiceSecretsFromChildEnvironment(t *testing.T) {
	_, rc := testWorkspace(t)
	rc.Workspace.ExecEnabled = true
	t.Setenv("AGENTCANVAS_TEST_SECRET", "must-not-reach-agent-command")
	t.Setenv("OPENAI_API_KEY", "sk-must-not-reach-agent-command")
	tool := WorkspaceExecTool{Timeout: 5 * time.Second, MaxOutputBytes: 64 * 1024}
	result, err := tool.Execute(context.Background(), rc, mustJSON(map[string]any{"command": "env"}))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal(result.ContentJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(payload.Output, "AGENTCANVAS_TEST_SECRET") || strings.Contains(payload.Output, "OPENAI_API_KEY") || strings.Contains(payload.Output, "must-not-reach-agent-command") {
		t.Fatalf("workspace_exec leaked service secrets: %s", payload.Output)
	}
	if !strings.Contains(payload.Output, "HOME="+rc.Workspace.WorkspacePath) || !strings.Contains(payload.Output, "GIT_TERMINAL_PROMPT=0") {
		t.Fatalf("workspace_exec safe environment is incomplete: %s", payload.Output)
	}
}

func TestGitToolsEnforceApprovalMetadataBranchBindingAndCompleteEvents(t *testing.T) {
	root, rc := testWorkspace(t)
	service := gitinfra.NewService(gitinfra.Config{GitUserName: "AgentCanvas Test", GitUserEmail: "test@example.com"})
	if _, err := service.EnsureRepository(context.Background(), root, true); err != nil {
		t.Fatal(err)
	}
	branch := service.CurrentBranch(context.Background(), root)
	base, err := service.Head(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	rc.OwnerID, rc.RunID = 7, 20
	rc.Workspace.ID, rc.Workspace.ProjectID, rc.Workspace.RunID = 70, 11, 20
	rc.Workspace.Kind, rc.Workspace.BranchName, rc.Workspace.BaseSHA, rc.Workspace.GitEnabled = "worktree", branch, base, true
	var eventType string
	var eventPayload map[string]any
	rc.EmitEvent = func(_ context.Context, kind string, payload map[string]any) error {
		eventType, eventPayload = kind, payload
		return nil
	}

	commitTool := GitTool{Kind: "git_commit", Git: service}
	metadata := commitTool.Metadata()
	if metadata.RiskLevel != RiskMedium || !metadata.RequiresApproval || metadata.SideEffect != SideEffectWrite {
		t.Fatalf("git_commit approval metadata = %#v", metadata)
	}
	if err := os.WriteFile(filepath.Join(root, "change.txt"), []byte("change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "left-uncommitted.txt"), []byte("still dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := commitTool.Execute(context.Background(), rc, json.RawMessage(`{"message":"feat: add change","paths":["change.txt"]}`)); err != nil {
		t.Fatal(err)
	}
	if eventType != "git.commit_created" || eventPayload["workspace_id"] != int64(70) || eventPayload["project_id"] != int64(11) || eventPayload["kind"] != "worktree" || eventPayload["head_sha"] == "" || eventPayload["status"] != "ready" {
		t.Fatalf("incomplete git.commit_created event: type=%q payload=%#v", eventType, eventPayload)
	}
	if eventPayload["dirty"] != true || eventPayload["has_unpushed_commits"] != true || !rc.Workspace.Dirty || !rc.Workspace.HasUnpushedCommits {
		t.Fatalf("git.commit_created did not preserve the real post-commit status: payload=%#v workspace=%#v", eventPayload, rc.Workspace)
	}
	if rc.Workspace.HeadSHA != eventPayload["head_sha"] {
		t.Fatalf("git_commit did not refresh the runtime HEAD snapshot: payload=%#v workspace=%#v", eventPayload, rc.Workspace)
	}
	if _, err := (GitTool{Kind: "git_status", Git: service}).Execute(context.Background(), rc, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if eventType != "git.status_changed" || eventPayload["dirty"] != true || eventPayload["has_unpushed_commits"] != true || !rc.Workspace.Dirty || !rc.Workspace.HasUnpushedCommits {
		t.Fatalf("git_status did not update the workspace snapshot: type=%q payload=%#v workspace=%#v", eventType, eventPayload, rc.Workspace)
	}

	command := exec.Command("git", "checkout", "-b", "branch-drift")
	command.Dir = root
	if output, commandErr := command.CombinedOutput(); commandErr != nil {
		t.Fatalf("checkout branch drift: %v: %s", commandErr, output)
	}
	if _, err := commitTool.Execute(context.Background(), rc, json.RawMessage(`{"message":"feat: wrong branch"}`)); err == nil {
		t.Fatal("git_commit accepted a checkout whose branch drifted from the Run binding")
	}
	if _, err := (GitTool{Kind: "git_unknown", Git: service}).Execute(context.Background(), rc, json.RawMessage(`{}`)); err == nil {
		t.Fatal("unknown Git tool kind fell through to a mutating operation")
	}
}

func TestGitToolsRejectChangedRepositoryBinding(t *testing.T) {
	root, rc := testWorkspace(t)
	service := gitinfra.NewService(gitinfra.Config{GitUserName: "AgentCanvas Test", GitUserEmail: "test@example.com"})
	if _, err := service.EnsureRepository(context.Background(), root, true); err != nil {
		t.Fatal(err)
	}
	rc.Workspace.Kind = "shared"
	rc.Workspace.GitEnabled = true
	rc.Workspace.RepositoryRoot = t.TempDir()
	if _, err := (GitTool{Kind: "git_status", Git: service}).Execute(context.Background(), rc, json.RawMessage(`{}`)); err == nil {
		t.Fatal("git_status accepted a repository binding that no longer resolves to the Run repository")
	}
}

func TestReadFileLimitCountsUnicodeCharacters(t *testing.T) {
	root, rc := testWorkspace(t)
	if err := os.WriteFile(filepath.Join(root, "unicode.txt"), []byte("你好世界"), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := (FileTool{Kind: "read_file", MaxReadChars: 4}).Execute(context.Background(), rc, json.RawMessage(`{"path":"unicode.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.ContentJSON, &payload); err != nil {
		t.Fatal(err)
	}
	content, _ := payload["content"].(string)
	if utf8.RuneCountInString(content) != 4 || len(content) <= 4 {
		t.Fatalf("character limit was applied as bytes: content=%q bytes=%d runes=%d", content, len(content), utf8.RuneCountInString(content))
	}
	if truncated, _ := payload["truncated"].(bool); !truncated {
		t.Fatalf("unicode read should report continuation: %#v", payload)
	}
	if hint, _ := payload["hint"].(string); !strings.Contains(hint, "clamped mid-line") {
		t.Fatalf("mid-line truncation did not explain the line-offset limitation: %#v", payload)
	}
}

func TestReadFileCharacterBudgetDoesNotSkipAnUnreadLine(t *testing.T) {
	root, rc := testWorkspace(t)
	if err := os.WriteFile(filepath.Join(root, "budget.txt"), []byte("a\nb\nc"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := FileTool{Kind: "read_file", MaxReadChars: 3}
	result, err := tool.Execute(context.Background(), rc, json.RawMessage(`{"path":"budget.txt"}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.ContentJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["content"] != "1|a" || payload["next_offset"] != float64(2) {
		t.Fatalf("character budget skipped an unread line: %#v", payload)
	}
	continued, err := tool.Execute(context.Background(), rc, json.RawMessage(`{"path":"budget.txt","offset":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(continued.ContentJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["content"] != "2|b" {
		t.Fatalf("continuation did not resume at the unread line: %#v", payload)
	}
}

func TestReadFileReportsLineLimitContinuation(t *testing.T) {
	root, rc := testWorkspace(t)
	if err := os.WriteFile(filepath.Join(root, "lines.txt"), []byte("one\ntwo\nthree"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := FileTool{Kind: "read_file", MaxReadChars: 1000}
	result, err := tool.Execute(context.Background(), rc, json.RawMessage(`{"path":"lines.txt","limit":2}`))
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.ContentJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["truncated_by"] != "lines" || payload["next_offset"] != float64(3) || !strings.Contains(fmt.Sprint(payload["hint"]), "offset=3") {
		t.Fatalf("line-limited read returned the wrong continuation metadata: %#v", payload)
	}
}

func TestSharedWorkspaceOverwriteRequiresHash(t *testing.T) {
	root, rc := testWorkspace(t)
	rc.Workspace.Kind = "shared"
	path := filepath.Join(root, "shared.txt")
	if err := os.WriteFile(path, []byte("before"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := FileTool{Kind: "write_file"}
	if _, err := tool.Execute(context.Background(), rc, json.RawMessage(`{"path":"shared.txt","content":"after"}`)); err == nil {
		t.Fatal("shared overwrite without expected_sha256 was accepted")
	}
	hash := fileHash([]byte("before"))
	if _, err := tool.Execute(context.Background(), rc, json.RawMessage(`{"path":"shared.txt","content":"after","expected_sha256":"`+hash+`"}`)); err != nil {
		t.Fatalf("shared overwrite with matching hash failed: %v", err)
	}
	patch := FileTool{Kind: "patch_file"}
	if _, err := patch.Execute(context.Background(), rc, json.RawMessage(`{"path":"shared.txt","old_string":"after","new_string":"patched"}`)); err == nil {
		t.Fatal("shared replace patch without expected_sha256 was accepted")
	}
	if _, err := patch.Execute(context.Background(), rc, json.RawMessage(`{"path":"shared.txt","old_string":"after","new_string":"patched","expected_sha256":"`+fileHash([]byte("after"))+`"}`)); err != nil {
		t.Fatalf("shared replace patch with matching hash failed: %v", err)
	}
}

func TestWorkspaceFileLocksUseGitCommonDirectory(t *testing.T) {
	root := t.TempDir()
	gitService := gitinfra.NewService(gitinfra.Config{})
	if _, err := gitService.EnsureRepository(context.Background(), root, true); err != nil {
		t.Fatal(err)
	}
	lockRoot := workspaceFileLockRoot(root)
	want := filepath.Join(root, ".git", "agentcanvas-file-locks")
	want, _ = filepath.EvalSymlinks(filepath.Dir(want))
	want = filepath.Join(want, "agentcanvas-file-locks")
	if filepath.Clean(lockRoot) != filepath.Clean(want) {
		t.Fatalf("lock root = %q, want shared Git directory %q", lockRoot, want)
	}
	release, err := acquirePathLock(root, filepath.Join(root, "locked.txt"))
	if err != nil {
		t.Fatal(err)
	}
	release()
	if info, err := os.Stat(lockRoot); err != nil || !info.IsDir() {
		t.Fatalf("shared lock directory was not created: %v", err)
	}
}

func TestFileToolsRejectTraversalSensitiveAndSymlink(t *testing.T) {
	root, rc := testWorkspace(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	tool := FileTool{Kind: "write_file"}
	for _, path := range []string{"../outside.txt", ".env", ".netrc", "credentials.json", "service-account.json", "escape/new.txt"} {
		_, err := tool.Execute(context.Background(), rc, mustJSON(map[string]any{"path": path, "content": "x"}))
		if err == nil {
			t.Fatalf("expected protected path %q to fail", path)
		}
	}
	unknownPath := filepath.Join(root, "must-stay.txt")
	if err := os.WriteFile(unknownPath, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (FileTool{Kind: "unknown_file_tool"}).Execute(context.Background(), rc, mustJSON(map[string]any{"path": "must-stay.txt"})); err == nil {
		t.Fatal("unknown file tool kind fell through to delete_file")
	}
	if data, err := os.ReadFile(unknownPath); err != nil || string(data) != "keep" {
		t.Fatalf("unknown file tool mutated the workspace: %q err=%v", data, err)
	}
}

func TestSearchFilesSupportsPathGlob(t *testing.T) {
	root, rc := testWorkspace(t)
	if err := os.MkdirAll(filepath.Join(root, "pkg", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(root, "root.go"), []byte("needle\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "pkg", "a.go"), []byte("needle\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "pkg", "deep", "b.go"), []byte("needle\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "pkg", "a.txt"), []byte("needle\n"), 0o644)
	tool := FileTool{Kind: "search_files", MaxOutputBytes: 256 * 1024}
	result, err := tool.Execute(context.Background(), rc, mustJSON(map[string]any{"pattern": "needle", "path": "**/*.go"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result.ContentJSON), "root.go") || !strings.Contains(string(result.ContentJSON), "a.go") || !strings.Contains(string(result.ContentJSON), "b.go") || strings.Contains(string(result.ContentJSON), "a.txt") {
		t.Fatalf("unexpected glob result: %s", result.ContentJSON)
	}
	if _, err := tool.Execute(context.Background(), rc, mustJSON(map[string]any{"pattern": "needle", "path": "[/*.go"})); err == nil {
		t.Fatal("invalid path glob was accepted")
	}
	if _, err := tool.Execute(context.Background(), rc, mustJSON(map[string]any{"pattern": ""})); err == nil {
		t.Fatal("empty search pattern was accepted")
	}
}

func TestPatchFileUsesHermesFuzzyIndentationStrategy(t *testing.T) {
	root, rc := testWorkspace(t)
	path := filepath.Join(root, "indent.go")
	original := "func run() {\n    if ready {\n        start()\n    }\n}\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	tool := FileTool{Kind: "patch_file"}
	result, err := tool.Execute(context.Background(), rc, mustJSON(map[string]any{
		"mode":       "replace",
		"path":       "indent.go",
		"old_string": "  if ready {\n    start()\n  }",
		"new_string": "  if ready {\n    startAll()\n  }",
	}))
	if err != nil || result == nil || result.IsError {
		t.Fatalf("fuzzy patch failed: %v %#v", err, result)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, "    if ready {\n      startAll()\n    }") {
		t.Fatalf("replacement was not reindented to file content:\n%s", got)
	}
}

func TestHermesFuzzyMatchingStrategies(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		pattern  string
		strategy string
	}{
		{name: "exact", content: "alpha beta", pattern: "alpha", strategy: "exact"},
		{name: "line trimmed", content: "  alpha  \n\tbeta", pattern: "alpha\nbeta", strategy: "line_trimmed"},
		{name: "horizontal whitespace", content: "alpha   beta", pattern: "alpha beta", strategy: "whitespace_normalized"},
		{name: "escaped tab", content: "alpha\tbeta", pattern: `alpha\tbeta`, strategy: "escape_normalized"},
		{name: "unicode normalized", content: "alpha—beta", pattern: "alpha--beta", strategy: "unicode_normalized"},
		{name: "block anchor", content: "start\nmostly similar body\nend", pattern: "start\nmostly changed body\nend", strategy: "block_anchor"},
		{name: "context aware", content: "function start\nreturn value\nend", pattern: "function starts\nreturn values\nend", strategy: "context_aware"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, count, strategy, err := fuzzyFindAndReplace(test.content, test.pattern, "replacement", false)
			if err != nil || count != 1 || strategy != test.strategy {
				t.Fatalf("got count=%d strategy=%q err=%v; want strategy=%q", count, strategy, err, test.strategy)
			}
		})
	}

	if matches := trimmedBoundaryMatches("  alpha\n  exact middle\nomega  ", "alpha\n  exact middle\nomega"); len(matches) != 1 {
		t.Fatalf("trimmed boundary strategy got %d matches", len(matches))
	}
	if matches := indentationFlexibleMatches("    alpha\n        beta", "  alpha\n    beta"); len(matches) != 1 {
		t.Fatalf("indentation flexible strategy got %d matches", len(matches))
	}
}

func TestPatchFileAppliesHermesV4AMultiFilePatch(t *testing.T) {
	root, rc := testWorkspace(t)
	for name, content := range map[string]string{
		"update.txt": "alpha\nbeta\n",
		"delete.txt": "remove me\n",
		"move.txt":   "move me\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	patchBody := `*** Begin Patch
*** Update File: update.txt
@@ alpha @@
 alpha
-beta
+gamma
*** Add File: added.txt
+created
+content
*** Move File: move.txt -> moved.txt
*** Delete File: delete.txt
*** End Patch`
	tool := FileTool{Kind: "patch_file"}
	result, err := tool.Execute(context.Background(), rc, mustJSON(map[string]any{
		"mode":  "patch",
		"patch": patchBody,
	}))
	if err != nil || result == nil || result.IsError {
		t.Fatalf("V4A patch failed: %v %#v", err, result)
	}

	assertContent := func(name, want string) {
		t.Helper()
		data, readErr := os.ReadFile(filepath.Join(root, name))
		if readErr != nil || string(data) != want {
			t.Fatalf("%s: got %q, err=%v, want %q", name, data, readErr, want)
		}
	}
	assertContent("update.txt", "alpha\ngamma\n")
	assertContent("added.txt", "created\ncontent")
	assertContent("moved.txt", "move me\n")
	for _, name := range []string{"move.txt", "delete.txt"} {
		if _, statErr := os.Stat(filepath.Join(root, name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("%s should not exist, stat err=%v", name, statErr)
		}
	}
}

func TestPatchFileV4AValidationIsAtomic(t *testing.T) {
	root, rc := testWorkspace(t)
	path := filepath.Join(root, "stable.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	patchBody := `*** Begin Patch
*** Update File: stable.txt
-before
+after
*** Delete File: missing.txt
*** End Patch`
	tool := FileTool{Kind: "patch_file"}
	if _, err := tool.Execute(context.Background(), rc, mustJSON(map[string]any{"mode": "patch", "patch": patchBody})); err == nil {
		t.Fatal("expected validation failure")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "before\n" {
		t.Fatalf("validation failure mutated a file: %q, err=%v", data, err)
	}
}

func TestPatchFileV4AAddDoesNotOverwriteExistingFile(t *testing.T) {
	root, rc := testWorkspace(t)
	path := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(path, []byte("keep me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	patchBody := `*** Begin Patch
*** Add File: existing.txt
+replacement
*** End Patch`
	tool := FileTool{Kind: "patch_file"}
	if _, err := tool.Execute(context.Background(), rc, mustJSON(map[string]any{"mode": "patch", "patch": patchBody})); err == nil {
		t.Fatal("expected Add File to reject an existing destination")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "keep me\n" {
		t.Fatalf("failed Add File overwrote existing content: %q err=%v", data, err)
	}
}

func TestPatchFileV4AApplyFailureRollsBackEarlierWrites(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "z-blocker")
	if err := os.WriteFile(blocker, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	created := filepath.Join(root, "a-created.txt")
	states := map[string]*v4aFileState{
		created:                             {exists: true, content: "must be rolled back\n", mode: 0o644},
		filepath.Join(blocker, "child.txt"): {exists: true, content: "cannot be created\n", mode: 0o644},
	}
	if err := applyV4AStates(states); err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("apply failure did not report a successful rollback: %v", err)
	}
	if _, err := os.Stat(created); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed multi-file patch left an earlier write behind: %v", err)
	}
	data, err := os.ReadFile(blocker)
	if err != nil || string(data) != "not a directory\n" {
		t.Fatalf("rollback changed the blocking original path: %q err=%v", data, err)
	}
}

func TestSharedV4APatchRequiresPerFileHashesBeforeAnyWrite(t *testing.T) {
	root, rc := testWorkspace(t)
	rc.Workspace.Kind = "shared"
	for name, content := range map[string]string{"first.txt": "first\n", "second.txt": "second\n"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	patchBody := `*** Begin Patch
*** Update File: first.txt
-first
+FIRST
*** Update File: second.txt
-second
+SECOND
*** End Patch`
	tool := FileTool{Kind: "patch_file"}
	if _, err := tool.Execute(context.Background(), rc, mustJSON(map[string]any{"mode": "patch", "patch": patchBody})); err == nil {
		t.Fatal("shared multi-file patch without hashes was accepted")
	}
	wrong := map[string]string{"first.txt": fileHash([]byte("first\n")), "second.txt": "wrong"}
	if _, err := tool.Execute(context.Background(), rc, mustJSON(map[string]any{"mode": "patch", "patch": patchBody, "expected_sha256_by_path": wrong})); err == nil {
		t.Fatal("shared multi-file patch with a stale hash was accepted")
	}
	for name, want := range map[string]string{"first.txt": "first\n", "second.txt": "second\n"} {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(data) != want {
			t.Fatalf("version conflict partially modified %s: %q err=%v", name, data, err)
		}
	}
	expected := map[string]string{"first.txt": fileHash([]byte("first\n")), "second.txt": fileHash([]byte("second\n"))}
	if _, err := tool.Execute(context.Background(), rc, mustJSON(map[string]any{"mode": "patch", "patch": patchBody, "expected_sha256_by_path": expected})); err != nil {
		t.Fatalf("shared multi-file patch with matching hashes failed: %v", err)
	}
}

func TestPatchFileV4ARejectsUnsafeHeaderPaths(t *testing.T) {
	root, rc := testWorkspace(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	tool := FileTool{Kind: "patch_file"}
	for _, path := range []string{"../escape.txt", ".env", "escape/outside.txt", filepath.Join(outside, "absolute.txt")} {
		patchBody := "*** Begin Patch\n*** Add File: " + path + "\n+blocked\n*** End Patch"
		if _, err := tool.Execute(context.Background(), rc, mustJSON(map[string]any{"mode": "patch", "patch": patchBody})); err == nil {
			t.Fatalf("expected unsafe path %q to be rejected", path)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unsafe patch escaped the workspace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "outside.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("symlink patch escaped the workspace: %v", err)
	}
}

func mustJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}
