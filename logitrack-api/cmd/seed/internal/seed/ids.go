// Package seed builds and loads the deterministic seed profiles of developer-spec.md §14 and Appendix D:
// uuid v5 ids from a namespace, the smoke fixture of §D.4, the generated demo and load additions,
// placeholder media, the load through ETL_DATABASE_URL inside db.WithSystem (R66, R87), --reset and the
// twelve --verify invariants of §D.3. It lives under cmd/seed, one of the packages the WithSystem analyzer
// allows (Appendix C §C.3.2).
package seed

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// DefaultNamespace is uuid v5(NAMESPACE_URL, "https://logitrack.test/seed/v1"), the namespace of every
// seeded id when SEED_NAMESPACE is empty (Appendix D §D.1.3).
var DefaultNamespace = uuid.MustParse("dd659aa7-e92b-55af-b6f0-51075452cf69")

// QuarantineTenantID is the fixed id of the quarantine tenant that migration 0002 inserts (R56). The seed
// never inserts that row.
var QuarantineTenantID = uuid.MustParse("00000000-0000-7000-8000-00000000000f")

// quarantineSymbol and ownFleetSymbol are the two tenant symbols with special ids.
const (
	quarantineSymbol = "TN_QUAR"
	ownFleetSymbol   = "TN_OWN"
)

// ID is uuid v5(ns, "<table>:<natural key>"), the id of every seeded row (Appendix D §D.1.3).
func ID(ns uuid.UUID, table, key string) uuid.UUID {
	return uuid.NewSHA1(ns, []byte(table+":"+key))
}

// ClientOpID is the client_op_id / client_message_id of a native driver-created fixture row: uuid v5 of
// "client_op:<symbol>" (Appendix D §D.4.1, R63).
func ClientOpID(ns uuid.UUID, symbol string) uuid.UUID {
	return uuid.NewSHA1(ns, []byte("client_op:"+symbol))
}

// RegistryEntry is one row of the §D.4.2 id registry (cmd/seed/testdata/registry.json). UUID is the
// documented value for DefaultNamespace: the review copy, checked by the tests and never read by the loader.
type RegistryEntry struct {
	Symbol string `json:"symbol"`
	Table  string `json:"table"`
	Key    string `json:"key"`
	UUID   string `json:"uuid"`
}

// ReadRegistry reads the registry file.
func ReadRegistry(fsys fs.FS, path string) ([]RegistryEntry, error) {
	b, err := fs.ReadFile(fsys, path)
	if err != nil {
		return nil, fmt.Errorf("seed: registry: %w", err)
	}
	var out []RegistryEntry
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("seed: registry %s: %w", path, err)
	}
	seen := map[string]bool{}
	for _, e := range out {
		if !symbolRx.MatchString(e.Symbol) || e.Table == "" || e.Key == "" {
			return nil, fmt.Errorf("seed: registry %s: incomplete entry %q", path, e.Symbol)
		}
		if seen[e.Symbol] {
			return nil, fmt.Errorf("seed: registry %s: symbol %s listed twice", path, e.Symbol)
		}
		seen[e.Symbol] = true
	}
	return out, nil
}

// symbolRx is a registry symbol; refRx is a reference @SYM inside a fixture string or a natural key.
var (
	symbolRx = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	refRx    = regexp.MustCompile(`@([A-Z][A-Z0-9_]*)`)
)

// Symbols maps registry symbols to their ids in one namespace.
type Symbols struct {
	ids  map[string]uuid.UUID
	syms map[uuid.UUID]string // reverse of ids
	keys map[string]string    // natural key with references resolved
}

// ResolveSymbols computes every symbol's id in ns. A natural key that names another symbol (object keys
// carry entity ids) is hashed once that symbol is known. ownFleet, when set (OWN_FLEET_TENANT_ID, R7),
// replaces the id of TN_OWN and nothing else; TN_QUAR keeps the fixed id of migration 0002.
func ResolveSymbols(entries []RegistryEntry, ns uuid.UUID, ownFleet *uuid.UUID) (Symbols, error) {
	s := Symbols{ids: map[string]uuid.UUID{}, keys: map[string]string{}}
	known := map[string]bool{}
	for _, e := range entries {
		known[e.Symbol] = true
	}
	pending := append([]RegistryEntry(nil), entries...)
	for len(pending) > 0 {
		var next []RegistryEntry
		for _, e := range pending {
			switch e.Symbol {
			case quarantineSymbol:
				s.ids[e.Symbol] = QuarantineTenantID
				s.keys[e.Symbol] = e.Key
				continue
			case ownFleetSymbol:
				if ownFleet != nil {
					s.ids[e.Symbol] = *ownFleet
					s.keys[e.Symbol] = e.Key
					continue
				}
			}
			ready := true
			for _, m := range refRx.FindAllStringSubmatch(e.Key, -1) {
				if !known[m[1]] {
					return Symbols{}, fmt.Errorf("seed: registry: the key of %s names the unknown symbol @%s", e.Symbol, m[1])
				}
				if _, ok := s.ids[m[1]]; !ok {
					ready = false
				}
			}
			if !ready {
				next = append(next, e)
				continue
			}
			key := s.replace(e.Key)
			s.keys[e.Symbol] = key
			s.ids[e.Symbol] = ID(ns, e.Table, key)
		}
		if len(next) == len(pending) {
			return Symbols{}, fmt.Errorf("seed: registry: natural keys refer to each other in a cycle (%s)", next[0].Symbol)
		}
		pending = next
	}
	s.syms = make(map[uuid.UUID]string, len(s.ids))
	for sym, id := range s.ids {
		s.syms[id] = sym
	}
	return s, nil
}

// replace substitutes every @SYM whose id is known; callers check unknown references first.
func (s Symbols) replace(v string) string {
	return refRx.ReplaceAllStringFunc(v, func(m string) string {
		if id, ok := s.ids[m[1:]]; ok {
			return id.String()
		}
		return m
	})
}

// ID returns the id of sym.
func (s Symbols) ID(sym string) (uuid.UUID, bool) {
	id, ok := s.ids[sym]
	return id, ok
}

// MustID returns the id of a symbol the code names literally; an unknown one is a programming error.
func (s Symbols) MustID(sym string) uuid.UUID {
	id, ok := s.ids[sym]
	if !ok {
		panic("seed: unknown symbol " + sym)
	}
	return id
}

// Key is the natural key of sym with its references resolved.
func (s Symbols) Key(sym string) string { return s.keys[sym] }

// Resolve replaces every @SYM in v; an unknown symbol is an error (a typo in a fixture).
func (s Symbols) Resolve(v string) (string, error) {
	if !strings.Contains(v, "@") {
		return v, nil
	}
	for _, m := range refRx.FindAllStringSubmatch(v, -1) {
		if _, ok := s.ids[m[1]]; !ok {
			return "", fmt.Errorf("unknown symbol @%s", m[1])
		}
	}
	return s.replace(v), nil
}

// SymbolOf returns the symbol whose id is id ("" when none).
func (s Symbols) SymbolOf(id uuid.UUID) string { return s.syms[id] }
