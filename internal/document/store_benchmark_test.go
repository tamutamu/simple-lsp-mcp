package document

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkWarmCandidateSync measures the *file IO and document sync*
// component of project reference warming. It is not a coding-agent
// benchmark. Both variants scan the same file for the same identifier;
// the legacy variant rereads the matched source during Sync.
func BenchmarkWarmCandidateSync(b *testing.B) {
	root := b.TempDir()
	path := filepath.Join(root, "candidate.py")
	source := append(bytes.Repeat([]byte("# realistic source line and surrounding context\n"), 4096), []byte("def sought_symbol(): return 1\n")...)
	if err := os.WriteFile(path, source, 0600); err != nil {
		b.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		reuseBytes bool
	}{
		{name: "legacy-reread", reuseBytes: false},
		{name: "single-read", reuseBytes: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			store := NewStore()
			notifier := &fakeNotifier{generation: 1}
			ctx := context.Background()
			b.SetBytes(int64(len(source)))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				content, err := os.ReadFile(path)
				if err != nil { b.Fatal(err) }
				if !bytes.Contains(content, []byte("sought_symbol")) {
					b.Fatal("fixture no longer matches")
				}
				if tc.reuseBytes {
					_, err = store.SyncBytes(ctx, notifier, path, "python", content)
				} else {
					_, err = store.Sync(ctx, notifier, path, "python")
				}
				if err != nil { b.Fatal(err) }
			}
		})
	}
}
