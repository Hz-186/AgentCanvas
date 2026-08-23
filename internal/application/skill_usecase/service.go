package skill_usecase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/domain/skill"
	agenterrors "agentcanvas/internal/pkg/errors"

	"gorm.io/gorm"
)

type Service struct {
	repo          skill.Repository
	workspaceRoot string
}

type CreateSkillRequest struct {
	Name            string   `json:"name" binding:"required"`
	Description     string   `json:"description"`
	SkillType       string   `json:"skill_type"`
	SourceType      string   `json:"source_type"`
	EntryFile       string   `json:"entry_file"`
	ContentMarkdown string   `json:"content_markdown"`
	BundlePath      string   `json:"bundle_path"`
	Tags            []string `json:"tags"`
	Enabled         *bool    `json:"enabled"`
}

type UpdateSkillRequest struct {
	Name            *string   `json:"name"`
	Description     *string   `json:"description"`
	SkillType       *string   `json:"skill_type"`
	SourceType      *string   `json:"source_type"`
	EntryFile       *string   `json:"entry_file"`
	ContentMarkdown *string   `json:"content_markdown"`
	BundlePath      *string   `json:"bundle_path"`
	Tags            *[]string `json:"tags"`
	Enabled         *bool     `json:"enabled"`
}

type ValidationResult struct {
	Valid       bool         `json:"valid"`
	Error       string       `json:"error,omitempty"`
	Checksum    string       `json:"checksum,omitempty"`
	ValidatedAt time.Time    `json:"validated_at"`
	Skill       *skill.Skill `json:"skill"`
}

func NewService(repo skill.Repository, workspaceRoot string) *Service {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		if cwd, err := os.Getwd(); err == nil {
			workspaceRoot = cwd
		}
	}
	return &Service{repo: repo, workspaceRoot: workspaceRoot}
}

func (s *Service) Create(ctx context.Context, ownerID int64, req CreateSkillRequest) (*skill.Skill, error) {
	if ownerID <= 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	item := &skill.Skill{
		SoftDeleteModel: domain.SoftDeleteModel{BaseModel: domain.BaseModel{OwnerID: ownerID}},
		Name:            strings.TrimSpace(req.Name),
		Description:     strings.TrimSpace(req.Description),
		SkillType:       strings.TrimSpace(req.SkillType),
		SourceType:      strings.TrimSpace(req.SourceType),
		EntryFile:       strings.TrimSpace(req.EntryFile),
		ContentMarkdown: req.ContentMarkdown,
		BundlePath:      strings.TrimSpace(req.BundlePath),
		Enabled:         skill.Enabled,
	}
	if req.Enabled != nil {
		item.Enabled = *req.Enabled
	}
	item.TagsJSON = mustMarshalTags(req.Tags)
	if err := s.prepareSkill(item); err != nil {
		return nil, err
	}
	if err := s.repo.Create(ctx, item); err != nil {
		return nil, mapNotFound(err)
	}
	return item, nil
}

func (s *Service) List(ctx context.Context, ownerID int64, limit, offset int) ([]skill.Skill, error) {
	return s.repo.List(ctx, ownerID, limit, offset)
}

func (s *Service) Get(ctx context.Context, ownerID, id int64) (*skill.Skill, error) {
	item, err := s.repo.FindByID(ctx, ownerID, id)
	return item, mapNotFound(err)
}

func (s *Service) Update(ctx context.Context, ownerID, id int64, req UpdateSkillRequest) (*skill.Skill, error) {
	item, err := s.Get(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		item.Name = strings.TrimSpace(*req.Name)
	}
	if req.Description != nil {
		item.Description = strings.TrimSpace(*req.Description)
	}
	if req.SkillType != nil {
		item.SkillType = strings.TrimSpace(*req.SkillType)
	}
	if req.SourceType != nil {
		item.SourceType = strings.TrimSpace(*req.SourceType)
	}
	if req.EntryFile != nil {
		item.EntryFile = strings.TrimSpace(*req.EntryFile)
	}
	if req.ContentMarkdown != nil {
		item.ContentMarkdown = *req.ContentMarkdown
	}
	if req.BundlePath != nil {
		item.BundlePath = strings.TrimSpace(*req.BundlePath)
	}
	if req.Tags != nil {
		item.TagsJSON = mustMarshalTags(*req.Tags)
	}
	if req.Enabled != nil {
		item.Enabled = *req.Enabled
	}
	if err := s.prepareSkill(item); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) Delete(ctx context.Context, ownerID, id int64) error {
	return s.repo.SoftDelete(ctx, ownerID, id)
}

func (s *Service) Validate(ctx context.Context, ownerID, id int64) (*ValidationResult, error) {
	item, err := s.Get(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	validErr := s.prepareSkill(item)
	now := time.Now().UTC()
	item.LastValidatedAt = &now
	result := &ValidationResult{Valid: validErr == nil, ValidatedAt: now, Skill: item}
	if validErr != nil {
		item.LastValidationError = validErr.Error()
		if updateErr := s.repo.Update(ctx, item); updateErr != nil {
			return nil, updateErr
		}
		result.Error = validErr.Error()
		return result, nil
	}
	item.LastValidationError = ""
	if err := s.repo.Update(ctx, item); err != nil {
		return nil, err
	}
	result.Checksum = item.Checksum
	return result, nil
}

func (s *Service) prepareSkill(item *skill.Skill) error {
	if item == nil {
		return agenterrors.ErrInvalidInput
	}
	item.Name = strings.TrimSpace(item.Name)
	item.Description = strings.TrimSpace(item.Description)
	if item.Name == "" || item.Description == "" {
		return agenterrors.ErrInvalidInput
	}
	if item.SkillType == "" {
		item.SkillType = skill.TypeInstruction
	}
	if item.SkillType != skill.TypeInstruction && item.SkillType != skill.TypeBundle {
		return fmt.Errorf("%w: unsupported skill_type", agenterrors.ErrInvalidInput)
	}
	if item.SourceType == "" {
		item.SourceType = skill.SourceInline
	}
	if item.SourceType != skill.SourceInline && item.SourceType != skill.SourceLocalPath {
		return fmt.Errorf("%w: unsupported source_type", agenterrors.ErrInvalidInput)
	}
	if item.EntryFile == "" {
		item.EntryFile = "SKILL.md"
	}
	if !isSimpleRelativeFile(item.EntryFile) {
		return fmt.Errorf("%w: entry_file must be a relative file path", agenterrors.ErrInvalidInput)
	}
	// Enabled is a boolean by design; no numeric status validation is needed.
	item.TagsJSON = mustMarshalTags(decodeTags(item.TagsJSON))
	content, bundlePath, err := s.resolveSkillContent(item.SourceType, item.BundlePath, item.EntryFile, item.ContentMarkdown)
	if err != nil {
		return err
	}
	item.BundlePath = bundlePath
	if item.SourceType == skill.SourceInline {
		item.ContentMarkdown = content
	}
	item.Checksum = checksumForContent(item.Name, item.Description, item.SkillType, item.SourceType, item.EntryFile, bundlePath, content)
	return nil
}

func (s *Service) resolveSkillContent(sourceType, bundlePath, entryFile, inlineContent string) (string, string, error) {
	switch sourceType {
	case skill.SourceInline:
		content := strings.TrimSpace(inlineContent)
		if content == "" {
			return "", "", fmt.Errorf("%w: inline skill content_markdown is required", agenterrors.ErrInvalidInput)
		}
		return content, "", nil
	case skill.SourceLocalPath:
		root, err := normalizeWorkspaceRoot(s.workspaceRoot)
		if err != nil {
			return "", "", err
		}
		cleanBundle, err := normalizeBundlePath(root, bundlePath)
		if err != nil {
			return "", "", err
		}
		fullEntry, err := resolveEntryPath(cleanBundle, entryFile)
		if err != nil {
			return "", "", err
		}
		data, err := os.ReadFile(fullEntry)
		if err != nil {
			return "", "", fmt.Errorf("%w: read skill entry file failed", agenterrors.ErrInvalidInput)
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			return "", "", fmt.Errorf("%w: skill entry content is empty", agenterrors.ErrInvalidInput)
		}
		return content, cleanBundle, nil
	default:
		return "", "", fmt.Errorf("%w: unsupported source_type", agenterrors.ErrInvalidInput)
	}
}

func normalizeWorkspaceRoot(root string) (string, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("%w: workspace root is not configured", agenterrors.ErrInvalidInput)
	}
	absRoot, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return "", fmt.Errorf("%w: invalid workspace root", agenterrors.ErrInvalidInput)
	}
	return absRoot, nil
}

func normalizeBundlePath(workspaceRoot, bundlePath string) (string, error) {
	bundlePath = strings.TrimSpace(bundlePath)
	if bundlePath == "" {
		return "", fmt.Errorf("%w: bundle_path is required", agenterrors.ErrInvalidInput)
	}
	cleanPath := filepath.Clean(bundlePath)
	if !filepath.IsAbs(cleanPath) {
		cleanPath = filepath.Join(workspaceRoot, cleanPath)
	}
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		return "", fmt.Errorf("%w: invalid bundle_path", agenterrors.ErrInvalidInput)
	}
	if absPath != workspaceRoot && !strings.HasPrefix(absPath, workspaceRoot+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: bundle_path must stay within the workspace", agenterrors.ErrInvalidInput)
	}
	return absPath, nil
}

func resolveEntryPath(bundlePath, entryFile string) (string, error) {
	joined := filepath.Join(bundlePath, filepath.Clean(entryFile))
	absPath, err := filepath.Abs(joined)
	if err != nil {
		return "", fmt.Errorf("%w: invalid entry_file path", agenterrors.ErrInvalidInput)
	}
	if absPath != bundlePath && !strings.HasPrefix(absPath, bundlePath+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: entry_file escapes bundle_path", agenterrors.ErrInvalidInput)
	}
	return absPath, nil
}

func isSimpleRelativeFile(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." {
		return false
	}
	return !strings.HasPrefix(clean, ".."+string(os.PathSeparator))
}

func decodeTags(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		return nil
	}
	return tags
}

func mustMarshalTags(tags []string) json.RawMessage {
	clean := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		clean = append(clean, tag)
	}
	data, _ := json.Marshal(clean)
	if len(data) == 0 {
		return json.RawMessage("[]")
	}
	return data
}

func checksumForContent(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(strings.TrimSpace(part)))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func mapNotFound(err error) error {
	if err == nil {
		return nil
	}
	if err == gorm.ErrRecordNotFound {
		return agenterrors.ErrNotFound
	}
	return err
}
