package tools

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync"

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
	info    fs.FileInfo
	bloom   [warmBloomBytes]byte
	content []byte // Source bytes cached opportunistically, bounded per project.
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
				// The small-source fast path avoids constructing a Bloom
				// filter on the first scan, and avoids both disk reads and
				// false-positive Bloom checks on subsequent scans.
				cachedBytes += len(old.content)
				content = old.content
				stats.SkippedRead++
			} else if !warmBloomMayContain(&old.bloom, needle) {
				next[path] = old
				stats.SkippedRead++
				return nil
			}
		}
		if content == nil {
			var err error
			content, err = os.ReadFile(path)
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
			warmBloomAdd(&current.bloom, content)
		}
		next[path] = current
		if !bytes.Contains(content, needle) {
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

