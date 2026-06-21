package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type SwitchNode struct{}

type switchConfig struct {
	Conditions []switchCondition `json:"conditions"`
}

type switchCondition struct {
	Expr   string `json:"expr"`
	Target string `json:"target"`
}

func (SwitchNode) Type() string { return "switch" }

func (SwitchNode) Validate(config json.RawMessage) error {
	var cfg switchConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid switch config", agenterrors.ErrInvalidInput)
	}
	if len(cfg.Conditions) == 0 {
		return fmt.Errorf("%w: switch conditions are required", agenterrors.ErrInvalidInput)
	}
	for _, condition := range cfg.Conditions {
		if strings.TrimSpace(condition.Expr) == "" || strings.TrimSpace(condition.Target) == "" {
			return fmt.Errorf("%w: switch expr and target are required", agenterrors.ErrInvalidInput)
		}
	}
	return nil
}

func (SwitchNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	target, err := selectSwitchTarget(config, rc)
	if err != nil {
		return nil, err
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.SwitchSelected, RunID: rc.RunID, Payload: map[string]any{"target": target}})
	return engine.NodeOutput{"target": target}, nil
}

func (SwitchNode) NextNodeID(ctx context.Context, rc *engine.RunContext, output engine.NodeOutput, config json.RawMessage) (string, error) {
	target, _ := output["target"].(string)
	if target == "" {
		return selectSwitchTarget(config, rc)
	}
	return target, nil
}

func selectSwitchTarget(config json.RawMessage, rc *engine.RunContext) (string, error) {
	var cfg switchConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return "", err
	}
	defaultTarget := ""
	for _, condition := range cfg.Conditions {
		expr := strings.TrimSpace(condition.Expr)
		if strings.EqualFold(expr, "default") {
			defaultTarget = strings.TrimSpace(condition.Target)
			continue
		}
		matched, err := evalSwitchExpr(expr, rc)
		if err != nil {
			return "", err
		}
		if matched {
			return strings.TrimSpace(condition.Target), nil
		}
	}
	return defaultTarget, nil
}

func evalSwitchExpr(expr string, rc *engine.RunContext) (bool, error) {
	for _, op := range []string{">=", "<=", "==", "!=", ">", "<"} {
		if !strings.Contains(expr, op) {
			continue
		}
		parts := strings.SplitN(expr, op, 2)
		left := strings.TrimSpace(engine.ResolveTemplate(parts[0], rc))
		right := strings.Trim(strings.TrimSpace(engine.ResolveTemplate(parts[1], rc)), `"'`)
		return compareValues(left, right, op)
	}
	resolved := strings.TrimSpace(engine.ResolveTemplate(expr, rc))
	return resolved == "true" || resolved == "1", nil
}

func compareValues(left, right, op string) (bool, error) {
	leftNum, leftErr := strconv.ParseFloat(left, 64)
	rightNum, rightErr := strconv.ParseFloat(right, 64)
	if leftErr == nil && rightErr == nil {
		switch op {
		case ">=":
			return leftNum >= rightNum, nil
		case "<=":
			return leftNum <= rightNum, nil
		case ">":
			return leftNum > rightNum, nil
		case "<":
			return leftNum < rightNum, nil
		case "==":
			return leftNum == rightNum, nil
		case "!=":
			return leftNum != rightNum, nil
		}
	}
	switch op {
	case "==":
		return left == right, nil
	case "!=":
		return left != right, nil
	default:
		return false, fmt.Errorf("%w: switch operator %s requires numeric values", agenterrors.ErrInvalidInput, op)
	}
}
