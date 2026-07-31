package document

import (
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
	"testing"
)

func TestPositionEncodingsRoundTrip(t *testing.T) {
	text := []byte("ASCII\n日本語😀e\u0301\n")
	for _, encoding := range []string{"utf-8", "utf-16", "utf-32"} {
		for _, p := range []core.Position{{Line: 1, Column: 6}, {Line: 2, Column: 4}, {Line: 2, Column: 5}} {
			got, err := ToLSP(text, p, encoding)
			if err != nil {
				t.Fatalf("%s ToLSP(%+v): %v", encoding, p, err)
			}
			back, err := FromLSP(text, got, encoding)
			if err != nil || back != p {
				t.Fatalf("%s round trip: %#v, %v", encoding, back, err)
			}
		}
	}
}
func TestPositionRejectsSplitUTF16CodeUnit(t *testing.T) {
	if _, err := FromLSP([]byte("😀"), protocol.Position{Line: 0, Character: 1}, "utf-16"); err == nil {
		t.Fatal("expected invalid UTF-16 position")
	}
}
