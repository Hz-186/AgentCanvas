package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func SanitizeBranch(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = regexp.MustCompile(`[^a-z0-9._/-]+`).ReplaceAllString(value, "-")
	value = strings.Trim(value, "-/_ .")
	value = strings.ReplaceAll(value, "..", "-")
	value = strings.TrimSuffix(value, ".lock")
	value = regexp.MustCompile(`/+`).ReplaceAllString(value, "/")
	value = regexp.MustCompile(`-+`).ReplaceAllString(value, "-")
	parts := make([]string, 0)
	for _, part := range strings.Split(value, "/") {
		if part = strings.Trim(part, "-._"); part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, "/")
}

func Slugify(value string) string {
	value = strings.ReplaceAll(SanitizeBranch(value), "/", "-")
	if len(value) > 80 {
		value = value[:80]
	}
	value = strings.Trim(value, "-._")
	if value == "" {
		return "run"
	}
	return value
}

func BranchName(projectSlug string, runID int64, task string) string {
	projectSlug = Slugify(projectSlug)
	if len(projectSlug) > 48 {
		projectSlug = projectSlug[:48]
	}
	return SanitizeBranch(fmt.Sprintf("%s/%d-%s", projectSlug, runID, Slugify(task)))
}

func EnsureInside(root, target string) (string, error) {
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	target, err = filepath.Abs(filepath.Clean(target))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes workspace root")
	}
	return target, nil
}

func EnsureSafePath(root, target string) (string, error) {
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", err
	}
	target, err = filepath.Abs(filepath.Clean(target))
	if err != nil {
		return "", err
	}
	if _, err := EnsureInside(root, target); err != nil {
		return "", err
	}
	rootReal := root
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		rootReal = resolved
	}
	existing := target
	for {
		if _, statErr := os.Lstat(existing); statErr == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	if resolved, resolveErr := filepath.EvalSymlinks(existing); resolveErr == nil {
		if _, err := EnsureInside(rootReal, resolved); err != nil {
			return "", err
		}
	}
	if _, statErr := os.Lstat(target); statErr == nil {
		if resolved, resolveErr := filepath.EvalSymlinks(target); resolveErr == nil {
			if _, err := EnsureInside(rootReal, resolved); err != nil {
				return "", err
			}
		}
	}
	return target, nil
}

func IsSensitivePath(root, target string) bool {
	if _, err := EnsureSafePath(root, target); err != nil {
		return true
	}
	resolved := filepath.Clean(target)
	if candidate, err := filepath.EvalSymlinks(target); err == nil {
		resolved = candidate
	}
	rel, _ := filepath.Rel(root, resolved)
	for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
		lower := strings.ToLower(part)
		if lower == ".git" || lower == ".agentcanvas" || lower == "credentials" || lower == "secrets" || lower == ".ssh" || lower == ".aws" {
			return true
		}
	}
	base := strings.ToLower(filepath.Base(resolved))
	if base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".p12") || strings.HasSuffix(base, ".pfx") {
		return true
	}
	switch base {
	case ".git-credentials", ".netrc", ".npmrc", ".pypirc", "authorized_keys", "credentials", "credentials.json",
		"google-credentials.json", "id_dsa", "id_ecdsa", "id_ed25519", "id_rsa", "service-account.json", "service_account.json":
		return true
	}
	for _, secretName := range []string{"secrets.json", "secrets.yaml", "secrets.yml", "secrets.toml", "secrets.ini"} {
		if base == secretName {
			return true
		}
	}
	return strings.HasSuffix(base, "-credentials.json") || strings.HasSuffix(base, "_credentials.json")
}
