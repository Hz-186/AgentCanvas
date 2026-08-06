package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// ValidateToolArguments implements the deterministic JSON-Schema subset used
// by runtime tools. It intentionally has no network/model fallback: a schema
// failure is a tool result and must never reach RuntimeTool.Execute.
func ValidateToolArguments(schema, args json.RawMessage) error {
	if !json.Valid(args) {
		return fmt.Errorf("arguments are not valid JSON")
	}
	if len(bytes.TrimSpace(schema)) == 0 || bytes.Equal(bytes.TrimSpace(schema), []byte("null")) || bytes.Equal(bytes.TrimSpace(schema), []byte("{}")) {
		return nil
	}
	var schemaValue map[string]any
	if err := json.Unmarshal(schema, &schemaValue); err != nil {
		return fmt.Errorf("invalid tool JSON schema: %w", err)
	}
	var value any
	if err := json.Unmarshal(args, &value); err != nil {
		return fmt.Errorf("arguments are not valid JSON: %w", err)
	}
	return validateJSONSchema(schemaValue, value, "$")
}

func validateJSONSchema(schema map[string]any, value any, path string) error {
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		matched := false
		for _, candidate := range enum {
			if reflect.DeepEqual(candidate, value) {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("%s must be one of enum values", path)
		}
	}
	if types, ok := schema["type"]; ok {
		if !matchesSchemaType(types, value) {
			return fmt.Errorf("%s has an invalid type", path)
		}
	}
	if object, ok := value.(map[string]any); ok {
		if required, ok := schema["required"].([]any); ok {
			for _, raw := range required {
				name, _ := raw.(string)
				if name != "" {
					if _, exists := object[name]; !exists {
						return fmt.Errorf("%s is missing required field %q", path, name)
					}
				}
			}
		}
		properties, _ := schema["properties"].(map[string]any)
		additional, hasAdditional := schema["additionalProperties"]
		for name, child := range object {
			rawProperty, known := properties[name]
			if !known {
				if hasAdditional {
					if allowed, ok := additional.(bool); ok && !allowed {
						return fmt.Errorf("%s contains unknown field %q", path, name)
					}
				}
				continue
			}
			propertySchema, ok := rawProperty.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s has invalid schema", path, name)
			}
			if err := validateJSONSchema(propertySchema, child, path+"."+name); err != nil {
				return err
			}
		}
	}
	if array, ok := value.([]any); ok {
		if min, ok := integerSchemaValue(schema["minItems"]); ok && len(array) < min {
			return fmt.Errorf("%s must contain at least %d items", path, min)
		}
		if max, ok := integerSchemaValue(schema["maxItems"]); ok && len(array) > max {
			return fmt.Errorf("%s must contain at most %d items", path, max)
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for index, item := range array {
				if err := validateJSONSchema(items, item, fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
			}
		}
	}
	if text, ok := value.(string); ok {
		if min, ok := integerSchemaValue(schema["minLength"]); ok && len([]rune(text)) < min {
			return fmt.Errorf("%s must contain at least %d characters", path, min)
		}
		if max, ok := integerSchemaValue(schema["maxLength"]); ok && len([]rune(text)) > max {
			return fmt.Errorf("%s must contain at most %d characters", path, max)
		}
	}
	return nil
}

func matchesSchemaType(raw any, value any) bool {
	if list, ok := raw.([]any); ok {
		for _, item := range list {
			if matchesSchemaType(item, value) {
				return true
			}
		}
		return false
	}
	typeName, _ := raw.(string)
	switch strings.ToLower(typeName) {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		number, ok := value.(float64)
		return ok && number == float64(int64(number))
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	default:
		return true
	}
}

func integerSchemaValue(value any) (int, bool) {
	number, ok := value.(float64)
	if !ok || number < 0 || number != float64(int(number)) {
		return 0, false
	}
	return int(number), true
}
