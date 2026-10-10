// Command envcheck verifies .env.example and the compose environment against
// developer-spec.md §16 (the canonical env inventory), issues T02 and TW2:
//
//   - .env.example holds exactly the §16.1 names consumed by the Go binaries,
//     infra and caddy, plus the web-server and web-public names compose passes
//     to the `web` container (a web-public name may be a DROP row that lives
//     until P6, the Firebase web config); secret names are blank; no "spelling
//     not adopted" (§16.4) and no container-only name appears;
//   - every ${NAME} interpolated in the compose file is listed in .env.example,
//     so `make env` yields a complete .env;
//   - each Go service in compose receives exactly the names whose consumer
//     column lists it (so api, worker and scheduler get only DATABASE_URL and
//     seed gets all three DB URLs, R66/R87);
//   - `web` receives exactly the web-server names and is built with web-public
//     names only; `caddy` receives exactly the caddy names (TW2).
//
// Usage: docker compose ... --profile '*' config --format json | envcheck -spec ../developer-spec.md -env .env.example -compose - -compose-file deploy/docker-compose.yml
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

// localConsumers are the §16.1 consumer labels whose names must all be in
// logitrack-api/.env.example (mobile, ci and functions names live elsewhere).
var localConsumers = []string{"all-go", "api", "worker", "scheduler", "migrate", "seed", "etl", "release", "infra", "infra (dev tunnel)", "caddy"}

// webConsumers may appear in .env.example because compose interpolates them for
// the `web` service; they are required only when the compose file uses them.
var webConsumers = []string{"web-server", "web-public"}

type entry struct {
	secret    string
	consumers map[string]bool
}

// inventory is §16.1 split by status: live rows and DROP rows.
type inventory struct {
	live    map[string]*entry
	dropped map[string]*entry
}

var (
	nameRx  = regexp.MustCompile("`([A-Z][A-Z0-9_]*)(\\*?)`")
	parenRx = regexp.MustCompile(`\([^()]*\)`)
	// ${NAME...} not preceded by "$" ("$${...}" is compose's escape for a literal "${").
	interpRx = regexp.MustCompile(`(^|[^$])\$\{([A-Z_][A-Z0-9_]*)`)
)

func main() {
	specPath := flag.String("spec", "../developer-spec.md", "main spec")
	envPath := flag.String("env", ".env.example", "env template")
	composePath := flag.String("compose", "", "compose config JSON (\"-\" for stdin); empty skips the compose checks")
	composeFile := flag.String("compose-file", "", "raw compose file whose ${NAME} interpolations must be in -env; empty skips")
	flag.Parse()

	spec, err := os.ReadFile(*specPath)
	must(err)
	inv, banned, bannedPrefixes := parseSpec(string(spec))
	lines, err := dotenv.Read(*envPath)
	must(err)

	problems, seen := checkEnvExample(*envPath, lines, inv, banned, bannedPrefixes)
	if *composeFile != "" {
		raw, err := os.ReadFile(*composeFile)
		must(err)
		problems = append(problems, checkInterpolation(*composeFile, string(raw), seen)...)
	}
	if *composePath != "" {
		var r io.Reader = os.Stdin
		if *composePath != "-" {
			f, err := os.Open(*composePath)
			must(err)
			defer func() { _ = f.Close() }()
			r = f
		}
		var cfg composeConfig
		must(json.NewDecoder(r).Decode(&cfg))
		problems = append(problems, checkCompose(cfg, inv)...)
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
	if *composeFile != "" {
		fmt.Printf(", interpolations of %s", *composeFile)
	}
	if *composePath != "" {
		fmt.Print(", compose environments of ", strings.Join(append(slices.Clone(goServices), "web", "caddy"), ", "))
	}
	fmt.Println(")")
}

// checkEnvExample applies the .env.example rules and returns the names it lists.
func checkEnvExample(path string, lines []dotenv.Line, inv inventory, banned map[string]bool, bannedPrefixes []string) ([]string, map[string]bool) {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	required := map[string]bool{}
	for n, e := range inv.live {
		if hasAny(e, localConsumers) {
			required[n] = true
		}
	}

	seen := map[string]bool{}
	for _, l := range lines {
		if l.Key == "" {
			continue
		}
		if seen[l.Key] {
			add("%s: %s is listed twice", path, l.Key)
		}
		seen[l.Key] = true
		live, isLive := inv.live[l.Key]
		drop, isDrop := inv.dropped[l.Key]
		switch {
		case isLive && !required[l.Key] && !hasAny(live, webConsumers):
			add("%s: %s belongs to another consumer (%v), not to logitrack-api", path, l.Key, keys(live.consumers))
		case !isLive && isDrop && !drop.consumers["web-public"]:
			add("%s: %s is a DROP row of §16.1 (%v)", path, l.Key, keys(drop.consumers))
		case !isLive && !isDrop:
			add("%s: %s is not in §16.1 (container-only or unknown name)", path, l.Key)
		}
		if banned[l.Key] {
			add("%s: %s is a spelling not adopted (§16.4)", path, l.Key)
		}
		for _, p := range bannedPrefixes {
			if strings.HasPrefix(l.Key, p) {
				add("%s: %s uses the dropped prefix %s* (§16.4)", path, l.Key, p)
			}
		}
		if isLive && live.secret == "yes" && strings.TrimSpace(l.Value) != "" {
			add("%s: %s is a secret and must be blank", path, l.Key)
		}
		// Caddy dials "api" + API_PUBLIC_ADDR (deploy/Caddyfile, §16.1); the api enforces the same
		// rule for APP_ENV dev and prod. The value is not echoed.
		if l.Key == "API_PUBLIC_ADDR" && !strings.HasPrefix(strings.TrimSpace(l.Value), ":") {
			add("%s: API_PUBLIC_ADDR must be written :port (Caddy dials api + this value)", path)
		}
	}
	for n := range required {
		if !seen[n] {
			add("%s: %s is missing (§16.1 lists a logitrack-api consumer)", path, n)
		}
	}
	return problems, seen
}

// checkInterpolation requires every ${NAME} of the compose file to be in .env.example.
func checkInterpolation(path, raw string, seen map[string]bool) []string {
	var problems []string
	done := map[string]bool{}
	for _, m := range interpRx.FindAllStringSubmatch(raw, -1) {
		if n := m[2]; !seen[n] && !done[n] {
			done[n] = true
			problems = append(problems, fmt.Sprintf("%s: ${%s} is interpolated but not listed in .env.example", path, n))
		}
	}
	return problems
}

type composeConfig struct {
	Services map[string]struct {
		Environment map[string]*string `json:"environment"`
		Build       *struct {
			Args map[string]*string `json:"args"`
		} `json:"build"`
	} `json:"services"`
}

// checkCompose verifies what each service receives.
func checkCompose(cfg composeConfig, inv inventory) []string {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	exact := func(svc string, got map[string]*string, want map[string]bool, what string) {
		for n := range got {
			if !want[n] {
				add("compose: %s receives %s, which §16.1 does not give it", svc, n)
			}
		}
		for n := range want {
			if _, ok := got[n]; !ok {
				add("compose: %s does not receive %s (%s)", svc, n, what)
			}
		}
	}
	liveWith := func(consumers ...string) map[string]bool {
		out := map[string]bool{}
		for n, e := range inv.live {
			if hasAny(e, consumers) {
				out[n] = true
			}
		}
		return out
	}

	for _, svc := range goServices {
		s, ok := cfg.Services[svc]
		if !ok {
			add("compose: service %s missing (run config with --profile '*')", svc)
			continue
		}
		exact(svc, s.Environment, liveWith(svc, "all-go"), "§16.1 consumer "+svc)
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

	// TW2: the web container and the edge proxy.
	if web, ok := cfg.Services["web"]; !ok {
		add("compose: service web missing (run config with --profile '*')")
	} else {
		exact("web", web.Environment, liveWith("web-server"), "§16.1 consumer web-server")
		var args map[string]*string
		if web.Build != nil {
			args = web.Build.Args
		}
		for n := range args {
			e := inv.live[n]
			if e == nil {
				e = inv.dropped[n]
			}
			if e == nil || !e.consumers["web-public"] {
				add("compose: web build arg %s is not a §16.1 web-public name (only NEXT_PUBLIC_* values may be inlined)", n)
			}
		}
		for n := range liveWith("web-public") {
			if _, ok := args[n]; !ok {
				add("compose: web build does not receive %s (§16.1 web-public)", n)
			}
		}
	}
	if caddy, ok := cfg.Services["caddy"]; !ok {
		add("compose: service caddy missing (run config with --profile '*')")
	} else {
		exact("caddy", caddy.Environment, liveWith("caddy"), "§16.1 consumer caddy")
	}
	return problems
}

// parseSpec reads §16.1 (inventory) and the "Spellings not adopted" list of §16.4.
func parseSpec(s string) (inv inventory, banned map[string]bool, bannedPrefixes []string) {
	sec := between(s, "### 16.1", "### 16.2")
	inv = inventory{live: map[string]*entry{}, dropped: map[string]*entry{}}
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
		target := inv.live
		if status == "DROP" {
			target = inv.dropped
		}
		for _, m := range nameRx.FindAllStringSubmatch(cells[0], -1) {
			e, ok := target[m[1]]
			if !ok {
				e = &entry{secret: secret, consumers: map[string]bool{}}
				target[m[1]] = e
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
		case inv.live[m[1]] != nil:
			// also an adopted §16.1 name; the inventory wins
		default:
			banned[m[1]] = true
		}
	}
	return inv, banned, bannedPrefixes
}

func hasAny(e *entry, consumers []string) bool {
	for _, c := range consumers {
		if e.consumers[c] {
			return true
		}
	}
	return false
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
