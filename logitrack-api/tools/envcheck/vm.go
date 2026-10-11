package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/tools/internal/dotenv"
)

// The staging VM kit (deploy/vm, issue OPSVM) runs api, worker, scheduler and web under pm2, migrate and seed
// from the deploy script, and cmd/release for the operator (T52). Its env.vm.example is the names-only template of
// the VM's .env:
//
//   - every key is a live §16.1 name consumed by one of those programs, or an infra name of the one
//     container that reads the .env (RabbitMQ); secret names are blank;
//   - every name those programs read is either a key or a per-process name listed on a
//     "# per-process (ecosystem.config.cjs):" comment line (listener addresses differ per pm2 app, so they
//     live in ecosystem.config.cjs and never in the shared .env);
//
// and env-names/<program>.txt, the allow-list run-app.sh exports to each program, holds exactly the
// §16.1 names of that program (the web: the web-server names), as compose does for its services.

// vmPrograms maps each program of the kit to the §16.1 consumer labels whose names it receives.
var vmPrograms = map[string][]string{
	"api":       {"api", "all-go"},
	"worker":    {"worker", "all-go"},
	"scheduler": {"scheduler", "all-go"},
	"migrate":   {"migrate", "all-go"},
	"seed":      {"seed", "all-go"},
	"web":       {"web-server"},
	"release":   {"release"},
}

// vmInfra are the infra names the VM's .env holds: provision.sh renders the RabbitMQ container's env-file
// from them. Redis takes its password from REDIS_URL; PostgreSQL is the server's own cluster.
var vmInfra = []string{"RABBITMQ_DEFAULT_USER", "RABBITMQ_DEFAULT_PASS"}

// perProcessRx matches the comment line that lists the names ecosystem.config.cjs sets per pm2 app.
var perProcessRx = regexp.MustCompile(`^#\s*per-process \(ecosystem\.config\.cjs\):(.*)$`)

// vmNamesRx is one name per line in env-names/<program>.txt.
var vmNamesRx = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)

// checkVMEnv applies the env.vm.example rules; it returns the per-process names it declares.
func checkVMEnv(path string, lines []dotenv.Line, inv inventory, banned map[string]bool, bannedPrefixes []string) ([]string, map[string]bool) {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	consumers := vmConsumers()
	perProcess := map[string]bool{}
	listed := map[string]bool{}
	for _, l := range lines {
		if l.Key == "" {
			if m := perProcessRx.FindStringSubmatch(strings.TrimSpace(l.Raw)); m != nil {
				for _, n := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ' ' || r == ',' || r == '\t' }) {
					perProcess[n] = true
				}
			}
			continue
		}
		if listed[l.Key] {
			add("%s: %s is listed twice", path, l.Key)
		}
		listed[l.Key] = true
		live, isLive := inv.live[l.Key]
		switch {
		case !isLive && inv.dropped[l.Key] != nil:
			add("%s: %s is a DROP row of §16.1", path, l.Key)
		case !isLive:
			add("%s: %s is not in §16.1", path, l.Key)
		case !hasAny(live, consumers) && !slices.Contains(vmInfra, l.Key):
			add("%s: %s belongs to no program of the VM kit (%v)", path, l.Key, keys(live.consumers))
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
			add("%s: %s is a secret and must be blank (generated on the VM)", path, l.Key)
		}
	}
	for n := range perProcess {
		e := inv.live[n]
		switch {
		case e == nil:
			add("%s: per-process name %s is not a live §16.1 name", path, n)
		case !hasAny(e, consumers):
			add("%s: per-process name %s belongs to no program of the VM kit", path, n)
		case e.secret == "yes":
			add("%s: per-process name %s is a secret; secrets live in the .env only", path, n)
		}
		if listed[n] {
			add("%s: %s is per-process (ecosystem.config.cjs) and must not be a key of the shared .env", path, n)
		}
	}
	for n, e := range inv.live {
		if (hasAny(e, consumers) || slices.Contains(vmInfra, n)) && !listed[n] && !perProcess[n] {
			add("%s: %s is missing (read by a program of the VM kit; a key, or per-process)", path, n)
		}
	}
	return problems, perProcess
}

// checkVMNames requires env-names/<program>.txt to hold exactly the §16.1 names of each program.
func checkVMNames(dir string, inv inventory) []string {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{err.Error()}
	}
	seen := map[string]bool{}
	for _, e := range entries {
		program, ok := strings.CutSuffix(e.Name(), ".txt")
		if e.IsDir() || !ok {
			add("%s: unexpected entry %s", dir, e.Name())
			continue
		}
		cons, known := vmPrograms[program]
		if !known {
			add("%s: %s is not a program of the VM kit", dir, e.Name())
			continue
		}
		seen[program] = true
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			add("%s", err)
			continue
		}
		path := filepath.Join(dir, e.Name())
		got := map[string]bool{}
		for i, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if !vmNamesRx.MatchString(line) {
				add("%s:%d: expected one variable name per line", path, i+1)
				continue
			}
			if got[line] {
				add("%s: %s is listed twice", path, line)
			}
			got[line] = true
		}
		want := map[string]bool{}
		for n, e := range inv.live {
			if hasAny(e, cons) {
				want[n] = true
			}
		}
		for n := range got {
			if !want[n] {
				add("%s: %s is not a §16.1 name of %s (%v)", path, n, program, cons)
			}
		}
		for n := range want {
			if !got[n] {
				add("%s: %s is missing (§16.1 consumer %v)", path, n, cons)
			}
		}
	}
	for p := range vmPrograms {
		if !seen[p] {
			add("%s: %s.txt is missing", dir, p)
		}
	}
	return problems
}

func vmConsumers() []string {
	var out []string
	for _, cons := range vmPrograms {
		for _, c := range cons {
			if !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
	}
	sort.Strings(out)
	return out
}
