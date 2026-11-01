package tools

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadStableWarmSourceAcceptsUnchangedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stable.ts")
	want := []byte("export const current = 42;\n")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readStableWarmSource(path, info)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("stable source: got=%q err=%v", got, err)
	}
}

func TestReadStableWarmSourceRejectsReplacedFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "candidate.ts")
	if err := os.WriteFile(path, []byte("const original = 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Replacing the inode is deterministic even on filesystems whose mtime
	// granularity cannot distinguish two writes within one clock tick.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("const replacement = 2;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readStableWarmSource(path, oldInfo); err == nil ||
		!strings.Contains(err.Error(), "changed before reading") {
		t.Fatalf("replaced source was accepted: %v", err)
	}
}

func TestReadStableWarmSourceRejectsInPlaceEditAfterStat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidate.ts")
	if err := os.WriteFile(path, []byte("const oldName = 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("const newName = 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Ensure a different mtime even on coarse-resolution filesystems.
	future := oldInfo.ModTime().Add(3 * time.Second)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatal(err)
	}
	if _, err := readStableWarmSource(path, oldInfo); err == nil {
		t.Fatal("in-place source edit was accepted using stale file metadata")
	}
}

func TestReadStableWarmSourceRejectsSymlinkSwap(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	path := filepath.Join(root, "candidate.ts")
	outsidePath := filepath.Join(outside, "secret.ts")
	if err := os.WriteFile(path, []byte("const old = 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldInfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsidePath, []byte("const privateValue = 2;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsidePath, path); err != nil {
		t.Skipf("symlinks not supported in this test environment: %v", err)
	}
	if _, err := readStableWarmSource(path, oldInfo); err == nil {
		t.Fatal("symlink replacement was read using stale workspace metadata")
	}
}
