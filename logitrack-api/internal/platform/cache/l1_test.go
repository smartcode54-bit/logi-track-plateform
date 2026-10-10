package cache

import (
	"testing"
	"time"
)

func TestL1DropsByKeyAndByDependency(t *testing.T) {
	s := newL1(10, time.Minute)
	s.put(s.mark(), "a", 1)
	s.put(s.mark(), "maps", "m", "n2c", "c2n")
	if v, ok := s.get("maps"); !ok || v != "m" {
		t.Fatalf("get maps = %v %v", v, ok)
	}
	s.drop("c2n")
	if _, ok := s.get("maps"); ok {
		t.Fatal("dropping a dependency kept the entry")
	}
	if _, ok := s.get("a"); !ok {
		t.Fatal("an unrelated entry was dropped")
	}
	s.drop("a")
	if s.len() != 0 || len(s.byDep) != 0 {
		t.Fatalf("left %d entries, %d dependency sets", s.len(), len(s.byDep))
	}
}

func TestL1ExpiresAndBounds(t *testing.T) {
	now := time.Unix(0, 0)
	s := newL1(2, time.Second)
	s.now = func() time.Time { return now }
	s.put(s.mark(), "a", 1)
	now = now.Add(2 * time.Second)
	if _, ok := s.get("a"); ok {
		t.Fatal("expired entry served")
	}
	s.put(s.mark(), "b", 2)
	s.put(s.mark(), "c", 3)
	s.put(s.mark(), "d", 4)
	if s.len() != 2 {
		t.Fatalf("len = %d, want the bound 2", s.len())
	}
	if _, ok := s.get("d"); !ok {
		t.Fatal("the newest entry was evicted")
	}
	s.flush()
	if s.len() != 0 {
		t.Fatal("flush kept entries")
	}
}

func TestNilL1IsOff(t *testing.T) {
	var s *l1
	s.put(s.mark(), "a", 1)
	s.drop("a")
	s.flush()
	if _, ok := s.get("a"); ok || s.len() != 0 {
		t.Fatal("nil l1 stored a value")
	}
}

func TestL1NeverStoresAValueReadBeforeADrop(t *testing.T) {
	s := newL1(10, time.Minute)
	mark := s.mark() // a reader starts reading Redis
	s.drop("other")  // an invalidation lands meanwhile (any key: the epoch is global)
	s.put(mark, "a", "old")
	if _, ok := s.get("a"); ok {
		t.Fatal("a value read before a drop was stored after it")
	}
	mark = s.mark()
	s.flush()
	s.put(mark, "a", "old")
	if _, ok := s.get("a"); ok {
		t.Fatal("a value read before a flush was stored after it")
	}
	s.put(s.mark(), "a", "new")
	if v, ok := s.get("a"); !ok || v != "new" {
		t.Fatalf("get = %v %v", v, ok)
	}
}
