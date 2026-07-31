package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveJailsTraversalAndAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "ok.go"), []byte("package ok"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Resolve("ok.go"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"../outside", filepath.Join(root, "ok.go"), "C:\\outside"} {
		if _, err := w.Resolve(path); err == nil {
			t.Fatalf("Resolve(%q) succeeded", path)
		}
	}
}
