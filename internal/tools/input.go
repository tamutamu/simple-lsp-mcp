package tools

import (
	"encoding/json"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func targetOf(in map[string]any) core.Target {
	return core.Target{
		SymbolID: stringVal(in, "symbol_id"),
		Path:     stringVal(in, "path"),
		Line:     intVal(in, "line"),
		Column:   intVal(in, "column"),
	}
}

func stringVal(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return value
}

func intVal(values map[string]any, key string) int {
	switch value := values[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	case json.Number:
		integer, _ := value.Int64()
		return int(integer)
	default:
		return 0
	}
}

func boolValDefault(values map[string]any, key string, fallback bool) bool {
	value, ok := values[key].(bool)
	if !ok {
		return fallback
	}
	return value
}

func kindAllowed(kind string, in map[string]any) bool {
	values, ok := in["kinds"].([]any)
	if !ok || len(values) == 0 {
		return true
	}
	for _, value := range values {
		if allowed, _ := value.(string); allowed == kind {
			return true
		}
	}
	return false
}
