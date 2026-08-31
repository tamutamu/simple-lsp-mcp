package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

func TestFileCacheReadMemoizesAfterFileIsDeleted(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(ws.Root(), "f.txt")
	if err := os.WriteFile(full, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := newFileCache()
	if _, err := c.read(ws, "f.txt"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(full); err != nil {
		t.Fatal(err)
	}
	b, err := c.read(ws, "f.txt")
	if err != nil {
		t.Fatalf("second read should be memoized, got error: %v", err)
	}
	if string(b) != "hello" {
		t.Fatalf("read = %q, want hello", b)
	}
}

func TestFileCacheHashOfMemoizes(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(ws.Root(), "f.txt")
	if err := os.WriteFile(full, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := newFileCache()
	want := c.hashOf(ws, "f.txt")
	if want == "" {
		t.Fatal("hashOf returned empty hash")
	}
	if err := os.Remove(full); err != nil {
		t.Fatal(err)
	}
	if got := c.hashOf(ws, "f.txt"); got != want {
		t.Fatalf("hashOf after delete = %q, want memoized %q", got, want)
	}
}

func TestFileCacheNilAlwaysReadsFromDisk(t *testing.T) {
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(ws.Root(), "f.txt")
	if err := os.WriteFile(full, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	var c *fileCache
	if _, err := c.read(ws, "f.txt"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(full); err != nil {
		t.Fatal(err)
	}
	if _, err := c.read(ws, "f.txt"); err == nil {
		t.Fatal("nil cache should not memoize; expected an error after deletion")
	}
}
