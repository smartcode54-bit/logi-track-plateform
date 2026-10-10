package authz

import (
	"encoding/json"
	"math/bits"
)

// capWords is the number of 64-bit words a CapSet needs for the catalog.
const capWords = 2

// CapSet is a set of catalog keys, one bit per catalog entry. The zero value is empty. Keys outside
// the catalog cannot be members: Add ignores them, so an unknown key in an override row or an API key
// never grants anything.
type CapSet struct {
	w [capWords]uint64
}

// NewCapSet returns the set of the given catalog keys.
func NewCapSet(keys ...Cap) CapSet {
	var s CapSet
	for _, k := range keys {
		s.Add(k)
	}
	return s
}

// Add puts a catalog key into the set and reports whether it is a catalog key.
func (s *CapSet) Add(k Cap) bool {
	i, ok := index[k]
	if ok {
		s.w[i/64] |= 1 << (i % 64)
	}
	return ok
}

// Remove takes a key out of the set.
func (s *CapSet) Remove(k Cap) {
	if i, ok := index[k]; ok {
		s.w[i/64] &^= 1 << (i % 64)
	}
}

// Has reports whether k is in the set.
func (s CapSet) Has(k Cap) bool {
	i, ok := index[k]
	return ok && s.w[i/64]&(1<<(i%64)) != 0
}

// Union returns s ∪ o.
func (s CapSet) Union(o CapSet) CapSet {
	for i := range s.w {
		s.w[i] |= o.w[i]
	}
	return s
}

// Minus returns s without the members of o.
func (s CapSet) Minus(o CapSet) CapSet {
	for i := range s.w {
		s.w[i] &^= o.w[i]
	}
	return s
}

// Len is the number of keys in the set.
func (s CapSet) Len() int {
	n := 0
	for _, w := range s.w {
		n += bits.OnesCount64(w)
	}
	return n
}

// Keys lists the members in catalog order.
func (s CapSet) Keys() []Cap {
	out := make([]Cap, 0, s.Len())
	for i, e := range catalog {
		if s.w[i/64]&(1<<(i%64)) != 0 {
			out = append(out, e.Key)
		}
	}
	return out
}

// Strings lists the members as strings in catalog order (GET /v1/me capabilities).
func (s CapSet) Strings() []string {
	keys := s.Keys()
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = string(k)
	}
	return out
}

// MarshalJSON encodes the set as a JSON array of keys in catalog order (the rbac:caps cache value).
func (s CapSet) MarshalJSON() ([]byte, error) { return json.Marshal(s.Strings()) }

// UnmarshalJSON decodes a JSON array of keys; keys the catalog no longer has are dropped.
func (s *CapSet) UnmarshalJSON(b []byte) error {
	var keys []Cap
	if err := json.Unmarshal(b, &keys); err != nil {
		return err
	}
	*s = NewCapSet(keys...)
	return nil
}
