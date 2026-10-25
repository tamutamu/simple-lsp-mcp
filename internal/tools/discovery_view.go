package tools

import "github.com/tamutamu/simple-lsp-mcp/internal/core"

// Symbol discovery returns *identities*, not full source trees. A caller can
// request the legacy full shape explicitly when exact ranges are needed.
func symbolListDetail(in map[string]any) (string, error) {
	detail := stringVal(in, "detail")
	switch detail {
	case "", "compact", "outline":
		return "compact", nil
	case "full":
		return "full", nil
	default:
		return "", core.NewError(core.InvalidArgument, "detail must be compact, outline or full")
	}
}

func projectSymbols(symbols []core.SymbolSummary, detail string) any {
	if detail == "full" {
		return symbols
	}
	out := make([]map[string]any, 0, len(symbols))
	for _, x := range symbols {
		y := map[string]any{
			"name": x.Name, "kind": x.Kind,
			"path": x.Path, "line": x.SelectionRange.Start.Line,
			"symbol_id": x.SymbolID,
		}
		if x.SymbolPath != "" {
			y["symbol_path"] = x.SymbolPath
		}
		if x.ContainerName != "" && x.SymbolPath == "" {
			y["container_name"] = x.ContainerName
		}
		out = append(out, y)
	}
	return out
}

func projectSymbolPage(page map[string]any, detail string) map[string]any {
	symbols, ok := page["symbols"].([]core.SymbolSummary)
	if ok {
		page["symbols"] = projectSymbols(symbols, detail)
	}
	return page
}
