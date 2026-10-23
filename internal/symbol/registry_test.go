package symbol

import (
	"fmt"
	"testing"
)

func TestRegistryReusesIdentityAndRefreshesRecord(t *testing.T) {
	r := New()
	a := Record{SessionKey: "go", Path: "a.go", FileHash: "first", Name: "Thing", Kind: "function", Detail: "old"}
	id := r.Register(a)
	a.Detail = "new"
	if got := r.Register(a); got != id {
		t.Fatalf("same identity returned %s instead of %s", got, id)
	}
	got, err := r.Get(id)
	if err != nil || got.Detail != "new" {
		t.Fatalf("record was not refreshed: %+v %v", got, err)
	}
	a.FileHash = "second"
	if next := r.Register(a); next == id {
		t.Fatal("changed file hash reused stale ID")
	}
	if len(r.records) != 2 {
		t.Fatalf("expected two records, got %d", len(r.records))
	}
}

func TestRegistryLRUEviction(t *testing.T) {
	r := New()
	first := r.Register(Record{Path: "first.go"})
	for i := 0; i < maxRegistryEntries-1; i++ {
		r.Register(Record{Path: fmt.Sprintf("file-%d.go", i)})
	}
	if _, err := r.Get(first); err != nil {
		t.Fatalf("missing first: %v", err)
	}
	extra := r.Register(Record{Path: "extra.go"})
	if _, err := r.Get(first); err != nil {
		t.Fatalf("recently used record evicted: %v", err)
	}
	if _, err := r.Get(extra); err != nil {
		t.Fatalf("new record missing: %v", err)
	}
	if len(r.records) != maxRegistryEntries || len(r.identities) != maxRegistryEntries {
		t.Fatal("registry grew past capacity")
	}
	if _, err := r.Get(r.identities[identity{path: "file-0.go"}]); err == nil {
		t.Fatal("oldest entry should have been evicted")
	}
}
