package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type ExecuteRequest struct {
	Language       string
	Code           string
	TimeoutMS      int
	MaxOutputBytes int
	NetworkEnabled bool
	MemoryLimitMB  int
	CPULimit       string
}

type ExecuteResult struct {
	Language        string `json:"language"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	ExitCode        int    `json:"exit_code"`
	TimedOut        bool   `json:"timed_out"`
	OutputTruncated bool   `json:"output_truncated"`
	LatencyMS       int    `json:"latency_ms"`
}

type Runner interface {
	Execute(ctx context.Context, req ExecuteRequest) (*ExecuteResult, error)
}

type DockerRunner struct {
	DockerBinary string
	PythonImage  string
}

func NewDockerRunner() DockerRunner {
	return DockerRunner{
		DockerBinary: "docker",
		PythonImage:  "python:3.12-alpine",
	}
}

func (r DockerRunner) Execute(ctx context.Context, req ExecuteRequest) (*ExecuteResult, error) {
	language := strings.ToLower(strings.TrimSpace(req.Language))
	if language == "" {
		language = "python"
	}
	if language != "python" {
		return nil, fmt.Errorf("unsupported sandbox language: %s", language)
	}
	code := strings.TrimSpace(req.Code)
	if code == "" {
		return nil, fmt.Errorf("sandbox code is required")
	}
	timeout := time.Duration(req.TimeoutMS) * time.Millisecond
	if timeout <= 0 || timeout > 30*time.Second {
		timeout = 5 * time.Second
	}
	maxOutputBytes := req.MaxOutputBytes
	if maxOutputBytes <= 0 || maxOutputBytes > 1024*1024 {
		maxOutputBytes = 64 * 1024
	}
	memoryLimitMB := req.MemoryLimitMB
	if memoryLimitMB <= 0 || memoryLimitMB > 512 {
		memoryLimitMB = 128
	}
	cpuLimit := strings.TrimSpace(req.CPULimit)
	if cpuLimit == "" {
		cpuLimit = "1"
	}
	dir, err := os.MkdirTemp("", "agentcanvas-sandbox-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	codePath := filepath.Join(dir, "main.py")
	if err := os.WriteFile(codePath, []byte(code), 0o600); err != nil {
		return nil, err
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := []string{
		"run", "--rm",
		"--memory", fmt.Sprintf("%dm", memoryLimitMB),
		"--cpus", cpuLimit,
		"--pids-limit", "64",
		"-v", dir + ":/workspace:ro",
		"-w", "/workspace",
	}
	if !req.NetworkEnabled {
		args = append(args, "--network", "none")
	}
	image := strings.TrimSpace(r.PythonImage)
	if image == "" {
		image = "python:3.12-alpine"
	}
	args = append(args, image, "python", "/workspace/main.py")
	binary := strings.TrimSpace(r.DockerBinary)
	if binary == "" {
		binary = "docker"
	}
	started := time.Now()
	cmd := exec.CommandContext(runCtx, binary, args...)
	stdout := &limitedBuffer{limit: maxOutputBytes}
	stderr := &limitedBuffer{limit: maxOutputBytes}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err = cmd.Run()
	result := &ExecuteResult{
		Language:        language,
		Stdout:          stdout.String(),
		Stderr:          stderr.String(),
		ExitCode:        exitCode(err),
		TimedOut:        errors.Is(runCtx.Err(), context.DeadlineExceeded),
		OutputTruncated: stdout.truncated || stderr.truncated,
		LatencyMS:       int(time.Since(started).Milliseconds()),
	}
	if err != nil && !result.TimedOut {
		return result, err
	}
	if result.TimedOut {
		return result, context.DeadlineExceeded
	}
	return result, nil
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(data []byte) (int, error) {
	if b.limit <= 0 {
		return len(data), nil
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(data), nil
	}
	if len(data) > remaining {
		b.truncated = true
		_, _ = b.buf.Write(data[:remaining])
		return len(data), nil
	}
	_, _ = b.buf.Write(data)
	return len(data), nil
}

func (b *limitedBuffer) String() string {
	return b.buf.String()
}
