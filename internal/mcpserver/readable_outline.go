package mcpserver

import (
	"fmt"
	"strings"
)

// renderReadableDocumentOutline converts an already-compact, LSP-backed
// document outline to a concise hierarchy, keeping every displayed symbol,
// source line, kind and opaque symbol handle. Full-detail requests are never
// shortened, and unfamiliar result shapes fall back to exact JSON.
func renderReadableDocumentOutline(input, data map[string]any) (string, bool) {
	if input["detail"] == "full" || len(data) != 2 {
		return "", false
	}
	path, ok := data["path"].(string)
	if !ok || path == "" {
		return "", false
	}
	raw, ok := data["symbols"].([]any)
	if !ok {
		return "", false
	}
	var out strings.Builder
	fmt.Fprintf(&out, "LSP outline %s (navigation only; some locals omitted):\n", path)
	count := 0
	var walk func([]any, int) bool
	walk = func(nodes []any, depth int) bool {
		if depth > 64 {
			return false
		}
		for _, node := range nodes {
			m, ok := node.(map[string]any)
			if !ok {
				return false
			}
			// Preserve every field exposed by the compact API or return JSON
			// if the upstream schema adds data this renderer doesn't know.
			for key := range m {
				switch key {
				case "name", "kind", "symbol_id", "line", "children":
				default:
					return false
				}
			}
			name, nameOK := m["name"].(string)
			kind, kindOK := m["kind"].(string)
			handle, handleOK := m["symbol_id"].(string)
			if !nameOK || !kindOK || !handleOK || name == "" || kind == "" || handle == "" {
				return false
			}
			line, ok := outlineLineNumber(m["line"])
			if !ok || line < 1 {
				return false
			}
			fmt.Fprintf(&out, "%s%d %s %s [symbol_id=%s]\n", strings.Repeat("  ", depth), line, kind, name, handle)
			count++
			if children, present := m["children"]; present {
				childNodes, ok := children.([]any)
				if !ok || len(childNodes) == 0 || !walk(childNodes, depth+1) {
					return false
				}
			}
		}
		return true
	}
	if !walk(raw, 0) {
		return "", false
	}
	if count == 0 {
		out.WriteString("No displayed symbols. Use full detail if local declarations are needed.\n")
	} else {
		out.WriteString("Next: inspect_symbol(symbol_id=...,view=edit); no additional name search needed.\n")
	}
	return out.String(), true
}

func outlineLineNumber(value any) (int, bool) {
	switch x := value.(type) {
	case int:
		return x, true
	case float64:
		if x >= 1 && x <= 1e9 && x == float64(int(x)) {
			return int(x), true
		}
	}
	return 0, false
}
