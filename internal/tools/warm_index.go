package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"github.com/tamutamu/simple-lsp-mcp/internal/language"
)

// Cache small sources directly; use a compact per-source substring filter
// only when the bounded byte budget is exceeded. False positives are allowed,
// but false negatives are not: every possible hit is verified with bytes.Contains.
// The filesystem is still walked and stat'ed for each query so that additions,
// removals, edits, and replacements invalidate stale filters.
const (
	warmBloomBytes       = 256
	warmIndexMaxProjects = 4
	warmIndexMaxCachedBytes = 8 << 20
)

type warmFileIndex struct {
	info       fs.FileInfo
	bloom      [warmBloomBytes]byte
	shortBytes [32]byte           // Exact presence of one-byte names.
	shortPairs [warmBloomBytes]byte // Two-byte name Bloom filter.
	content    []byte              // Source bytes cached opportunistically, bounded per project.
}

type warmSourceIndex struct {
	mu        sync.Mutex
	snapshots map[string]map[string]warmFileIndex
	order     []string
}

type warmScanStats struct {
	SourceFiles int
	SourceBytes int64
	FilesRead   int
	BytesRead   int64
	SkippedRead int
	Matches     int
}

func (idx *warmSourceIndex) snapshot(key string) map[string]warmFileIndex {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	return idx.snapshots[key] // Published maps are immutable.
}

func (idx *warmSourceIndex) publish(key string, files map[string]warmFileIndex) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if idx.snapshots == nil {
		idx.snapshots = make(map[string]map[string]warmFileIndex)
	}
	for i, k := range idx.order {
		if k == key {
			idx.order = append(idx.order[:i], idx.order[i+1:]...)
			break
		}
	}
	idx.order = append(idx.order, key)
	idx.snapshots[key] = files
	if len(idx.order) > warmIndexMaxProjects {
		delete(idx.snapshots, idx.order[0])
		idx.order = idx.order[1:]
	}
}

// Check file identity, size, mode, mtime and (when exposed) change time.
// Do not compare full os.FileInfo.Sys(): atime changes when we merely read a
// source, which would invalidate the index on every subsequent lookup.
// On filesystems that conceal change time, a same-size write that spoofs
// mtime may remain invisible; no stat-only cache can detect such writes.
func warmSameFile(a, b fs.FileInfo) bool {
	return a != nil && b != nil &&
		a.Size() == b.Size() && a.Mode() == b.Mode() &&
		a.ModTime().Equal(b.ModTime()) && os.SameFile(a, b) &&
		reflect.DeepEqual(warmChangeTime(a), warmChangeTime(b))
}

func warmChangeTime(info fs.FileInfo) any {
	value := reflect.ValueOf(info.Sys())
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return nil
	}
	// Unix variants expose Ctim/Ctimespec; some platforms use Ctime.
	for _, name := range []string{"Ctim", "Ctimespec", "Ctime", "ChangeTime"} {
		field := value.FieldByName(name)
		if field.IsValid() && field.CanInterface() {
			return field.Interface()
		}
	}
	return nil
}

func warmTrigram(v []byte) (int, int) {
	x := uint32(v[0])<<16 | uint32(v[1])<<8 | uint32(v[2])
	h := x * 0x9e3779b1
	return int((h >> 21) & 2047), int(((h ^ (h >> 11)) * 0x85ebca6b >> 21) & 2047)
}

func warmBloomAdd(bits *[warmBloomBytes]byte, data []byte) {
	for i := 0; i+2 < len(data); i++ {
		a, b := warmTrigram(data[i:])
		bits[a/8] |= 1 << (a % 8)
		bits[b/8] |= 1 << (b % 8)
	}
}

func warmBloomMayContain(bits *[warmBloomBytes]byte, needle []byte) bool {
	if len(needle) < 3 {
		return true // No filtering for one- and two-byte names.
	}
	for i := 0; i+2 < len(needle); i++ {
		a, b := warmTrigram(needle[i:])
		if bits[a/8]&(1<<(a%8)) == 0 || bits[b/8]&(1<<(b%8)) == 0 {
			return false
		}
	}
	return true
}

// warmContainsIdentifier filters lexical candidates before LSP synchronization.
// UTF-8 identifier neighbors must be decoded as runes: treating every non-ASCII
// byte as a boundary falsely matches "id" inside "日本id語", potentially opening
// hundreds of unrelated files and hitting the 512-candidate fail-closed limit.
// Invalid UTF-8 is conservatively treated as a boundary (extra candidates,
// never a missed valid reference). The LSP still determines symbol identity.
func warmContainsIdentifier(content, name []byte) bool {
	if len(name) == 0 {
		return false
	}
	for offset := 0; offset <= len(content)-len(name); {
		relative := bytes.Index(content[offset:], name)
		if relative < 0 {
			return false
		}
		start := offset + relative
		end := start + len(name)
		if warmIdentifierBoundary(content, start, true) &&
			warmIdentifierBoundary(content, end, false) {
			return true
		}
		offset = start + 1
	}
	return false
}

// Treat only known identifier-continuation runes as non-boundaries. Unicode
// whitespace, punctuation, symbols and malformed bytes remain possible
// boundaries so the candidate filter cannot discard valid references.
func warmIdentifierBoundary(content []byte, at int, before bool) bool {
	if at == 0 || at == len(content) {
		return true
	}
	if before {
		if content[at-1] < utf8.RuneSelf {
			return !warmIdentifierByte(content[at-1])
		}
		r, size := utf8.DecodeLastRune(content[:at])
		return size == 1 && r == utf8.RuneError || !warmIdentifierRune(r)
	}
	if content[at] < utf8.RuneSelf {
		return !warmIdentifierByte(content[at])
	}
	r, size := utf8.DecodeRune(content[at:])
	return size == 1 && r == utf8.RuneError || !warmIdentifierRune(r)
}

func warmIdentifierRune(r rune) bool {
	return r == '_' || r == '

// Match the actual source spelling first. JS/TS permits Unicode escapes
// inside identifiers, including mixed literal and escaped characters. Such
// files must be opened before semantic references or rename is requested.
func warmContainsProjectIdentifier(content, name []byte, sessionKey string) bool {
	if warmContainsIdentifier(content, name) {
		return true
	}
	return sessionKey == "typescript-javascript" &&
		bytes.Contains(content, []byte(`\u`)) &&
		warmContainsIdentifier(warmDecodeJSEscapes(content), name)
}

// warmBuildIndex records short names as well as trigrams. Previously the
// trigram index could never reject a one- or two-byte needle: a repeat search
// reread every uncached (over-budget) source, even for absent names. The byte
// bitmap is exact; pair hashes may have false positives, never false negatives.
// Only uncached files pay the cost of constructing these filters.
func warmBuildIndex(idx *warmFileIndex, content []byte) {
	warmBuildIndexBytes(idx, content)
	// The Bloom filter must cover decoded spellings too. Otherwise an
	// uncached large source with i\u0064 is incorrectly rejected for "id"
	// before the lexical candidate matcher can examine it.
	if bytes.Contains(content, []byte(`\u`)) {
		warmBuildIndexBytes(idx, warmDecodeJSEscapes(content))
	}
}

func warmBuildIndexBytes(idx *warmFileIndex, content []byte) {
	warmBloomAdd(&idx.bloom, content)
	for i, b := range content {
		idx.shortBytes[b/8] |= 1 << (b % 8)
		if i+1 < len(content) {
			pair := [3]byte{0, b, content[i+1]}
			a, c := warmTrigram(pair[:])
			idx.shortPairs[a/8] |= 1 << (a % 8)
			idx.shortPairs[c/8] |= 1 << (c % 8)
		}
	}
}

func warmIndexedMayContain(idx *warmFileIndex, needle []byte) bool {
	switch len(needle) {
	case 0:
		return false
	case 1:
		b := needle[0]
		return idx.shortBytes[b/8]&(1<<(b%8)) != 0
	case 2:
		pair := [3]byte{0, needle[0], needle[1]}
		a, c := warmTrigram(pair[:])
		return idx.shortPairs[a/8]&(1<<(a%8)) != 0 &&
			idx.shortPairs[c/8]&(1<<(c%8)) != 0
	default:
		return warmBloomMayContain(&idx.bloom, needle)
	}
}

// readStableWarmSource verifies that the bytes we index and send to an LSP
// belong to the same file version observed during the directory walk. A
// replacement, truncation, or in-place edit between stat and read must fail
// closed instead of publishing an index for bytes paired with stale metadata.
// Open the file once so a symlink swap cannot make us silently read a different
// inode; bound growth during concurrent writes.
func readStableWarmSource(path string, expected fs.FileInfo) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !warmSameFile(expected, before) {
		return nil, fmt.Errorf("source changed before reading: %s", path)
	}
	if before.Size() < 0 || before.Size() > projectWarmMaxBytes {
		return nil, fmt.Errorf("source size exceeded warm-up limit: %s", path)
	}
	// Allocate once using the verified file size instead of growing an
	// io.ReadAll buffer, which can double memory use for large sources.
	content := make([]byte, int(before.Size()))
	if _, err := io.ReadFull(f, content); err != nil {
		return nil, fmt.Errorf("source changed while reading %s: %w", path, err)
	}
	var extra [1]byte
	if n, err := f.Read(extra[:]); n != 0 || err != io.EOF {
		return nil, fmt.Errorf("source grew while reading %s: %v", path, err)
	}
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !warmSameFile(before, after) {
		return nil, fmt.Errorf("source changed while reading: %s", path)
	}
	return content, nil
}

// scanProjectWarmCandidates discovers sources anew on every call, but reads
// their contents only for newly modified files or possible substring matches.
// A failed or interrupted scan is never published as a complete index.
// The callback performs the actual LSP synchronization, not this index.
func (e *Engine) scanProjectWarmCandidates(
	ctx context.Context, p language.Profile, root string, needle []byte,
	onMatch func(path, rel string, profile language.Profile, content []byte) error,
) (warmScanStats, error) {
	var stats warmScanStats
	key := root + "\x00" + p.SessionKey
	previous := e.warmIndex.snapshot(key)
	next := make(map[string]warmFileIndex, len(previous))
	cachedBytes := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return core.WithCause(core.IncompleteSearch, "project warm-up could not inspect source files", walkErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && workspaceListExcludedDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(e.WS.Root(), path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		candidateProfile, err := language.FromPath(rel)
		if err != nil || candidateProfile.SessionKey != p.SessionKey {
			return nil
		}
		stats.SourceFiles++
		if stats.SourceFiles > projectWarmMaxFiles {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("project warm-up exceeded %d source files; narrow the workspace/profile", projectWarmMaxFiles))
		}
		info, err := entry.Info()
		if err != nil {
			return core.WithCause(core.IncompleteSearch, "project warm-up could not stat "+rel, err)
		}
		stats.SourceBytes += info.Size()
		if stats.SourceBytes > projectWarmMaxBytes {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("project warm-up exceeded %d MiB of source; narrow the workspace/profile", projectWarmMaxBytes>>20))
		}

		old, found := previous[path]
		unchanged := found && warmSameFile(old.info, info)
		current := old
		var content []byte
		if unchanged {
			if old.content != nil {
				// A newly inserted/changed earlier file may already have
				// consumed the remaining cache budget. Never blindly carry
				// forward old cached bytes: that grows the published
				// snapshot past the advertised 8 MiB bound.
				content = old.content
				stats.SkippedRead++
				if len(content) <= warmIndexMaxCachedBytes-cachedBytes {
					cachedBytes += len(content)
				} else {
					// Evict this source's byte snapshot while preserving
					// the negative-query filter. We have the bytes in
					// memory already, so this does not cause disk I/O.
					current = warmFileIndex{info: info}
					warmBuildIndex(&current, content)
				}
			} else if !warmIndexedMayContain(&old, needle) {
				next[path] = old
				stats.SkippedRead++
				return nil
			}
		}
		if content == nil {
			var err error
			content, err = readStableWarmSource(path, info)
			if err != nil {
				return core.WithCause(core.IncompleteSearch, "project warm-up could not read "+rel, err)
			}
			stats.FilesRead++
			stats.BytesRead += int64(len(content))
		}
		if !unchanged {
			current = warmFileIndex{info: info}
		}
		if current.content == nil && len(content) <= warmIndexMaxCachedBytes-cachedBytes {
			// Prefer the actual source bytes (up to a strict 8 MiB bound)
			// over calculating thousands of substring hashes during cold
			// startup. Only over-budget files need a Bloom filter.
			current.content = content
			cachedBytes += len(content)
		}
		if !unchanged && current.content == nil {
			warmBuildIndex(&current, content)
		}
		next[path] = current
		if !warmContainsProjectIdentifier(content, needle, p.SessionKey) {
			return nil
		}
		stats.Matches++
		if stats.Matches > projectWarmMaxMatches {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("project warm-up found more than %d candidate files; narrow the workspace/profile", projectWarmMaxMatches))
		}
		if err := onMatch(path, rel, candidateProfile, content); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return stats, err
	}
	e.warmIndex.publish(key, next)
	return stats, nil
}

 || r == '\u200c' || r == '\u200d' ||
		unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) ||
		unicode.Is(unicode.Pc, r)
}

func warmIdentifierByte(b byte) bool {
	return b == '_' || b == '

// Match the actual source spelling first. JS/TS permits Unicode escapes
// inside identifiers, including mixed literal and escaped characters. Such
// files must be opened before semantic references or rename is requested.
func warmContainsProjectIdentifier(content, name []byte, sessionKey string) bool {
	if warmContainsIdentifier(content, name) {
		return true
	}
	return sessionKey == "typescript-javascript" &&
		bytes.Contains(content, []byte(`\u`)) &&
		warmContainsIdentifier(warmDecodeJSEscapes(content), name)
}

// warmBuildIndex records short names as well as trigrams. Previously the
// trigram index could never reject a one- or two-byte needle: a repeat search
// reread every uncached (over-budget) source, even for absent names. The byte
// bitmap is exact; pair hashes may have false positives, never false negatives.
// Only uncached files pay the cost of constructing these filters.
func warmBuildIndex(idx *warmFileIndex, content []byte) {
	warmBuildIndexBytes(idx, content)
	// The Bloom filter must cover decoded spellings too. Otherwise an
	// uncached large source with i\u0064 is incorrectly rejected for "id"
	// before the lexical candidate matcher can examine it.
	if bytes.Contains(content, []byte(`\u`)) {
		warmBuildIndexBytes(idx, warmDecodeJSEscapes(content))
	}
}

func warmBuildIndexBytes(idx *warmFileIndex, content []byte) {
	warmBloomAdd(&idx.bloom, content)
	for i, b := range content {
		idx.shortBytes[b/8] |= 1 << (b % 8)
		if i+1 < len(content) {
			pair := [3]byte{0, b, content[i+1]}
			a, c := warmTrigram(pair[:])
			idx.shortPairs[a/8] |= 1 << (a % 8)
			idx.shortPairs[c/8] |= 1 << (c % 8)
		}
	}
}

func warmIndexedMayContain(idx *warmFileIndex, needle []byte) bool {
	switch len(needle) {
	case 0:
		return false
	case 1:
		b := needle[0]
		return idx.shortBytes[b/8]&(1<<(b%8)) != 0
	case 2:
		pair := [3]byte{0, needle[0], needle[1]}
		a, c := warmTrigram(pair[:])
		return idx.shortPairs[a/8]&(1<<(a%8)) != 0 &&
			idx.shortPairs[c/8]&(1<<(c%8)) != 0
	default:
		return warmBloomMayContain(&idx.bloom, needle)
	}
}

// readStableWarmSource verifies that the bytes we index and send to an LSP
// belong to the same file version observed during the directory walk. A
// replacement, truncation, or in-place edit between stat and read must fail
// closed instead of publishing an index for bytes paired with stale metadata.
// Open the file once so a symlink swap cannot make us silently read a different
// inode; bound growth during concurrent writes.
func readStableWarmSource(path string, expected fs.FileInfo) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !warmSameFile(expected, before) {
		return nil, fmt.Errorf("source changed before reading: %s", path)
	}
	if before.Size() < 0 || before.Size() > projectWarmMaxBytes {
		return nil, fmt.Errorf("source size exceeded warm-up limit: %s", path)
	}
	// Allocate once using the verified file size instead of growing an
	// io.ReadAll buffer, which can double memory use for large sources.
	content := make([]byte, int(before.Size()))
	if _, err := io.ReadFull(f, content); err != nil {
		return nil, fmt.Errorf("source changed while reading %s: %w", path, err)
	}
	var extra [1]byte
	if n, err := f.Read(extra[:]); n != 0 || err != io.EOF {
		return nil, fmt.Errorf("source grew while reading %s: %v", path, err)
	}
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !warmSameFile(before, after) {
		return nil, fmt.Errorf("source changed while reading: %s", path)
	}
	return content, nil
}

// scanProjectWarmCandidates discovers sources anew on every call, but reads
// their contents only for newly modified files or possible substring matches.
// A failed or interrupted scan is never published as a complete index.
// The callback performs the actual LSP synchronization, not this index.
func (e *Engine) scanProjectWarmCandidates(
	ctx context.Context, p language.Profile, root string, needle []byte,
	onMatch func(path, rel string, profile language.Profile, content []byte) error,
) (warmScanStats, error) {
	var stats warmScanStats
	key := root + "\x00" + p.SessionKey
	previous := e.warmIndex.snapshot(key)
	next := make(map[string]warmFileIndex, len(previous))
	cachedBytes := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return core.WithCause(core.IncompleteSearch, "project warm-up could not inspect source files", walkErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && workspaceListExcludedDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(e.WS.Root(), path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		candidateProfile, err := language.FromPath(rel)
		if err != nil || candidateProfile.SessionKey != p.SessionKey {
			return nil
		}
		stats.SourceFiles++
		if stats.SourceFiles > projectWarmMaxFiles {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("project warm-up exceeded %d source files; narrow the workspace/profile", projectWarmMaxFiles))
		}
		info, err := entry.Info()
		if err != nil {
			return core.WithCause(core.IncompleteSearch, "project warm-up could not stat "+rel, err)
		}
		stats.SourceBytes += info.Size()
		if stats.SourceBytes > projectWarmMaxBytes {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("project warm-up exceeded %d MiB of source; narrow the workspace/profile", projectWarmMaxBytes>>20))
		}

		old, found := previous[path]
		unchanged := found && warmSameFile(old.info, info)
		current := old
		var content []byte
		if unchanged {
			if old.content != nil {
				// A newly inserted/changed earlier file may already have
				// consumed the remaining cache budget. Never blindly carry
				// forward old cached bytes: that grows the published
				// snapshot past the advertised 8 MiB bound.
				content = old.content
				stats.SkippedRead++
				if len(content) <= warmIndexMaxCachedBytes-cachedBytes {
					cachedBytes += len(content)
				} else {
					// Evict this source's byte snapshot while preserving
					// the negative-query filter. We have the bytes in
					// memory already, so this does not cause disk I/O.
					current = warmFileIndex{info: info}
					warmBuildIndex(&current, content)
				}
			} else if !warmIndexedMayContain(&old, needle) {
				next[path] = old
				stats.SkippedRead++
				return nil
			}
		}
		if content == nil {
			var err error
			content, err = readStableWarmSource(path, info)
			if err != nil {
				return core.WithCause(core.IncompleteSearch, "project warm-up could not read "+rel, err)
			}
			stats.FilesRead++
			stats.BytesRead += int64(len(content))
		}
		if !unchanged {
			current = warmFileIndex{info: info}
		}
		if current.content == nil && len(content) <= warmIndexMaxCachedBytes-cachedBytes {
			// Prefer the actual source bytes (up to a strict 8 MiB bound)
			// over calculating thousands of substring hashes during cold
			// startup. Only over-budget files need a Bloom filter.
			current.content = content
			cachedBytes += len(content)
		}
		if !unchanged && current.content == nil {
			warmBuildIndex(&current, content)
		}
		next[path] = current
		if !warmContainsProjectIdentifier(content, needle, p.SessionKey) {
			return nil
		}
		stats.Matches++
		if stats.Matches > projectWarmMaxMatches {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("project warm-up found more than %d candidate files; narrow the workspace/profile", projectWarmMaxMatches))
		}
		if err := onMatch(path, rel, candidateProfile, content); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return stats, err
	}
	e.warmIndex.publish(key, next)
	return stats, nil
}

 ||
		b >= 'A' && b <= 'Z' ||
		b >= 'a' && b <= 'z' ||
		b >= '0' && b <= '9'
}

// Match the actual source spelling first. JS/TS permits Unicode escapes
// inside identifiers, including mixed literal and escaped characters. Such
// files must be opened before semantic references or rename is requested.
func warmContainsProjectIdentifier(content, name []byte, sessionKey string) bool {
	if warmContainsIdentifier(content, name) {
		return true
	}
	return sessionKey == "typescript-javascript" &&
		bytes.Contains(content, []byte(`\u`)) &&
		warmContainsIdentifier(warmDecodeJSEscapes(content), name)
}

// warmBuildIndex records short names as well as trigrams. Previously the
// trigram index could never reject a one- or two-byte needle: a repeat search
// reread every uncached (over-budget) source, even for absent names. The byte
// bitmap is exact; pair hashes may have false positives, never false negatives.
// Only uncached files pay the cost of constructing these filters.
func warmBuildIndex(idx *warmFileIndex, content []byte) {
	warmBuildIndexBytes(idx, content)
	// The Bloom filter must cover decoded spellings too. Otherwise an
	// uncached large source with i\u0064 is incorrectly rejected for "id"
	// before the lexical candidate matcher can examine it.
	if bytes.Contains(content, []byte(`\u`)) {
		warmBuildIndexBytes(idx, warmDecodeJSEscapes(content))
	}
}

func warmBuildIndexBytes(idx *warmFileIndex, content []byte) {
	warmBloomAdd(&idx.bloom, content)
	for i, b := range content {
		idx.shortBytes[b/8] |= 1 << (b % 8)
		if i+1 < len(content) {
			pair := [3]byte{0, b, content[i+1]}
			a, c := warmTrigram(pair[:])
			idx.shortPairs[a/8] |= 1 << (a % 8)
			idx.shortPairs[c/8] |= 1 << (c % 8)
		}
	}
}

func warmIndexedMayContain(idx *warmFileIndex, needle []byte) bool {
	switch len(needle) {
	case 0:
		return false
	case 1:
		b := needle[0]
		return idx.shortBytes[b/8]&(1<<(b%8)) != 0
	case 2:
		pair := [3]byte{0, needle[0], needle[1]}
		a, c := warmTrigram(pair[:])
		return idx.shortPairs[a/8]&(1<<(a%8)) != 0 &&
			idx.shortPairs[c/8]&(1<<(c%8)) != 0
	default:
		return warmBloomMayContain(&idx.bloom, needle)
	}
}

// readStableWarmSource verifies that the bytes we index and send to an LSP
// belong to the same file version observed during the directory walk. A
// replacement, truncation, or in-place edit between stat and read must fail
// closed instead of publishing an index for bytes paired with stale metadata.
// Open the file once so a symlink swap cannot make us silently read a different
// inode; bound growth during concurrent writes.
func readStableWarmSource(path string, expected fs.FileInfo) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !warmSameFile(expected, before) {
		return nil, fmt.Errorf("source changed before reading: %s", path)
	}
	if before.Size() < 0 || before.Size() > projectWarmMaxBytes {
		return nil, fmt.Errorf("source size exceeded warm-up limit: %s", path)
	}
	// Allocate once using the verified file size instead of growing an
	// io.ReadAll buffer, which can double memory use for large sources.
	content := make([]byte, int(before.Size()))
	if _, err := io.ReadFull(f, content); err != nil {
		return nil, fmt.Errorf("source changed while reading %s: %w", path, err)
	}
	var extra [1]byte
	if n, err := f.Read(extra[:]); n != 0 || err != io.EOF {
		return nil, fmt.Errorf("source grew while reading %s: %v", path, err)
	}
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !warmSameFile(before, after) {
		return nil, fmt.Errorf("source changed while reading: %s", path)
	}
	return content, nil
}

// scanProjectWarmCandidates discovers sources anew on every call, but reads
// their contents only for newly modified files or possible substring matches.
// A failed or interrupted scan is never published as a complete index.
// The callback performs the actual LSP synchronization, not this index.
func (e *Engine) scanProjectWarmCandidates(
	ctx context.Context, p language.Profile, root string, needle []byte,
	onMatch func(path, rel string, profile language.Profile, content []byte) error,
) (warmScanStats, error) {
	var stats warmScanStats
	key := root + "\x00" + p.SessionKey
	previous := e.warmIndex.snapshot(key)
	next := make(map[string]warmFileIndex, len(previous))
	cachedBytes := 0
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return core.WithCause(core.IncompleteSearch, "project warm-up could not inspect source files", walkErr)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && workspaceListExcludedDirs[entry.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(e.WS.Root(), path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		candidateProfile, err := language.FromPath(rel)
		if err != nil || candidateProfile.SessionKey != p.SessionKey {
			return nil
		}
		stats.SourceFiles++
		if stats.SourceFiles > projectWarmMaxFiles {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("project warm-up exceeded %d source files; narrow the workspace/profile", projectWarmMaxFiles))
		}
		info, err := entry.Info()
		if err != nil {
			return core.WithCause(core.IncompleteSearch, "project warm-up could not stat "+rel, err)
		}
		stats.SourceBytes += info.Size()
		if stats.SourceBytes > projectWarmMaxBytes {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("project warm-up exceeded %d MiB of source; narrow the workspace/profile", projectWarmMaxBytes>>20))
		}

		old, found := previous[path]
		unchanged := found && warmSameFile(old.info, info)
		current := old
		var content []byte
		if unchanged {
			if old.content != nil {
				// A newly inserted/changed earlier file may already have
				// consumed the remaining cache budget. Never blindly carry
				// forward old cached bytes: that grows the published
				// snapshot past the advertised 8 MiB bound.
				content = old.content
				stats.SkippedRead++
				if len(content) <= warmIndexMaxCachedBytes-cachedBytes {
					cachedBytes += len(content)
				} else {
					// Evict this source's byte snapshot while preserving
					// the negative-query filter. We have the bytes in
					// memory already, so this does not cause disk I/O.
					current = warmFileIndex{info: info}
					warmBuildIndex(&current, content)
				}
			} else if !warmIndexedMayContain(&old, needle) {
				next[path] = old
				stats.SkippedRead++
				return nil
			}
		}
		if content == nil {
			var err error
			content, err = readStableWarmSource(path, info)
			if err != nil {
				return core.WithCause(core.IncompleteSearch, "project warm-up could not read "+rel, err)
			}
			stats.FilesRead++
			stats.BytesRead += int64(len(content))
		}
		if !unchanged {
			current = warmFileIndex{info: info}
		}
		if current.content == nil && len(content) <= warmIndexMaxCachedBytes-cachedBytes {
			// Prefer the actual source bytes (up to a strict 8 MiB bound)
			// over calculating thousands of substring hashes during cold
			// startup. Only over-budget files need a Bloom filter.
			current.content = content
			cachedBytes += len(content)
		}
		if !unchanged && current.content == nil {
			warmBuildIndex(&current, content)
		}
		next[path] = current
		if !warmContainsProjectIdentifier(content, needle, p.SessionKey) {
			return nil
		}
		stats.Matches++
		if stats.Matches > projectWarmMaxMatches {
			return core.NewError(core.IncompleteSearch, fmt.Sprintf("project warm-up found more than %d candidate files; narrow the workspace/profile", projectWarmMaxMatches))
		}
		if err := onMatch(path, rel, candidateProfile, content); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return stats, err
	}
	e.warmIndex.publish(key, next)
	return stats, nil
}

