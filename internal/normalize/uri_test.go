package normalize

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

func TestFileURIRoundTripWithEscapedCharacters(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"plain.go", "a b#日本語.ts", "percent%file.py"} {
		path := filepath.Join(ws.Root(), name)
		uri := FileURI(path)
		parsed, err := url.Parse(uri)
		if err != nil || parsed.Scheme != "file" || parsed.Opaque != "" {
			t.Fatalf("invalid file URI %q: %v", uri, err)
		}
		if parsed.Host != "" {
			t.Fatalf("local path unexpectedly became URI authority: %q", uri)
		}
		got, err := URIPath(ws, uri)
		if err != nil || got != name {
			t.Fatalf("round-trip %q: got %q, err %v", uri, got, err)
		}
		if strings.Contains(name, " ") && !strings.Contains(uri, "%20") {
			t.Fatalf("space not escaped: %q", uri)
		}
		if strings.Contains(name, "#") && !strings.Contains(uri, "%23") {
			t.Fatalf("hash not escaped: %q", uri)
		}
	}
}

func TestURIPathRejectsQueryAndFragment(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	uri := FileURI(filepath.Join(ws.Root(), "sample.go"))
	for _, bad := range []string{uri + "?unexpected=1", uri + "#fragment", "https://example.com/sample.go"} {
		if _, err := URIPath(ws, bad); err == nil {
			t.Fatalf("accepted invalid LSP file URI %q", bad)
		}
	}
}

func TestURIPathRejectsRemoteHostOnPOSIX(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("UNC file URLs are valid on Windows")
	}
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := URIPath(ws, "file://remote-host"+filepath.ToSlash(filepath.Join(ws.Root(), "sample.go"))); err == nil {
		t.Fatal("remote host was silently discarded")
	}
}

func TestWindowsFileURIUsesPathForDriveAndAuthorityForUNC(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path semantics")
	}
	if got, want := FileURI(`C:\Users\A B\src\main.go`), "file:///C:/Users/A%20B/src/main.go"; got != want {
		t.Fatalf("drive URI = %q, want %q", got, want)
	}
	if got, want := FileURI(`\\server\share\A B\main.go`), "file://server/share/A%20B/main.go"; got != want {
		t.Fatalf("UNC URI = %q, want %q", got, want)
	}
}
