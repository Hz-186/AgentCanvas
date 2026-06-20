package node

import (
	"encoding/json"
	"fmt"
)

func validateSimpleJSONSchema(schema json.RawMessage, value any) error {
	if len(schema) == 0 || string(schema) == "null" {
		return nil
	}
	var cfg map[string]any
	if err := json.Unmarshal(schema, &cfg); err != nil {
		return fmt.Errorf("invalid json schema")
	}
	if typ, _ := cfg["type"].(string); typ != "" {
		if err := validateJSONType(typ, value); err != nil {
			return err
		}
	}
	properties, _ := cfg["properties"].(map[string]any)
	required, _ := cfg["required"].([]any)
	if len(properties) == 0 && len(required) == 0 {
		return nil
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return fmt.Errorf("json schema expects object")
	}
	for _, item := range required {
		key, _ := item.(string)
		if key == "" {
			continue
		}
		if _, ok := obj[key]; !ok {
			return fmt.Errorf("json schema missing required field %s", key)
		}
	}
	for key, spec := range properties {
		value, exists := obj[key]
		if !exists {
			continue
		}
		specMap, ok := spec.(map[string]any)
		if !ok {
			continue
		}
		typ, _ := specMap["type"].(string)
		if typ == "" {
			continue
		}
		if err := validateJSONType(typ, value); err != nil {
			return fmt.Errorf("json schema field %s: %w", key, err)
		}
	}
	return nil
}

func validateJSONType(typ string, value any) error {
	switch typ {
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("expected object")
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("expected array")
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("expected string")
		}
	case "number":
		if _, ok := value.(float64); !ok {
			if _, ok := value.(int); !ok {
				return fmt.Errorf("expected number")
			}
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("expected boolean")
		}
	}
	return nil
}
