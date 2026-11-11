package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"
)

// renderAgentRelation displays exact semantic positions together with source
// lines or declaration bodies already present in the result payload.
func renderAgentRelation(name string, data map[string]any) (string, bool) {
	key := ""
	switch name {
	case "get_definition", "find_references", "find_implementations", "get_type_definition", "get_declaration":
		key = "locations"
	case "get_incoming_calls", "get_outgoing_calls":
		key = "calls"
	case "get_subtypes", "get_supertypes":
		key = "types"
	default:
		return "", false
	}
	raw, err := json.Marshal(data[key])
	if err != nil {
		return "", false
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return "", false
	}

	var out strings.Builder
	fmt.Fprintf(&out, "## %s — %d result(s)\n", name, len(items))
	if meta, ok := normalizedMap(data["meta"]); ok && meta["truncated"] == true {
		out.WriteString("Results truncated by limit; request a higher limit if more are needed.\n")
	}
	seenSource := make(map[string]string)
	for _, item := range items {
		path, _ := item["path"].(string)
		if path == "" {
			return "", false
		}
		line, column := sourceItemPosition(item)
		switch {
		case line > 0 && column > 0:
			fmt.Fprintf(&out, "\n### %s:%d:%d", path, line, column)
		case line > 0:
			fmt.Fprintf(&out, "\n### %s:%d", path, line)
		default:
			fmt.Fprintf(&out, "\n### %s", path)
		}
		label, _ := item["name"].(string)
		if label == "" {
			label, _ = item["symbol_name"].(string)
		}
		if label != "" {
			fmt.Fprintf(&out, " — %s", label)
		}
		out.WriteByte('\n')
		if handle, ok := item["symbol_id"].(string); ok && handle != "" {
			fmt.Fprintf(&out, "Symbol handle: %s\n", handle)
		}

		if sourceRange, ok := normalizedMap(item["source_range"]); ok {
			if start, startOK := normalizedMap(sourceRange["start"]); startOK {
				if end, endOK := normalizedMap(sourceRange["end"]); endOK {
					fmt.Fprintf(&out, "Declaration range: %v-%v\n", start["line"], end["line"])
				}
			}
		}
		if source, ok := item["source"].(string); ok && source != "" {
			if sourceKey, known := declarationKey(path, item["source_range"]); known {
				if first, duplicate := seenSource[sourceKey]; duplicate {
					fmt.Fprintf(&out, "Declaration source already shown at %s (same LSP range).\n", first)
					continue
				}
				seenSource[sourceKey] = fmt.Sprintf("%s:%d:%d", path, line, column)
			}
			out.WriteString(fencedSource(path, source))
			if item["source_truncated"] == true {
				out.WriteString("WARNING: Declaration source truncated by max_lines or byte limit.\n")
			}
		} else if reason, ok := item["source_unavailable"].(string); ok && reason != "" {
			fmt.Fprintf(&out, "Source unavailable: %s\n", reason)
		} else if previous, ok := item["source_reused"].(string); ok && previous != "" {
			fmt.Fprintf(&out, "Declaration source already shown at %s (same LSP range).\n", previous)
		} else if _, ok := item["source_omitted"]; ok {
			out.WriteString("Source omitted (source_limit); semantic location retained.\n")
		} else if preview, ok := item["preview"].(string); ok && preview != "" {
			fmt.Fprintf(&out, "Matching line: %s\n", preview)
		}
	}
	return out.String(), true
}

func normalizedMap(value any) (map[string]any, bool) {
	if value == nil {
		return nil, false
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	var out map[string]any
	if json.Unmarshal(raw, &out) != nil {
		return nil, false
	}
	return out, true
}

func declarationKey(path string, value any) (string, bool) {
	rng, ok := normalizedMap(value)
	if !ok {
		return "", false
	}
	start, startOK := normalizedMap(rng["start"])
	end, endOK := normalizedMap(rng["end"])
	if !startOK || !endOK {
		return "", false
	}
	return fmt.Sprintf("%s:%v:%v-%v:%v", path, start["line"], start["column"], end["line"], end["column"]), true
}

func sourceItemPosition(item map[string]any) (int, int) {
	if position, ok := item["line"].(float64); ok && position > 0 {
		return int(position), 0
	}
	for _, field := range []string{"selection_range", "range"} {
		rng, ok := normalizedMap(item[field])
		if !ok {
			continue
		}
		start, ok := normalizedMap(rng["start"])
		if !ok {
			continue
		}
		line, lineOK := start["line"].(float64)
		if !lineOK {
			continue
		}
		column, _ := start["column"].(float64)
		return int(line), int(column)
	}
	return 0, 0
}
