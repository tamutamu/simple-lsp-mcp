package symbol

import (
	"crypto/rand"
	"encoding/hex"
	"github.com/tamutamu/simple-lsp-mcp/internal/core"
	"sync"
)

type Record struct {
	ID, SessionKey, Name, Kind, ContainerName, URI, Path, FileHash string
	Range, SelectionRange                                          core.Range
	Data                                                           any
}
type Registry struct {
	mu      sync.RWMutex
	records map[string]Record
}

func New() *Registry { return &Registry{records: map[string]Record{}} }
func (r *Registry) Register(v Record) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	for {
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		v.ID = "sym_" + hex.EncodeToString(b)
		if _, ok := r.records[v.ID]; !ok {
			r.records[v.ID] = v
			return v.ID
		}
	}
}
func (r *Registry) Get(id string) (Record, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	v, ok := r.records[id]
	if !ok {
		return Record{}, core.NewError(core.SymbolNotFound, "symbol handle was not found")
	}
	return v, nil
}
