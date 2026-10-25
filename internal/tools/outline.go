package tools

import "github.com/tamutamu/simple-lsp-mcp/internal/core"

// The default document outline is navigation data, not a complete dump of all
// LSP declarations. Full document symbols remain available with detail=full.
// Register every symbol first; hiding local variables here only affects the
// result, not semantic lookup or the registry.
func outlineDocumentSymbols(nodes []any) []any {
	out := make([]any, 0, len(nodes))
	for _, value := range nodes {
		node, ok := value.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, outlineNode(node))
	}
	return out
}

func outlineNode(node map[string]any) map[string]any {
	out := map[string]any{
		"symbol_id": node["symbol_id"],
		"name":      node["name"],
		"kind":      node["kind"],
	}
	if selection, ok := node["selection_range"].(core.Range); ok {
		out["line"] = selection.Start.Line
	} else if r, ok := node["range"].(core.Range); ok {
		out["line"] = r.Start.Line
	}
	if children, ok := node["children"].([]any); ok {
		visible := make([]any, 0, len(children))
		for _, value := range children {
			child, ok := value.(map[string]any)
			if !ok || !isOutlineMember(node["kind"], child["kind"]) {
				continue
			}
			visible = append(visible, outlineNode(child))
		}
		if len(visible) > 0 {
			out["children"] = visible
		}
	}
	return out
}

func isStructuralSymbol(kind any) bool {
	switch kind {
	case "module", "namespace", "package", "class", "method", "constructor",
		"enum", "interface", "function", "struct", "event", "operator":
		return true
	default:
		return false
	}
}

// Keep significant class/interface/enum members, but not every function-local
// variable. The parent kind matters: a variable in a class is a member while
// the same LSP kind under a function is usually a temporary or parameter.
func isOutlineMember(parentKind, childKind any) bool {
	if isStructuralSymbol(childKind) {
		return true
	}
	switch parentKind {
	case "class", "struct", "interface", "enum", "object":
		switch childKind {
		case "variable", "constant", "property", "field", "enum_member":
			return true
		}
	case "module", "namespace", "package":
		return childKind == "variable" || childKind == "constant"
	}
	return false
}

func outlineFlatSymbols(symbols []core.SymbolSummary) []any {
	out := make([]any, 0, len(symbols))
	for _, x := range symbols {
		out = append(out, map[string]any{
			"symbol_id": x.SymbolID,
			"name":      x.Name,
			"kind":      x.Kind,
			"line":      x.SelectionRange.Start.Line,
		})
	}
	return out
}
