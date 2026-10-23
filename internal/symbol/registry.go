package symbol

import (
	"container/list"
	"crypto/rand"
	"encoding/hex"
	"sync"

	"github.com/tamutamu/simple-lsp-mcp/internal/core"
)

// Record stores metadata needed to resolve a symbol in one MCP session.
type Record struct {
	ID, SessionKey, Name, Kind, ContainerName, URI, Path, FileHash string
	SymbolPath, Detail                                             string
	Range, SelectionRange                                          core.Range
	Data                                                           any
}

const maxRegistryEntries = 10000

type identity struct {
	session, path, hash, name, kind, symbolPath string
	selection                                   core.Range
}
type entry struct {
	record Record
	key    identity
}

// Registry is a bounded, process-local LRU of ephemeral symbol handles.
type Registry struct {
	mu         sync.Mutex
	records    map[string]*list.Element
	identities map[identity]string
	recent     *list.List
}

func New() *Registry {
	return &Registry{
		records:    make(map[string]*list.Element),
		identities: make(map[identity]string),
		recent:     list.New(),
	}
}

func (r *Registry) Register(v Record) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := identity{v.SessionKey, v.Path, v.FileHash, v.Name, v.Kind, v.SymbolPath, v.SelectionRange}
	if id, ok := r.identities[key]; ok {
		el := r.records[id]
		v.ID = id
		el.Value = entry{record: v, key: key}
		r.recent.MoveToFront(el)
		return id
	}
	for {
		var bytes [12]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			panic("secure symbol ID generation failed")
		}
		v.ID = "sym_" + hex.EncodeToString(bytes[:])
		if _, exists := r.records[v.ID]; exists {
			continue
		}
		el := r.recent.PushFront(entry{record: v, key: key})
		r.records[v.ID] = el
		r.identities[key] = v.ID
		break
	}
	if r.recent.Len() > maxRegistryEntries {
		oldest := r.recent.Back()
		previous := oldest.Value.(entry)
		delete(r.records, previous.record.ID)
		delete(r.identities, previous.key)
		r.recent.Remove(oldest)
	}
	return v.ID
}

func (r *Registry) Get(id string) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	el, ok := r.records[id]
	if !ok {
		return Record{}, core.NewError(core.SymbolNotFound, "symbol handle was not found")
	}
	r.recent.MoveToFront(el)
	return el.Value.(entry).record, nil
}
