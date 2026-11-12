package mcpserver

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

// validateToolArguments enforces the schemas actually advertised to MCP
// clients. Some clients do not validate tools locally and the LSP engine
// historically interpreted fractional numbers, strings in boolean fields,
// and invalid arrays as zero/default or silently ignored them.
func validateToolArguments(schema map[string]any, in map[string]any) error {
	return validateArgumentSchema(schema, in, "arguments")
}

func invalidArgument(field, message string) error {
	return core.NewError(core.InvalidArgument, fmt.Sprintf("%s %s", field, message))
}

func validateArgumentSchema(schema map[string]any, value any, name string) error {
	if enum, ok := schema["enum"].([]any); ok {
		found := false
		for _, item := range enum {
			if reflect.DeepEqual(value, item) {
				found = true
				break
			}
		}
		if !found {
			return invalidArgument(name, "must match one of the advertised enum values")
		}
	}
	switch schema["type"] {
	case "string":
		if _, ok := value.(string); !ok {
			return invalidArgument(name, "must be a string")
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return invalidArgument(name, "must be a boolean")
		}
	case "integer":
		var number float64
		switch n := value.(type) {
		case float64:
			number = n
		case float32:
			number = float64(n)
		case int:
			number = float64(n)
		case int64:
			number = float64(n)
		case json.Number:
			v, err := n.Float64()
			if err != nil {
				return invalidArgument(name, "must be a finite integer")
			}
			number = v
		default:
			return invalidArgument(name, "must be an integer")
		}
		if math.IsNaN(number) || math.IsInf(number, 0) ||
			math.Trunc(number) != number || number >= float64(math.MaxInt) ||
			number < float64(math.MinInt) {
			return invalidArgument(name, "must be a finite integer in range")
		}
		if minimum, ok := schema["minimum"].(int); ok && number < float64(minimum) {
			return invalidArgument(name, fmt.Sprintf("must be >= %d", minimum))
		}
		if maximum, ok := schema["maximum"].(int); ok && number > float64(maximum) {
			return invalidArgument(name, fmt.Sprintf("must be <= %d", maximum))
		}
	case "array":
		a, ok := value.([]any)
		if !ok {
			return invalidArgument(name, "must be an array")
		}
		if min, ok := schema["minItems"].(int); ok && len(a) < min {
			return invalidArgument(name, fmt.Sprintf("must contain at least %d items", min))
		}
		if max, ok := schema["maxItems"].(int); ok && len(a) > max {
			return invalidArgument(name, fmt.Sprintf("must contain at most %d items", max))
		}
		if schema["uniqueItems"] == true {
			seen := make(map[string]bool, len(a))
			for _, v := range a {
				canonical, err := json.Marshal(v)
				if err != nil {
					return invalidArgument(name, "contains an invalid item")
				}
				key := string(canonical)
				if seen[key] {
					return invalidArgument(name, "must contain unique items")
				}
				seen[key] = true
			}
		}
		if itemSchema, ok := schema["items"].(map[string]any); ok {
			for i, item := range a {
				if err := validateArgumentSchema(itemSchema, item, fmt.Sprintf("%s[%d]", name, i)); err != nil {
					return err
				}
			}
		}
	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return invalidArgument(name, "must be an object")
		}
		properties, _ := schema["properties"].(map[string]any)
		for _, nameRequired := range schemaRequired(schema) {
			if _, ok := obj[nameRequired]; !ok {
				return invalidArgument(name+"."+nameRequired, "is required")
			}
		}
		for key, field := range obj {
			descriptor, exists := properties[key]
			if !exists {
				if schema["additionalProperties"] == false {
					return invalidArgument(name+"."+key, "is not an advertised argument")
				}
				continue
			}
			fieldSchema, ok := descriptor.(map[string]any)
			if !ok {
				return invalidArgument(name+"."+key, "has an invalid server schema")
			}
			if err := validateArgumentSchema(fieldSchema, field, name+"."+key); err != nil {
				return err
			}
		}
	}
	return nil
}

func schemaRequired(schema map[string]any) []string {
	switch v := schema["required"].(type) {
	case []string:
		return v
	case []any:
		required := make([]string, 0, len(v))
		for _, item := range v {
			if str, ok := item.(string); ok {
				required = append(required, str)
			}
		}
		return required
	default:
		return nil
	}
}
