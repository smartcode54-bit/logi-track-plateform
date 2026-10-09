// Command envcheck verifies .env.example and the compose environment against
// developer-spec.md §16 (the canonical env inventory), issue T02:
//
//   - .env.example holds exactly the §16.1 names consumed by the Go binaries,
//     infra and caddy; secret names are blank; no "spelling not adopted" (§16.4)
//     and no container-only name appears;
//   - each Go service in compose receives exactly the names whose consumer
//     column lists it (so api, worker and scheduler get only DATABASE_URL and
//     seed gets all three DB URLs, R66/R87).
//
// Usage: docker compose ... --profile '*' config --format json | envcheck -spec ../developer-spec.md -env .env.example -compose -
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/tools/internal/dotenv"
)

// goServices are the compose services built from the logitrack-api image.
var goServices = []string{"api", "worker", "scheduler", "migrate", "seed", "etl"}

// localConsumers are the §16.1 consumer labels whose names belong in
// logitrack-api/.env.example (web, mobile, ci and functions names live elsewhere).
var localConsumers = []string{"all-go", "api", "worker", "scheduler", "migrate", "seed", "etl", "release", "infra", "infra (dev tunnel)", "caddy"}

type entry struct {
	secret    string
	consumers map[string]bool
}

var (
	nameRx  = regexp.MustCompile("`([A-Z][A-Z0-9_]*)(\\*?)`")
	parenRx = regexp.MustCompile(`\([^()]*\)`)
)

func main() {
	specPath := flag.String("spec", "../developer-spec.md", "main spec")
	envPath := flag.String("env", ".env.example", "env template")
	composePath := flag.String("compose", "", "compose config JSON (\"-\" for stdin); empty skips the compose checks")
	flag.Parse()

	spec, err := os.ReadFile(*specPath)
	must(err)
	inv, banned, bannedPrefixes := parseSpec(string(spec))

	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	expected := map[string]bool{}
	for n, e := range inv {
		for _, c := range localConsumers {
			if e.consumers[c] {
				expected[n] = true
			}
		}
	}

	lines, err := dotenv.Read(*envPath)
	must(err)
	seen := map[string]bool{}
	for _, l := range lines {
		if l.Key == "" {
			continue
		}
		if seen[l.Key] {
			add("%s: %s is listed twice", *envPath, l.Key)
		}
		seen[l.Key] = true
		e, known := inv[l.Key]
		switch {
		case !known:
			add("%s: %s is not in §16.1 (container-only or unknown name)", *envPath, l.Key)
		case !expected[l.Key]:
			add("%s: %s belongs to another consumer (%v), not to logitrack-api", *envPath, l.Key, keys(e.consumers))
		}
		if banned[l.Key] {
			add("%s: %s is a spelling not adopted (§16.4)", *envPath, l.Key)
		}
		for _, p := range bannedPrefixes {
			if strings.HasPrefix(l.Key, p) {
				add("%s: %s uses the dropped prefix %s* (§16.4)", *envPath, l.Key, p)
			}
		}
		if known && e.secret == "yes" && strings.TrimSpace(l.Value) != "" {
			add("%s: %s is a secret and must be blank", *envPath, l.Key)
		}
	}
	for n := range expected {
		if !seen[n] {
			add("%s: %s is missing (§16.1 lists a logitrack-api consumer)", *envPath, n)
		}
	}

	if *composePath != "" {
		var r io.Reader = os.Stdin
		if *composePath != "-" {
			f, err := os.Open(*composePath)
			must(err)
			defer func() { _ = f.Close() }()
			r = f
		}
		var cfg struct {
			Services map[string]struct {
				Environment map[string]*string `json:"environment"`
			} `json:"services"`
		}
		must(json.NewDecoder(r).Decode(&cfg))
		for _, svc := range goServices {
			s, ok := cfg.Services[svc]
			if !ok {
				add("compose: service %s missing (run config with --profile '*')", svc)
				continue
			}
			want := map[string]bool{}
			for n, e := range inv {
				if e.consumers[svc] || e.consumers["all-go"] {
					want[n] = true
				}
			}
			for n := range s.Environment {
				if !want[n] {
					add("compose: %s receives %s, which §16.1 does not give it", svc, n)
				}
			}
			for n := range want {
				if _, ok := s.Environment[n]; !ok {
					add("compose: %s does not receive %s", svc, n)
				}
			}
		}
		// The explicit R66/R87 rule, reported on its own for clarity.
		dbRule := map[string][]string{
			"api": {"DATABASE_URL"}, "worker": {"DATABASE_URL"}, "scheduler": {"DATABASE_URL"},
			"migrate": {"MIGRATE_DATABASE_URL"}, "etl": {"ETL_DATABASE_URL"},
			"seed": {"DATABASE_URL", "ETL_DATABASE_URL", "MIGRATE_DATABASE_URL"},
		}
		for svc, want := range dbRule {
			var got []string
			for n := range cfg.Services[svc].Environment {
				if strings.HasSuffix(n, "DATABASE_URL") {
					got = append(got, n)
				}
			}
			sort.Strings(got)
			if !slices.Equal(got, want) {
				add("compose: %s DB URLs = %v, want %v (R66, R87)", svc, got, want)
			}
		}
	}

	sort.Strings(problems)
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintln(os.Stderr, "envcheck:", p)
		}
		fmt.Fprintf(os.Stderr, "envcheck: %d problem(s)\n", len(problems))
		os.Exit(1)
	}
	fmt.Printf("envcheck: ok (%d names in %s", len(seen), *envPath)
	if *composePath != "" {
		fmt.Print(", compose environments of ", strings.Join(goServices, ", "))
	}
	fmt.Println(")")
}

// parseSpec reads §16.1 (inventory) and the "Spellings not adopted" list of §16.4.
func parseSpec(s string) (inv map[string]*entry, banned map[string]bool, bannedPrefixes []string) {
	sec := between(s, "### 16.1", "### 16.2")
	inv = map[string]*entry{}
	for _, line := range strings.Split(sec, "\n") {
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		if len(cells) < 5 {
			continue
		}
		status := strings.TrimSpace(cells[4])
		secret := strings.Fields(strings.TrimSpace(cells[1]))[0]
		var cons []string
		for _, c := range strings.Split(cells[2], ",") {
			cons = append(cons, strings.TrimSpace(c))
		}
		for _, m := range nameRx.FindAllStringSubmatch(cells[0], -1) {
			if status == "DROP" {
				continue
			}
			e, ok := inv[m[1]]
			if !ok {
				e = &entry{secret: secret, consumers: map[string]bool{}}
				inv[m[1]] = e
			}
			if secret == "yes" {
				e.secret = "yes"
			}
			for _, c := range cons {
				e.consumers[c] = true
			}
		}
	}
	// Parentheticals explain a ban by pointing at the adopted name, e.g.
	// "COMPAT_FIRESTORE_WRITEBACK_ENABLED (implied by PG_OWNED_DOMAINS)".
	drop := parenRx.ReplaceAllString(between(s, "**Spellings not adopted**", "### 16.5"), "")
	banned = map[string]bool{}
	for _, m := range nameRx.FindAllStringSubmatch(drop, -1) {
		switch {
		case m[2] == "*":
			bannedPrefixes = append(bannedPrefixes, m[1])
		case strings.HasSuffix(m[1], "_"):
			// a prefix mentioned in prose without "*" (e.g. "no ETL_-prefixed own-fleet id")
		case inv[m[1]] != nil:
			// also an adopted §16.1 name; the inventory wins
		default:
			banned[m[1]] = true
		}
	}
	return inv, banned, bannedPrefixes
}

func between(s, from, to string) string {
	i := strings.Index(s, from)
	if i < 0 {
		must(fmt.Errorf("spec: %q not found", from))
	}
	j := strings.Index(s[i:], to)
	if j < 0 {
		must(fmt.Errorf("spec: %q not found after %q", to, from))
	}
	return s[i : i+j]
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "envcheck:", err)
		os.Exit(2)
	}
}
