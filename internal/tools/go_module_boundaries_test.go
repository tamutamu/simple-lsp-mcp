package tools

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestGoModuleBoundariesConservativelyFlagsNestedGoModules(t *testing.T) {
	root := t.TempDir()
	for _, path := range []string{"go.mod", "api/go.mod", "vendor/pkg/go.mod", ".scratch/ignored/go.mod"} {
		absolute := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(absolute, []byte("module test\n\ngo 1.26\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	found, err := goModuleBoundaries(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"api/go.mod", "go.mod"}
	if !reflect.DeepEqual(found, want) {
		t.Fatalf("modules=%v, want %v", found, want)
	}
	if err := os.Remove(filepath.Join(root, "api/go.mod")); err != nil {
		t.Fatal(err)
	}
	found, err = goModuleBoundaries(context.Background(), root)
	if err != nil || !reflect.DeepEqual(found, []string{"go.mod"}) {
		t.Fatalf("single module incorrectly treated as multi-module: %v, %v", found, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := goModuleBoundaries(ctx, root); err == nil {
		t.Fatal("cancelled module boundary traversal silently succeeded")
	}
}
