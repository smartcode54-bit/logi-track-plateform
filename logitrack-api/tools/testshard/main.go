// Command testshard prints the packages of one shard of `make test-integration`, so go-ci's test
// job can run as parallel jobs (developer-spec.md §17.2): `make test-shard SHARD=n SHARDS=m` runs
// `go test` over the packages this command assigns to shard n of m.
//
// Packages are spread by how long their tests took in CI (weights.txt), heaviest first, each onto
// the shard with the least work so far, so the shards finish close together. A package missing
// from weights.txt (a new one) counts as defaultWeight. The split depends only on the package list
// and the weights, never on the order `go list` prints them in, so every shard computes the same
// split and each package lands in exactly one shard.
package main

import (
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

//go:embed weights.txt
var weightsFile string

// defaultWeight is the weight, in seconds, of a package weights.txt does not list.
const defaultWeight = 5.0

func main() {
	shard := flag.Int("shard", 0, "shard to print, 1-based")
	of := flag.Int("of", 0, "number of shards")
	tags := flag.String("tags", "integration", "build tags passed to go list")
	flag.Parse()
	if *of < 1 || *shard < 1 || *shard > *of {
		fail(fmt.Sprintf("need 1 <= -shard <= -of, got -shard %d -of %d", *shard, *of))
	}
	module, err := goList("-m")
	if err != nil {
		fail(err.Error())
	}
	pkgs, err := goList("-tags="+*tags, "./...")
	if err != nil {
		fail(err.Error())
	}
	weights, err := parseWeights(weightsFile)
	if err != nil {
		fail("weights.txt: " + err.Error())
	}
	shards, err := split(pkgs, weightOf(module[0], weights), *of)
	if err != nil {
		fail(err.Error())
	}
	for _, p := range shards[*shard-1] {
		fmt.Println(p)
	}
}

func goList(args ...string) ([]string, error) {
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list %s: %w", strings.Join(args, " "), err)
	}
	lines := strings.Fields(string(out))
	if len(lines) == 0 {
		return nil, fmt.Errorf("go list %s printed nothing", strings.Join(args, " "))
	}
	return lines, nil
}

// parseWeights reads "seconds path" lines; path is relative to the module root ("." for the root
// package). Blank lines and lines starting with # are skipped.
func parseWeights(s string) (map[string]float64, error) {
	w := map[string]float64{}
	for i, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 2 {
			return nil, fmt.Errorf("line %d: want \"seconds path\", got %q", i+1, line)
		}
		sec, err := strconv.ParseFloat(f[0], 64)
		if err != nil || sec < 0 {
			return nil, fmt.Errorf("line %d: bad seconds %q", i+1, f[0])
		}
		if _, dup := w[f[1]]; dup {
			return nil, fmt.Errorf("line %d: %s listed twice", i+1, f[1])
		}
		w[f[1]] = sec
	}
	return w, nil
}

// weightOf maps an import path to its weight, looking it up by its path relative to module.
func weightOf(module string, weights map[string]float64) func(string) float64 {
	return func(pkg string) float64 {
		rel := "."
		if pkg != module {
			rel = strings.TrimPrefix(pkg, module+"/")
		}
		if w, ok := weights[rel]; ok {
			return w
		}
		return defaultWeight
	}
}

// split assigns every package to one of n shards: heaviest first (ties by path), each to the
// least loaded shard (ties to the lowest index). Each shard is returned sorted by path.
func split(pkgs []string, weight func(string) float64, n int) ([][]string, error) {
	if n < 1 {
		return nil, errors.New("need at least one shard")
	}
	order := append([]string(nil), pkgs...)
	sort.Slice(order, func(i, j int) bool {
		wi, wj := weight(order[i]), weight(order[j])
		if wi != wj {
			return wi > wj
		}
		return order[i] < order[j]
	})
	shards := make([][]string, n)
	load := make([]float64, n)
	seen := map[string]bool{}
	for _, p := range order {
		if seen[p] {
			continue
		}
		seen[p] = true
		min := 0
		for i := 1; i < n; i++ {
			if load[i] < load[min] {
				min = i
			}
		}
		shards[min] = append(shards[min], p)
		load[min] += weight(p)
	}
	for i := range shards {
		if len(shards[i]) == 0 {
			return nil, fmt.Errorf("shard %d of %d is empty: more shards than packages (%d)", i+1, n, len(seen))
		}
		sort.Strings(shards[i])
	}
	return shards, nil
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "testshard:", msg)
	os.Exit(2)
}
