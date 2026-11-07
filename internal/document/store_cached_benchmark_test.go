package document

import (
	"bytes"
	"context"
	"testing"
)

// BenchmarkWarmRepeatedSync isolates redundant validation and SHA-256 work
// after a warmed source has already been opened by the language server.
// Both sub-benchmarks have identical contents and do not notify again;
// only the backing buffer identity differs.
func BenchmarkWarmRepeatedSync(b *testing.B) {
	source := bytes.Repeat([]byte("func target() { return referencedSymbol }\n"), 4096)
	for _, tc := range []struct {
		name string
		reuseExactBuffer bool
	}{
		{name: "fresh-equal-buffer", reuseExactBuffer: false},
		{name: "cached-identical-buffer", reuseExactBuffer: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			store := NewStore()
			notifier := &fakeNotifier{generation: 1}
			ctx := context.Background()
			path := "/benchmark/unchanged.go"
			if _, err := store.SyncBytes(ctx, notifier, path, "go", source); err != nil {
				b.Fatal(err)
			}
			other := append([]byte(nil), source...)
			b.SetBytes(int64(len(source)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				payload := other
				if tc.reuseExactBuffer {
					payload = source
				}
				if _, err := store.SyncBytes(ctx, notifier, path, "go", payload); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
