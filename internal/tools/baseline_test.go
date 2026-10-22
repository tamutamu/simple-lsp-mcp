package tools

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestExtractBaseArchiveRejectsPathEscape(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "../escape.go", Mode: 0o600, Size: 1, Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractBaseArchive(bytes.NewReader(buf.Bytes()), t.TempDir()); err == nil {
		t.Fatal("expected unsafe archive path to be rejected")
	}
}

func TestExtractBaseArchiveRejectsLinks(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "link.go", Linkname: "target.go", Typeflag: tar.TypeSymlink}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractBaseArchive(bytes.NewReader(buf.Bytes()), t.TempDir()); err == nil {
		t.Fatal("expected archive symlink to be rejected")
	}
}

func TestExtractBaseArchiveSkipsExcludedDirectories(t *testing.T) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	entries := map[string]string{
		"src/main.go":           "package src\n",
		".scratch/generated.go": "package generated\n",
	}
	for name, content := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := extractBaseArchive(bytes.NewReader(buf.Bytes()), root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "src", "main.go")); err != nil {
		t.Fatalf("expected source file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".scratch", "generated.go")); !os.IsNotExist(err) {
		t.Fatalf("excluded file should not be extracted, stat err=%v", err)
	}
}
