package toolruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	gitinfra "agentcanvas/internal/infrastructure/git"
)

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
