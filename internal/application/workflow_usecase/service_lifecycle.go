package workflow_usecase

import (
	"context"
	"fmt"

	"agentcanvas/internal/domain/flow"
	agenterrors "agentcanvas/internal/pkg/errors"
)

// Lifecycle workflows intentionally start with a capability-free node subset.
// They can transform/validate data with prompts and models, but cannot acquire
// tools, memory writes, delegation, MCP, sandbox, or message side effects that
// are absent from the calling AgentRelease.
func validateLifecycleDSL(dsl *flow.DSL) error {
	if dsl == nil {
		return fmt.Errorf("%w: lifecycle workflow DSL is missing", agenterrors.ErrInvalidInput)
	}
	allowed := map[string]bool{
		"begin": true, "prompt": true, "llm": true, "switch": true,
		"json_output": true, "guardrail": true,
	}
	for _, spec := range dsl.Nodes {
		if !allowed[spec.Type] {
			return fmt.Errorf("%w: lifecycle workflow node %s (%s) is outside the release-safe node allowlist", agenterrors.ErrForbidden, spec.ID, spec.Type)
		}
	}
	return nil
}

func (s *Service) ValidateLifecycleWorkflow(ctx context.Context, ownerID, workflowID, versionID int64) error {
	if ownerID <= 0 || workflowID <= 0 || versionID <= 0 {
		return agenterrors.ErrInvalidInput
	}
	version, err := s.versions.FindByID(ctx, ownerID, versionID)
	if err != nil {
		return mapNotFound(err)
	}
	if version.WorkflowID != workflowID || !version.IsPublished {
		return fmt.Errorf("%w: lifecycle workflow version must be published and belong to the configured workflow", agenterrors.ErrInvalidInput)
	}
	dsl, err := flow.ParseDSL(version.DSLJSON)
	if err != nil {
		return fmt.Errorf("%w: invalid lifecycle workflow DSL", agenterrors.ErrInvalidInput)
	}
	if err := s.validator.Validate(dsl); err != nil {
		return err
	}
	return validateLifecycleDSL(dsl)
}
