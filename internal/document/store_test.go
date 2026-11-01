package document

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/lsp/protocol"
)

type fakeNotifier struct {
	generation uint64
	calls      []string
	items      []any
	failOn     string
}

func (f *fakeNotifier) Generation() uint64 { return f.generation }

func (f *fakeNotifier) Notify(method string, params any) error {
	if f.failOn == method { return errors.New("simulated failed send") }
	f.calls = append(f.calls, method)
	f.items = append(f.items, params)
	return nil
}

func TestSyncIsolatedPerSessionAndRestartGeneration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "example.py")
	if err := os.WriteFile(path, []byte("x = 1\n"), 0600); err != nil { t.Fatal(err) }
	store := NewStore()
	one := &fakeNotifier{generation: 1}
	two := &fakeNotifier{generation: 1}
	ctx := context.Background()
	first, err := store.Sync(ctx, one, path, "python")
	if err != nil || first.Version != 1 || len(one.calls) != 1 || one.calls[0] != "textDocument/didOpen" {
		t.Fatalf("first open = %#v, %v, calls=%v", first, err, one.calls)
	}
	if _, err := store.Sync(ctx, one, path, "python"); err != nil || len(one.calls) != 1 {
		t.Fatalf("unchanged file generated duplicate notifications: %v %v", err, one.calls)
	}
	if _, err := store.Sync(ctx, two, path, "python"); err != nil || len(two.calls) != 1 || two.calls[0] != "textDocument/didOpen" {
		t.Fatalf("second server did not open independently: %v %v", err, two.calls)
	}
	one.generation++
	restarted, err := store.Sync(ctx, one, path, "python")
	if err != nil || restarted.Version != 1 || len(one.calls) != 2 || one.calls[1] != "textDocument/didOpen" {
		t.Fatalf("new server did not reopen unmodified file: %#v %v %v", restarted, err, one.calls)
	}
}

func TestSyncFailureDoesNotAdvanceVersion(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "example.go")
	if err := os.WriteFile(path, []byte("package x\n"), 0600); err != nil { t.Fatal(err) }
	store := NewStore()
	n := &fakeNotifier{generation: 1}
	ctx := context.Background()
	if _, err := store.Sync(ctx, n, path, "go"); err != nil { t.Fatal(err) }
	if err := os.WriteFile(path, []byte("package y\n"), 0600); err != nil { t.Fatal(err) }
	n.failOn = "textDocument/didChange"
	if _, err := store.Sync(ctx, n, path, "go"); err == nil {
		t.Fatal("expected notification failure")
	}
	n.failOn = ""
	d, err := store.Sync(ctx, n, path, "go")
	if err != nil || d.Version != 2 {
		t.Fatalf("failed didChange should not increment version: %#v, %v", d, err)
	}
	params := n.items[len(n.items)-1].(map[string]any)
	id := params["textDocument"].(protocol.VersionedTextDocumentIdentifier)
	if id.Version != 2 { t.Fatalf("notified wrong version %d", id.Version) }
}

func TestSyncRetriesFailedDidOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.py")
	if err := os.WriteFile(path, []byte("value=1\n"), 0600); err != nil { t.Fatal(err) }
	store := NewStore()
	n := &fakeNotifier{generation: 1, failOn: "textDocument/didOpen"}
	if _, err := store.Sync(context.Background(), n, path, "python"); err == nil {
		t.Fatal("expected send failure")
	}
	n.failOn = ""
	if _, err := store.Sync(context.Background(), n, path, "python"); err != nil || len(n.calls) != 1 || n.calls[0] != "textDocument/didOpen" {
		t.Fatalf("didOpen was not retried: %v %v", err, n.calls)
	}
}
