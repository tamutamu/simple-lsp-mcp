package tools

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/tamutamu/simple-lsp-mcp/internal/language"
	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

func warmIndexFixture(t *testing.T) (*Engine, language.Profile, string) {
	t.Helper()
	ws, err := workspace.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	p, err := language.FromPath("sample.ts")
	if err != nil {
		t.Fatal(err)
	}
	return &Engine{WS: ws}, p, ws.Root()
}

func warmWrite(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func warmScan(t *testing.T, e *Engine, p language.Profile, root, name string) ([]string, warmScanStats) {
	t.Helper()
	var found []string
	stats, err := e.scanProjectWarmCandidates(context.Background(), p, root, []byte(name),
		func(_ string, rel string, _ language.Profile, content []byte) error {
			if !bytes.Contains(content, []byte(name)) {
				t.Fatalf("false-positive match %s for %s", rel, name)
			}
			found = append(found, rel)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	return found, stats
}

func TestWarmIndexTracksEditsAdditionsRemovalsAndIgnoredDirectories(t *testing.T) {
	e, p, root := warmIndexFixture(t)
	warmWrite(t, root, "a.ts", "export const alpha = 1;\n")
	warmWrite(t, root, "b.ts", "export const beta = 1;\n")
	warmWrite(t, root, "node_modules/ignored.ts", "export const omega = 1;\n")
	found, stats := warmScan(t, e, p, root, "alpha")
	if !reflect.DeepEqual(found, []string{"a.ts"}) || stats.FilesRead != 2 || stats.SourceFiles != 2 {
		t.Fatalf("first query found=%v stats=%+v", found, stats)
	}
	found, stats = warmScan(t, e, p, root, "omega")
	if len(found) != 0 || stats.FilesRead != 0 || stats.SkippedRead != 2 {
		t.Fatalf("unchanged negative query found=%v stats=%+v", found, stats)
	}
	warmWrite(t, root, "b.ts", "export const omega = 12345;\n")
	found, stats = warmScan(t, e, p, root, "omega")
	if !reflect.DeepEqual(found, []string{"b.ts"}) || stats.FilesRead != 1 || stats.SkippedRead != 1 {
		t.Fatalf("edited source found=%v stats=%+v", found, stats)
	}
	warmWrite(t, root, "c.ts", "export const omega = 2;\n")
	found, stats = warmScan(t, e, p, root, "omega")
	if !reflect.DeepEqual(found, []string{"b.ts", "c.ts"}) || stats.FilesRead != 1 {
		t.Fatalf("added source found=%v stats=%+v", found, stats)
	}
	if err := os.Remove(filepath.Join(root, "b.ts")); err != nil {
		t.Fatal(err)
	}
	found, stats = warmScan(t, e, p, root, "omega")
	if !reflect.DeepEqual(found, []string{"c.ts"}) || stats.SourceFiles != 2 {
		t.Fatalf("removed source found=%v stats=%+v", found, stats)
	}
}

func TestWarmIndexReusesPositiveMatchesAndInvalidatesChangedBytes(t *testing.T) {
	e, p, root := warmIndexFixture(t)
	warmWrite(t, root, "a.ts", "const alpha = 123;\n")
	first, stats := warmScan(t, e, p, root, "alpha")
	if !reflect.DeepEqual(first, []string{"a.ts"}) || stats.FilesRead != 1 {
		t.Fatalf("first match: %v %+v", first, stats)
	}
	second, stats := warmScan(t, e, p, root, "alpha")
	if !reflect.DeepEqual(second, first) || stats.FilesRead != 0 {
		t.Fatalf("positive candidate was reread: %v %+v", second, stats)
	}
	warmWrite(t, root, "a.ts", "const gamma = 12345;\n")
	third, stats := warmScan(t, e, p, root, "alpha")
	if len(third) != 0 || stats.FilesRead != 1 {
		t.Fatalf("stale cached candidate after source change: %v %+v", third, stats)
	}
}

func TestWarmIndexInvalidatesReplacementEvenWithPreservedSizeAndMtime(t *testing.T) {
	e, p, root := warmIndexFixture(t)
	path := filepath.Join(root, "sample.ts")
	warmWrite(t, root, "sample.ts", "const alpha = 123;\n")
	_, _ = warmScan(t, e, p, root, "alpha")
	original, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(root, "replacement.tmp")
	if err := os.WriteFile(replacement, []byte("const omega = 123;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(replacement, original.ModTime(), original.ModTime()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	found, stats := warmScan(t, e, p, root, "omega")
	if !reflect.DeepEqual(found, []string{"sample.ts"}) || stats.FilesRead != 1 {
		t.Fatalf("replaced file missed: found=%v stats=%+v", found, stats)
	}
}

func TestWarmIndexFailedScanCannotPoisonSubsequentResults(t *testing.T) {
	e, p, root := warmIndexFixture(t)
	warmWrite(t, root, "a.ts", "const alpha = 1;\n")
	warmWrite(t, root, "b.ts", "const alpha = 2;\n")
	failure := errors.New("LSP synchronization failed")
	_, err := e.scanProjectWarmCandidates(context.Background(), p, root, []byte("alpha"),
		func(_ string, _ string, _ language.Profile, _ []byte) error { return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("expected callback failure: %v", err)
	}
	found, stats := warmScan(t, e, p, root, "alpha")
	if len(found) != 2 || stats.FilesRead != 2 {
		t.Fatalf("incomplete scan was cached: found=%v stats=%+v", found, stats)
	}
}

func TestWarmBloomNeverExcludesAContainedSubstring(t *testing.T) {
	contents := [][]byte{
		[]byte(""), []byte("ab"), []byte("abc"), []byte("alpha beta\nfooBar"),
		[]byte("日本語と🚀と変数名"), bytes.Repeat([]byte("0123456789_"), 150),
	}
	for _, content := range contents {
		var bits [warmBloomBytes]byte
		warmBloomAdd(&bits, content)
		for start := 0; start < len(content); start++ {
			for end := start + 1; end <= len(content) && end <= start+24; end++ {
				needle := content[start:end]
				if !warmBloomMayContain(&bits, needle) {
					t.Fatalf("false negative: content=%q needle=%q", content, needle)
				}
			}
		}
	}
}

func BenchmarkProjectWarmIndexMiss(b *testing.B) {
	root := b.TempDir()
	ws, err := workspace.Open(root)
	if err != nil {
		b.Fatal(err)
	}
	p, err := language.FromPath("source.ts")
	if err != nil {
		b.Fatal(err)
	}
	e := &Engine{WS: ws}
	for i := 0; i < 100; i++ {
		path := filepath.Join(root, strings.Repeat("a", i%7), "source"+string(rune('A'+i%26))+string(rune('a'+i/26))+".ts")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, bytes.Repeat([]byte("const nothing = 42;\n"), 100), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	scan := func() warmScanStats {
		stats, err := e.scanProjectWarmCandidates(context.Background(), p, root, []byte("absentUniqueIdentifierXYZ"),
			func(_ string, _ string, _ language.Profile, _ []byte) error { return nil })
		if err != nil {
			b.Fatal(err)
		}
		return stats
	}
	scan()
	b.ResetTimer()
	var reads int
	for i := 0; i < b.N; i++ {
		reads += scan().FilesRead
	}
	b.ReportMetric(float64(reads)/float64(b.N), "reads/op")
}

func BenchmarkProjectWarmLegacyFullScanMiss(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 100; i++ {
		path := filepath.Join(root, strings.Repeat("a", i%7), "source"+string(rune('A'+i%26))+string(rune('a'+i/26))+".ts")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, bytes.Repeat([]byte("const nothing = 42;\n"), 100), 0o600); err != nil {
			b.Fatal(err)
		}
	}
	needle := []byte("absentUniqueIdentifierXYZ")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !entry.Type().IsRegular() || filepath.Ext(path) != ".ts" {
				return nil
			}
			if _, err := entry.Info(); err != nil {
				return err
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Contains(content, needle) {
				b.Fatal("unexpected match in negative-control benchmark")
			}
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(100, "reads/op")
}

func BenchmarkProjectWarmIndexHit(b *testing.B) {
	root := b.TempDir()
	ws, err := workspace.Open(root)
	if err != nil { b.Fatal(err) }
	p, err := language.FromPath("sample.ts")
	if err != nil { b.Fatal(err) }
	e := &Engine{WS: ws}
	for i := 0; i < 100; i++ {
		path := filepath.Join(root, strings.Repeat("a", i%7), "source"+string(rune('A'+i%26))+string(rune('a'+i/26))+".ts")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { b.Fatal(err) }
		src := bytes.Repeat([]byte("const nothing = 42;\n"), 100)
		if i == 0 { src = append(src, []byte("const uniqueNeedleXYZ = 1;\n")...) }
		if err := os.WriteFile(path, src, 0o600); err != nil { b.Fatal(err) }
	}
	scan := func() warmScanStats {
		stats, err := e.scanProjectWarmCandidates(context.Background(), p, root, []byte("uniqueNeedleXYZ"),
			func(_ string, _ string, _ language.Profile, _ []byte) error { return nil })
		if err != nil { b.Fatal(err) }
		if stats.Matches != 1 { b.Fatalf("expected one match, found %d", stats.Matches) }
		return stats
	}
	scan()
	b.ResetTimer()
	var reads int
	for i := 0; i < b.N; i++ { reads += scan().FilesRead }
	b.ReportMetric(float64(reads)/float64(b.N), "reads/op")
}

func BenchmarkProjectWarmLegacyFullScanHit(b *testing.B) {
	root := b.TempDir()
	for i := 0; i < 100; i++ {
		path := filepath.Join(root, strings.Repeat("a", i%7), "source"+string(rune('A'+i%26))+string(rune('a'+i/26))+".ts")
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil { b.Fatal(err) }
		src := bytes.Repeat([]byte("const nothing = 42;\n"), 100)
		if i == 0 { src = append(src, []byte("const uniqueNeedleXYZ = 1;\n")...) }
		if err := os.WriteFile(path, src, 0o600); err != nil { b.Fatal(err) }
	}
	needle := []byte("uniqueNeedleXYZ")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		matches := 0
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil { return walkErr }
			if !entry.Type().IsRegular() || filepath.Ext(path) != ".ts" { return nil }
			if _, err := entry.Info(); err != nil { return err }
			content, err := os.ReadFile(path)
			if err != nil { return err }
			if bytes.Contains(content, needle) { matches++ }
			return nil
		})
		if err != nil { b.Fatal(err) }
		if matches != 1 { b.Fatalf("expected one match, got %d", matches) }
	}
	b.ReportMetric(100, "reads/op")
}
