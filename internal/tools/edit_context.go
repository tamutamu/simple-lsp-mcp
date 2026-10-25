package tools

import (
	"path"
	"regexp"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

const (
	maxEditContextBytes    = 4 * 1024
	maxEditContextLookback = 24
)

// editContext contains only current-source evidence outside the LSP symbol
// range. Its import selection is lexical, not a full dependency graph.
type editContext struct {
	Prefix    string
	Imports   string
	Bindings  string
	Truncated bool
}

var moduleAssignment = regexp.MustCompile("^(?:export\\s+)?(?:(?:const|let|var)\\s+)?([A-Za-z_][A-Za-z0-9_]*)\\s*(?::[^=]+)?=\\s*")

func selectEditContext(source []byte, symbolRange core.Range, code string, maxCodeBytes int) editContext {
	lines := strings.SplitAfter(string(source), "\n")
	start := symbolRange.Start.Line - 1
	if start < 0 || start >= len(lines) {
		return editContext{}
	}
	var out editContext

	// Decorators are immediately before a declaration, including multiline
	// Python route decorators. Only scan until the nearest blank separator.
	from := start
	for i := start - 1; i >= 0 && start-i <= maxEditContextLookback; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			break
		}
		from = i
	}
	if from < start {
		hasDecorator := false
		for i := from; i < start; i++ {
			if strings.HasPrefix(strings.TrimSpace(lines[i]), "@") {
				hasDecorator = true
				break
			}
		}
		if hasDecorator && strings.HasPrefix(strings.TrimSpace(lines[from]), "@") {
			out.Prefix = strings.Join(lines[from:start], "")
			if len(out.Prefix)+len(code) > maxCodeBytes {
				out.Prefix = ""
				out.Truncated = true
			}
		}
	}

	used := out.Prefix + code
	budget := maxEditContextBytes
	// Resolve directly referenced module bindings *before* imports so the
	// imports used by those bindings are also selected. Example: @router.get
	// depends on router = APIRouter(...), which itself depends on APIRouter.
	for i := 0; i < from && budget > 0; i++ {
		raw := lines[i]
		if strings.TrimLeft(raw, " \t") != raw {
			continue
		}
		if match := moduleAssignment.FindStringSubmatch(strings.TrimSpace(raw)); len(match) == 2 && containsIdentifier(used, match[1]) {
			if len(raw) > budget {
				out.Truncated = true
			} else {
				out.Bindings += raw
				used += raw
				budget -= len(raw)
			}
		}
	}
	// Header imports for declaration and its directly relevant bindings.
	// Source is never synthesized or fetched externally.
	for i := 0; i < from && budget > 0; {
		line := strings.TrimSpace(lines[i])
		// Never mistake imports or assignments *inside a previous function*
		// for module-level dependencies.
		moduleLevel := strings.TrimLeft(lines[i], " \t") == lines[i]
		if moduleLevel && isImportStart(line) {
			startImport, end := i, i+1
			if strings.HasPrefix(line, "from ") && strings.Contains(line, "(") && !strings.Contains(line, ")") {
				for end < from && end-startImport < maxEditContextLookback {
					if strings.Contains(lines[end-1], ")") {
						break
					}
					end++
				}
				if !strings.Contains(strings.Join(lines[startImport:end], ""), ")") {
					out.Truncated = true
					i = end
					continue
				}
			}
			if strings.HasPrefix(line, "import (") {
				for end < from && end-startImport < maxEditContextLookback {
					if strings.TrimSpace(lines[end-1]) == ")" {
						break
					}
					end++
				}
				if strings.TrimSpace(lines[end-1]) != ")" {
					out.Truncated = true
					i = end
					continue
				}
			}
			segment := strings.Join(lines[startImport:end], "")
			if importIsReferenced(segment, used) {
				if len(segment) > budget {
					out.Truncated = true
				} else {
					out.Imports += segment
					budget -= len(segment)
				}
			}
			i = end
			continue
		}
		i++
	}
	if budget == 0 {
		out.Truncated = true
	}
	return out
}

func isImportStart(line string) bool {
	return strings.HasPrefix(line, "import ") || strings.HasPrefix(line, "from ")
}

func importIsReferenced(importStmt, usage string) bool {
	t := strings.TrimSpace(importStmt)
	if strings.HasPrefix(t, "import (") {
		for _, part := range strings.Split(t, "\n") {
			fields := strings.Fields(strings.TrimSpace(part))
			if len(fields) == 0 {
				continue
			}
			alias := ""
			if len(fields) >= 2 && strings.HasPrefix(fields[len(fields)-1], "\"") {
				alias = fields[0]
			} else if strings.HasPrefix(fields[0], "\"") {
				alias = path.Base(strings.Trim(fields[0], "\""))
			}
			if alias != "" && containsIdentifier(usage, alias) {
				return true
			}
		}
		return false
	}
	if strings.HasPrefix(t, "import ") && strings.Contains(t, " from ") {
		// TypeScript/JavaScript: named, default and namespace imports.
		parts := strings.SplitN(strings.TrimPrefix(t, "import "), " from ", 2)
		if len(parts) == 2 {
			aliases := strings.Trim(parts[0], "{} ")
			for _, item := range strings.Split(aliases, ",") {
				fields := strings.Fields(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(item), "type ")))
				if len(fields) != 0 && containsIdentifier(usage, fields[len(fields)-1]) {
					return true
				}
			}
			return false
		}
	}
	if strings.HasPrefix(t, "from ") {
		idx := strings.Index(t, " import ")
		if idx < 0 {
			return false
		}
		names := strings.TrimSpace(t[idx+8:])
		names = strings.Trim(names, "() \n")
		for _, candidate := range strings.Split(names, ",") {
			fields := strings.Fields(candidate)
			if len(fields) != 0 && containsIdentifier(usage, fields[len(fields)-1]) {
				return true
			}
		}
		return false
	}
	if strings.HasPrefix(t, "import \"") {
		pkg := path.Base(strings.Trim(strings.TrimPrefix(t, "import "), "\" "))
		return containsIdentifier(usage, pkg)
	}
	if strings.HasPrefix(t, "import ") {
		rest := strings.TrimSpace(strings.TrimPrefix(t, "import "))
		for _, chunk := range strings.Split(rest, ",") {
			fields := strings.Fields(chunk)
			if len(fields) == 0 {
				continue
			}
			name := strings.Split(fields[0], ".")[0]
			if len(fields) >= 3 && fields[len(fields)-2] == "as" {
				name = fields[len(fields)-1]
			}
			if containsIdentifier(usage, name) {
				return true
			}
		}
	}
	return false
}

func containsIdentifier(code, name string) bool {
	if name == "" {
		return false
	}
	for offset := 0; offset < len(code); {
		i := strings.Index(code[offset:], name)
		if i < 0 {
			return false
		}
		i += offset
		before := i == 0 || !isIdentifierByte(code[i-1])
		end := i + len(name)
		after := end == len(code) || !isIdentifierByte(code[end])
		if before && after {
			return true
		}
		offset = i + 1
	}
	return false
}

func isIdentifierByte(x byte) bool {
	return x == '_' || x >= 'A' && x <= 'Z' || x >= 'a' && x <= 'z' || x >= '0' && x <= '9' || x >= 128
}
