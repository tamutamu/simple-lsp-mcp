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
	if f.failOn == method {
		return errors.New("simulated failed send")
	}
	f.calls = append(f.calls, method)
	f.items = append(f.items, params)
	return nil
}

func TestSyncIsolatedPerSessionAndRestartGeneration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "example.py")
	if err := os.WriteFile(path, []byte("x = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
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
	if err := os.WriteFile(path, []byte("package x\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store := NewStore()
	n := &fakeNotifier{generation: 1}
	ctx := context.Background()
	if _, err := store.Sync(ctx, n, path, "go"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package y\n"), 0600); err != nil {
		t.Fatal(err)
	}
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
	if id.Version != 2 {
		t.Fatalf("notified wrong version %d", id.Version)
	}
}

func TestSyncRetriesFailedDidOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.py")
	if err := os.WriteFile(path, []byte("value=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
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

func TestSyncBytesUsesAlreadyReadSourceSnapshot(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "example.ts")
	if err := os.WriteFile(path, []byte("const value = 1;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// A discovery scan has read the file. The sync layer must not silently
	// reopen it and substitute newer bytes from a second filesystem read.
	alreadyRead := []byte("const value = 1;\n")
	if err := os.WriteFile(path, []byte("const value = 2;\n"), 0600); err != nil {
		t.Fatal(err)
	}
	client := &fakeNotifier{generation: 1}
	store := NewStore()
	d, err := store.SyncBytes(context.Background(), client, path, "typescript", alreadyRead)
	if err != nil || string(d.Text) != string(alreadyRead) {
		t.Fatalf("SyncBytes read file a second time: %#v, %v", d, err)
	}
	if len(client.items) != 1 {
		t.Fatalf("notifications: %#v", client.calls)
	}
	item := client.items[0].(map[string]any)["textDocument"].(protocol.TextDocumentItem)
	if item.Text != string(alreadyRead) {
		t.Fatalf("unexpected synchronized document %q", item.Text)
	}
	// The normal Sync path still observes subsequent disk edits.
	d, err = store.Sync(context.Background(), client, path, "typescript")
	if err != nil || d.Version != 2 || string(d.Text) != "const value = 2;\n" {
		t.Fatalf("normal Sync did not detect disk edit: %#v, %v", d, err)
	}
}
func TestRevisionAdvancesOnlyAfterSuccessfulNotifications(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	n := &fakeNotifier{generation: 1}
	path := filepath.Join(t.TempDir(), "changed.ts")
	initial := []byte("export const id = 1;\n")
	updated := []byte("export const id = 2;\n")
	if got := store.Revision(n); got != 0 {
		t.Fatalf("unopened revision=%d", got)
	}
	if _, err := store.SyncBytes(ctx, n, path, "typescript", initial); err != nil {
		t.Fatal(err)
	}
	if got := store.Revision(n); got != 1 {
		t.Fatalf("didOpen revision=%d", got)
	}
	if _, err := store.SyncBytes(ctx, n, path, "typescript", initial); err != nil {
		t.Fatal(err)
	}
	if got := store.Revision(n); got != 1 {
		t.Fatalf("unchanged revision=%d", got)
	}

	n.failOn = "textDocument/didChange"
	if _, err := store.SyncBytes(ctx, n, path, "typescript", updated); err == nil {
		t.Fatal("expected failed didChange")
	}
	if got := store.Revision(n); got != 1 {
		t.Fatalf("failed notification advanced revision=%d", got)
	}
	n.failOn = ""
	if _, err := store.SyncBytes(ctx, n, path, "typescript", updated); err != nil {
		t.Fatal(err)
	}
	if got := store.Revision(n); got != 2 {
		t.Fatalf("didChange revision=%d", got)
	}
	n.generation++
	if _, err := store.SyncBytes(ctx, n, path, "typescript", updated); err != nil {
		t.Fatal(err)
	}
	if got := store.Revision(n); got != 3 {
		t.Fatalf("restart didOpen revision=%d", got)
	}
}
