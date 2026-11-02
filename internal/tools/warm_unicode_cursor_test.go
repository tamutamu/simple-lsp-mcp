package tools

import (
    "fmt"
    "reflect"
    "testing"

    "github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

// A cursor lookup must use the same Unicode identifier alphabet as the
// candidate scanner. Previously a combining mark, connector punctuation,
// ZWNJ or ZWJ truncated the cursor spelling to "a", but "a" is not a whole
// identifier in any of these files. The warm-up then opened zero candidates,
// potentially causing project-wide references/rename to miss real files.
func TestWarmUnicodeIdentifierCursorAndProjectScanAgree(t *testing.T) {
    e, p, root := warmIndexFixture(t)
    names := []string{
        "a\u0301",  // combining acute accent (Mn)
        "a\u200cb", // zero-width non-joiner
        "a\u200db", // zero-width joiner
        "a\u203fb", // undertie (Pc)
    }
    for i, name := range names {
        filename := fmt.Sprintf("unicode-%d.ts", i)
        source := "export const " + name + " = 1;\n"
        warmWrite(t, root, filename, source)
        for offset := range []rune(name) {
            got := warmIdentifierAtPosition([]byte(source), protocol.Position{
                Line: 0, Character: len("export const ") + offset,
            }, "utf-16")
            if got != name {
                t.Fatalf("name=%q cursor=%d: got %q", name, offset, got)
            }
        }
        if warmContainsIdentifier([]byte(source), []byte("a")) {
            t.Fatalf("truncated spelling 'a' must not match %q", name)
        }
    }
    for i, name := range names {
        got, stats := warmScan(t, e, p, root, name)
        want := []string{fmt.Sprintf("unicode-%d.ts", i)}
        if !reflect.DeepEqual(got, want) || stats.Matches != 1 {
            t.Fatalf("name=%q: matches=%v want=%v stats=%+v", name, got, want, stats)
        }
    }
    got, _ := warmScan(t, e, p, root, "a")
    if len(got) != 0 {
        t.Fatalf("truncated cursor spelling would open unrelated candidates: %v", got)
    }
}
