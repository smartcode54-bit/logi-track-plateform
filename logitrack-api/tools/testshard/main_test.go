package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSplitIsAPartition(t *testing.T) {
	var pkgs []string
	for i := range 23 {
		pkgs = append(pkgs, fmt.Sprintf("m/p%02d", i))
	}
	weight := func(p string) float64 { return float64(len(p) + strings.Count(p, "1")*40) }
	for n := 1; n <= 6; n++ {
		shards, err := split(pkgs, weight, n)
		if err != nil {
			t.Fatalf("n=%d: %v", n, err)
		}
		var got []string
		for _, s := range shards {
			if !slices.IsSorted(s) {
				t.Errorf("n=%d: shard not sorted: %v", n, s)
			}
			got = append(got, s...)
		}
		slices.Sort(got)
		if !slices.Equal(got, pkgs) {
			t.Errorf("n=%d: shards hold %v, want every package once", n, got)
		}
	}
}

func TestSplitIgnoresInputOrderAndDuplicates(t *testing.T) {
	w := map[string]float64{"m/a": 9, "m/b": 7, "m/c": 7, "m/d": 1, "m/e": 3}
	weight := func(p string) float64 { return w[p] }
	a, err := split([]string{"m/a", "m/b", "m/c", "m/d", "m/e"}, weight, 2)
	if err != nil {
		t.Fatal(err)
	}
	b, err := split([]string{"m/e", "m/d", "m/c", "m/c", "m/b", "m/a"}, weight, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.EqualFunc(a, b, slices.Equal[[]string]) {
		t.Fatalf("split depends on input order: %v vs %v", a, b)
	}
	// Heaviest first onto the least loaded shard: a(9)->1, b(7)->2, c(7)->2, e(3)->1, d(1)->1.
	want := [][]string{{"m/a", "m/d", "m/e"}, {"m/b", "m/c"}}
	if !slices.EqualFunc(a, want, slices.Equal[[]string]) {
		t.Fatalf("split = %v, want %v", a, want)
	}
}

func TestSplitRefusesEmptyShards(t *testing.T) {
	if _, err := split([]string{"m/a", "m/b"}, func(string) float64 { return 1 }, 3); err == nil {
		t.Fatal("3 shards of 2 packages: want an error")
	}
	if _, err := split([]string{"m/a"}, func(string) float64 { return 1 }, 0); err == nil {
		t.Fatal("0 shards: want an error")
	}
}

func TestWeightOf(t *testing.T) {
	w := weightOf("example.com/mod", map[string]float64{".": 2, "internal/auth": 300})
	for pkg, want := range map[string]float64{
		"example.com/mod":               2,
		"example.com/mod/internal/auth": 300,
		"example.com/mod/internal/new":  defaultWeight,
	} {
		if got := w(pkg); got != want {
			t.Errorf("weight(%s) = %v, want %v", pkg, got, want)
		}
	}
}

func TestParseWeightsRejectsBadLines(t *testing.T) {
	for _, s := range []string{"12 a b", "x internal/a", "-1 internal/a", "1 a\n2 a"} {
		if _, err := parseWeights(s); err == nil {
			t.Errorf("parseWeights(%q): want an error", s)
		}
	}
	w, err := parseWeights("# comment\n\n 3.5 internal/a \n1 .\n")
	if err != nil || w["internal/a"] != 3.5 || w["."] != 1 || len(w) != 2 {
		t.Fatalf("parseWeights = %v, %v", w, err)
	}
}

// Every package weights.txt lists still exists, so a rename or a removal is noticed (its tests
// would otherwise fall back to the default weight and unbalance the shards).
func TestWeightsNameExistingPackages(t *testing.T) {
	w, err := parseWeights(weightsFile)
	if err != nil {
		t.Fatal(err)
	}
	// The test runs in tools/testshard; list the whole module from its root, as `make test-shard` does.
	gomod, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	root := filepath.Dir(strings.TrimSpace(string(gomod)))
	list := exec.Command("go", "list", "-tags=integration", "./...")
	list.Dir = root
	out, err := list.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	modCmd := exec.Command("go", "list", "-m")
	modCmd.Dir = root
	mod, err := modCmd.Output()
	if err != nil {
		t.Fatalf("go list -m: %v", err)
	}
	module := strings.TrimSpace(string(mod))
	have := map[string]bool{}
	for _, p := range strings.Fields(string(out)) {
		if p == module {
			have["."] = true
		} else {
			have[strings.TrimPrefix(p, module+"/")] = true
		}
	}
	for rel := range w {
		if !have[rel] {
			t.Errorf("weights.txt lists %s, which is no package of %s", rel, module)
		}
	}
}
