package tool_usecase

import (
	"context"
	"encoding/json"
	"strings"

	"agentcanvas/internal/domain/tool"
	"agentcanvas/internal/runtime/toolruntime"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type Service struct {
	definitions tool.DefinitionRepository
}

func NewService(definitions tool.DefinitionRepository) *Service {
	return &Service{definitions: definitions}
}

type CreateToolRequest struct {
	Name             string          `json:"name" binding:"required"`
	ToolType         string          `json:"tool_type"`
	Description      string          `json:"description"`
	ConfigJSON       json.RawMessage `json:"config_json" binding:"required"`
	InputSchemaJSON  json.RawMessage `json:"input_schema_json"`
	OutputSchemaJSON json.RawMessage `json:"output_schema_json"`
}

type UpdateToolRequest struct {
	Name             string          `json:"name"`
	ToolType         string          `json:"tool_type"`
	Description      string          `json:"description"`
	ConfigJSON       json.RawMessage `json:"config_json"`
	InputSchemaJSON  json.RawMessage `json:"input_schema_json"`
	OutputSchemaJSON json.RawMessage `json:"output_schema_json"`
	Status           *int            `json:"status"`
}

func (s *Service) Create(ctx context.Context, ownerID int64, req CreateToolRequest) (*tool.Definition, error) {
	name := strings.TrimSpace(req.Name)
	toolType := strings.TrimSpace(req.ToolType)
	if toolType == "" {
		toolType = tool.TypeHTTP
	}
	if ownerID <= 0 || name == "" || toolType != tool.TypeHTTP || len(req.ConfigJSON) == 0 {
		return nil, agenterrors.ErrInvalidInput
	}
	item := &tool.Definition{
		OwnerID: ownerID, Name: name, ToolType: toolType, Description: strings.TrimSpace(req.Description),
		ConfigJSON: req.ConfigJSON, InputSchemaJSON: req.InputSchemaJSON, OutputSchemaJSON: req.OutputSchemaJSON,
		Status: tool.StatusActive,
	}
	if err := s.definitions.Create(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) List(ctx context.Context, ownerID int64, limit, offset int) ([]tool.Definition, error) {
	return s.definitions.List(ctx, ownerID, limit, offset)
}

func (s *Service) Get(ctx context.Context, ownerID, id int64) (*tool.Definition, error) {
	return s.definitions.FindByID(ctx, ownerID, id)
}

func (s *Service) Update(ctx context.Context, ownerID, id int64, req UpdateToolRequest) (*tool.Definition, error) {
	item, err := s.Get(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if value := strings.TrimSpace(req.Name); value != "" {
		item.Name = value
	}
	if value := strings.TrimSpace(req.ToolType); value != "" {
		if value != tool.TypeHTTP {
			return nil, agenterrors.ErrInvalidInput
		}
		item.ToolType = value
	}
	item.Description = strings.TrimSpace(req.Description)
	if len(req.ConfigJSON) > 0 {
		item.ConfigJSON = req.ConfigJSON
	}
	if len(req.InputSchemaJSON) > 0 {
		item.InputSchemaJSON = req.InputSchemaJSON
	}
	if len(req.OutputSchemaJSON) > 0 {
		item.OutputSchemaJSON = req.OutputSchemaJSON
	}
	if req.Status != nil {
		item.Status = *req.Status
	}
	if err := s.definitions.Update(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) Delete(ctx context.Context, ownerID, id int64) error {
	return s.definitions.SoftDelete(ctx, ownerID, id)
}

func (s *Service) Test(ctx context.Context, ownerID, id int64, input map[string]any) (map[string]any, error) {
	item, err := s.Get(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if item.Status != tool.StatusActive || item.ToolType != tool.TypeHTTP {
		return nil, agenterrors.ErrInvalidInput
	}
	inputJSON, _ := json.Marshal(input)
	output, err := toolruntime.ExecuteHTTPDefinition(ctx, item, inputJSON)
	if err != nil {
		return nil, err
	}
	return map[string]any(output), nil
}
