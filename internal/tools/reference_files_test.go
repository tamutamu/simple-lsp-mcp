package tools

import (
	"reflect"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

func TestUniqueReferenceFilePathsLimitsDistinctFilesNotHits(t *testing.T) {
	refs := []core.Location{
		{Path: "src/other.go"}, {Path: "src/first.go"}, {Path: "src/first.go"},
		{Path: "src/other.go"}, {Path: "src/third.go"}, {Path: "src/first.go"},
	}
	for _, tc := range []struct {
		name      string
		limit     int
		want      []string
		truncated bool
	}{
		{"all", 3, []string{"src/first.go", "src/other.go", "src/third.go"}, false},
		{"two unique", 2, []string{"src/first.go", "src/other.go"}, true},
		{"one unique", 1, []string{"src/first.go"}, true},
		{"five hit slots is all three files", 5, []string{"src/first.go", "src/other.go", "src/third.go"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, truncated := uniqueReferenceFilePaths(refs, tc.limit)
			if !reflect.DeepEqual(got, tc.want) || truncated != tc.truncated {
				t.Fatalf("got %v truncated=%v, want %v truncated=%v", got, truncated, tc.want, tc.truncated)
			}
		})
	}
	got, truncated := uniqueReferenceFilePaths(nil, 2)
	if truncated || len(got) != 0 {
		t.Fatalf("empty references should be complete, got %v truncated=%v", got, truncated)
	}
}
