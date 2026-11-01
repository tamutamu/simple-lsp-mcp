package tools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/config"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

// This is the previous backwards-copy implementation, kept only as an
// independent reference oracle for randomized equivalence and benchmarking.
func legacyAssembleWorkspaceEdits(original []byte, edits []computedEdit) []byte {
	updated := append([]byte(nil), original...)
	for i := len(edits) - 1; i >= 0; i-- {
		edit := edits[i]
		next := make([]byte, 0, len(updated)-(edit.end-edit.start)+len(edit.preview.NewText))
		next = append(next, updated[:edit.start]...)
		next = append(next, edit.preview.NewText...)
		next = append(next, updated[edit.end:]...)
		updated = next
	}
	return updated
}

func TestAssembleWorkspaceEditsMatchesLegacy(t *testing.T) {
	cases := []struct {
		source string
		edits  []computedEdit
		want   string
	}{
		{"", nil, ""},
		{"abc", nil, "abc"},
		{"abcdef", []computedEdit{{start: 1, end: 3, preview: editPreview{NewText: "XY"}}}, "aXYdef"},
		{"abcdef", []computedEdit{
			{start: 0, end: 0, preview: editPreview{NewText: "start"}},
			{start: 2, end: 4, preview: editPreview{NewText: ""}},
			{start: 6, end: 6, preview: editPreview{NewText: "end"}},
		}, "startabefend"},
		{"日本語abc🚀", []computedEdit{
			{start: 0, end: 3, preview: editPreview{NewText: "東"}},
			{start: 9, end: 12, preview: editPreview{NewText: "ABC"}},
		}, "東本語ABC🚀"},
	}
	for _, tc := range cases {
		got := assembleWorkspaceEdits([]byte(tc.source), tc.edits)
		if string(got) != tc.want {
			t.Fatalf("source=%q got=%q want=%q", tc.source, got, tc.want)
		}
		if legacy := legacyAssembleWorkspaceEdits([]byte(tc.source), tc.edits); !bytes.Equal(got, legacy) {
			t.Fatalf("new and previous implementations disagree: %q != %q", got, legacy)
		}
	}
	rng := rand.New(rand.NewSource(20261102))
	for trial := 0; trial < 500; trial++ {
		original := make([]byte, rng.Intn(1024))
		for i := range original {
			original[i] = byte('a' + rng.Intn(26))
		}
		var edits []computedEdit
		cursor := 0
		for cursor < len(original) {
			cursor += rng.Intn(17) + 1
			if cursor >= len(original) {
				break
			}
			end := cursor + rng.Intn(4)
			if end > len(original) {
				end = len(original)
			}
			edits = append(edits, computedEdit{
				start: cursor, end: end,
				preview: editPreview{NewText: strings.Repeat("Z", rng.Intn(8))},
			})
			cursor = end
		}
		got := assembleWorkspaceEdits(original, edits)
		want := legacyAssembleWorkspaceEdits(original, edits)
		if !bytes.Equal(got, want) {
			t.Fatalf("trial %d: result mismatch", trial)
		}
	}
}

func BenchmarkWorkspaceEditAssembly(b *testing.B) {
	original := bytes.Repeat([]byte("a"), 512<<10)
	var edits []computedEdit
	for i := 0; i < 256; i++ {
		start := i * 2048
		edits = append(edits, computedEdit{start: start, end: start + 2,
			preview: editPreview{NewText: "xyz"}})
	}
	for _, tc := range []struct {
		name string
		run  func([]byte, []computedEdit) []byte
	}{
		{"previous-repeated-copy", legacyAssembleWorkspaceEdits},
		{"linear-pass", assembleWorkspaceEdits},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				result := tc.run(original, edits)
				if len(result) != len(original)+256 {
					b.Fatalf("unexpected output length: %d", len(result))
				}
			}
		})
	}
}

func TestLSPBatchLocationsWithCacheMatchesUncached(t *testing.T) {
	root := t.TempDir()
	ws, err := workspace.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	e := New(ws, config.Runtime{MaxResults: 10})
	source := "const a = 1;\nconst b = a;\nconst c = a;\n"
	full := filepath.Join(root, "source.ts")
	if err := os.WriteFile(full, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(full)}).String()
	var locations []protocol.Location
	for i := 0; i < 100; i++ {
		locations = append(locations, protocol.Location{
			URI: uri,
			Range: protocol.Range{
				Start: protocol.Position{Line: 1, Character: 10},
				End: protocol.Position{Line: 1, Character: 11},
			},
		})
	}
	raw, err := json.Marshal(locations)
	if err != nil {
		t.Fatal(err)
	}
	uncached, err := e.locationsIn(nil, raw, "utf-16")
	if err != nil {
		t.Fatal(err)
	}
	cached, err := e.locationsIn(newFileCache(), raw, "utf-16")
	if err != nil {
		t.Fatal(err)
	}
	if len(cached) != 100 || !reflect.DeepEqual(uncached, cached) {
		t.Fatalf("cached result differs from uncached: %d vs %d", len(cached), len(uncached))
	}
}

func BenchmarkLSPBatchLocationCache(b *testing.B) {
	root := b.TempDir()
	ws, err := workspace.Open(root)
	if err != nil {
		b.Fatal(err)
	}
	e := New(ws, config.Runtime{MaxResults: 10})
	full := filepath.Join(root, "source.ts")
	source := strings.Repeat("const example = 1;\n", 1000)
	if err := os.WriteFile(full, []byte(source), 0o600); err != nil {
		b.Fatal(err)
	}
	uri := (&url.URL{Scheme: "file", Path: filepath.ToSlash(full)}).String()
	locations := make([]protocol.Location, 200)
	for i := range locations {
		locations[i] = protocol.Location{
			URI: uri,
			Range: protocol.Range{
				Start: protocol.Position{Line: i + 1, Character: 6},
				End: protocol.Position{Line: i + 1, Character: 13},
			},
		}
	}
	raw, err := json.Marshal(locations)
	if err != nil {
		b.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		cache bool
	}{
		{"previous-no-cache", false},
		{"request-local-cache", true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var c *fileCache
				if tc.cache {
					c = newFileCache()
				}
				got, err := e.locationsIn(c, raw, "utf-16")
				if err != nil || len(got) != 200 {
					b.Fatal(fmt.Errorf("locations=%d: %w", len(got), err))
				}
			}
		})
	}
}
