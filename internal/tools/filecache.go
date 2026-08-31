package tools

import (
	"os"
	"sync"

	"github.com/tamutamu/simple-lsp-mcp/internal/workspace"
)

// fileCacheMaxBytes bounds how much file content one fileCache holds onto.
// Past this, reads still work; they just stop being memoized.
const fileCacheMaxBytes = 32 << 20

// fileCache memoizes file contents and hashes for the lifetime of a single
// tool request. It is never stored on Engine and never shared between
// requests: Engine is shared across every concurrent request, and a
// process-lifetime cache here would reintroduce the staleness and locking
// concerns the rest of this package's read-only design avoids.
type fileCache struct {
	mu    sync.Mutex
	bytes map[string][]byte
	hash  map[string]string
	total int
}

func newFileCache() *fileCache {
	return &fileCache{bytes: map[string][]byte{}, hash: map[string]string{}}
}

// read returns the contents of a workspace-relative path, reading it from
// disk at most once. A nil cache always reads from disk.
func (c *fileCache) read(ws *workspace.Workspace, path string) ([]byte, error) {
	if c == nil {
		return readWorkspaceFile(ws, path)
	}
	c.mu.Lock()
	b, ok := c.bytes[path]
	c.mu.Unlock()
	if ok {
		return b, nil
	}
	b, err := readWorkspaceFile(ws, path)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.total+len(b) <= fileCacheMaxBytes {
		c.bytes[path] = b
		c.total += len(b)
	}
	c.mu.Unlock()
	return b, nil
}

// hashOf returns the sha256 of a workspace-relative path, hashing it at
// most once. A nil cache always reads and hashes from disk.
func (c *fileCache) hashOf(ws *workspace.Workspace, path string) string {
	if c == nil {
		b, err := readWorkspaceFile(ws, path)
		if err != nil {
			return ""
		}
		return hash(b)
	}
	c.mu.Lock()
	h, ok := c.hash[path]
	c.mu.Unlock()
	if ok {
		return h
	}
	b, err := c.read(ws, path)
	if err != nil {
		return ""
	}
	h = hash(b)
	c.mu.Lock()
	c.hash[path] = h
	c.mu.Unlock()
	return h
}

func readWorkspaceFile(ws *workspace.Workspace, path string) ([]byte, error) {
	full, err := ws.Resolve(path)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(full)
}
