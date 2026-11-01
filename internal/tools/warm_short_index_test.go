package tools

import (
	"reflect"
	"strings"
	"testing"
)

func TestWarmIndexShortNeedlesSkipRepeatedLargeFileReads(t *testing.T) {
	e, p, root := warmIndexFixture(t)
	line := "const alpha = 1;\n"
	large := strings.Repeat(line, warmIndexMaxCachedBytes/len(line)+1)
	warmWrite(t, root, "large.ts", large)

	first, cold := warmScan(t, e, p, root, "alpha")
	if !reflect.DeepEqual(first, []string{"large.ts"}) || cold.FilesRead != 1 {
		t.Fatalf("cold scan: found=%v stats=%+v", first, cold)
	}

	// Before the fix, a 1- or 2-byte needle always passed the trigram
	// prefilter, causing the entire over-budget file to be reread.
	for _, name := range []string{"z", "zz"} {
		found, stats := warmScan(t, e, p, root, name)
		if len(found) != 0 || stats.FilesRead != 0 || stats.SkippedRead != 1 {
			t.Fatalf("negative short-name %q reread unchanged source: found=%v stats=%+v", name, found, stats)
		}
	}
	// A possible match must still load the original source for exact
	// identifier-boundary verification and LSP synchronization.
	for _, name := range []string{"a", "al"} {
		found, stats := warmScan(t, e, p, root, name)
		if stats.FilesRead != 1 || len(found) != 0 {
			// Neither "a" nor "al" is a whole identifier in this source.
			t.Fatalf("short-name false positive %q: found=%v stats=%+v", name, found, stats)
		}
	}
}

func TestWarmShortIndexNeverExcludesActualByteOrPair(t *testing.T) {
	for _, source := range []string{"", "a", "ab", "alpha beta", "日本語🚀 alpha\n"} {
		data := []byte(source)
		var index warmFileIndex
		warmBuildIndex(&index, data)
		for start := range data {
			for end := start + 1; end <= len(data) && end <= start+2; end++ {
				if !warmIndexedMayContain(&index, data[start:end]) {
					t.Fatalf("false negative for %q in %q", data[start:end], source)
				}
			}
		}
	}
}
