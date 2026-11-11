package tools

import (
	"path"
	"strings"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

// excludeTestReferenceLocations narrows an already semantic LSP result to
// production files only when explicitly requested. A filtered result is
// complete for the requested scope; the caller must report excluded_count.
func excludeTestReferenceLocations(locations []core.Location) []core.Location {
	retained := make([]core.Location, 0, len(locations))
	for _, loc := range locations {
		if !isTestSourceFile(loc.Path) {
			retained = append(retained, loc)
		}
	}
	return retained
}

func isTestSourceFile(location string) bool {
	location = strings.ReplaceAll(location, "\\", "/")
	base := path.Base(location)
	if strings.Contains(location, "/__tests__/") {
		return true
	}
	if strings.HasSuffix(base, "_test.go") ||
		strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py") ||
		strings.HasSuffix(base, "_test.py") {
		return true
	}
	for _, suffix := range []string{".test.ts", ".spec.ts", ".test.tsx", ".spec.tsx",
		".test.js", ".spec.js", ".test.jsx", ".spec.jsx", ".test.mjs", ".spec.mjs"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}
