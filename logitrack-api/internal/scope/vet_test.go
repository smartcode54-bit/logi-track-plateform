package scope

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// sqlBlock is one entry of the sql list of an sqlc.yaml.
type sqlBlock struct {
	Name    string   `yaml:"name"`
	Queries string   `yaml:"queries"`
	Rules   []string `yaml:"rules"`
}

// sqlcConfig is the part of an sqlc.yaml these tests read.
type sqlcConfig struct {
	SQL   []sqlBlock `yaml:"sql"`
	Rules []struct {
		Name    string `yaml:"name"`
		Message string `yaml:"message"`
		Rule    string `yaml:"rule"`
	} `yaml:"rules"`
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for ; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if dir == filepath.Dir(dir) {
			t.Fatal("go.mod not found above the test directory")
		}
	}
}

func readConfig(t *testing.T, path string) sqlcConfig {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var c sqlcConfig
	if err := yaml.Unmarshal(b, &c); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return c
}

// rawPatterns are the r'...' regular expressions of a CEL rule. CEL's matches() is RE2, the syntax of
// Go's regexp, so the patterns can be checked here without sqlc.
var rawPattern = regexp.MustCompile(`r'([^']*)'`)

func scopeRule(t *testing.T) (rule string, patterns []*regexp.Regexp) {
	t.Helper()
	cfg := readConfig(t, filepath.Join(moduleRoot(t), "sqlc.yaml"))
	for _, r := range cfg.Rules {
		if r.Name != "scope-views-only" {
			continue
		}
		for _, m := range rawPattern.FindAllStringSubmatch(r.Rule, -1) {
			patterns = append(patterns, regexp.MustCompile(m[1]))
		}
		if len(patterns) != 2 {
			t.Fatalf("scope-views-only has %d patterns, want the table one and the write one", len(patterns))
		}
		return r.Rule, patterns
	}
	t.Fatal("sqlc.yaml has no scope-views-only rule")
	return "", nil
}

// fails evaluates the rule (pattern1 || pattern2) on a query text.
func fails(patterns []*regexp.Regexp, sql string) bool {
	return slices.ContainsFunc(patterns, func(re *regexp.Regexp) bool { return re.MatchString(sql) })
}

// TestScopeBlockUsesTheRule: the scope block reads internal/scope/repo, applies the rule, and every
// query file there is a scope_*.sql file.
func TestScopeBlockUsesTheRule(t *testing.T) {
	cfg := readConfig(t, filepath.Join(moduleRoot(t), "sqlc.yaml"))
	i := slices.IndexFunc(cfg.SQL, func(b sqlBlock) bool { return b.Name == "scope" })
	if i < 0 {
		t.Fatal("sqlc.yaml has no scope block")
	}
	if b := cfg.SQL[i]; b.Queries != "internal/scope/repo" || !slices.Equal(b.Rules, []string{"scope-views-only"}) {
		t.Fatalf("scope block reads %q with rules %v", b.Queries, b.Rules)
	}
	files, err := filepath.Glob(filepath.Join(moduleRoot(t), "internal", "scope", "repo", "*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no query files: %v", err)
	}
	for _, f := range files {
		if base := filepath.Base(f); !strings.HasPrefix(base, "scope_") || filepath.Ext(base) != ".sql" {
			t.Errorf("%s: internal/scope/repo holds only scope_*.sql files", base)
		}
	}
}

// TestScopeRuleNamesEveryTable keeps the rule's table list equal to the CREATE TABLE statements of
// migrations/: a table added later is covered or this test fails.
func TestScopeRuleNamesEveryTable(t *testing.T) {
	_, patterns := scopeRule(t)
	alt := regexp.MustCompile(`\(\?:([a-z_|]+)\)`).FindStringSubmatch(patterns[0].String())
	if alt == nil {
		t.Fatalf("unreadable table pattern %s", patterns[0])
	}
	inRule := strings.Split(alt[1], "|")
	var tables []string
	create := regexp.MustCompile(`(?mi)^\s*CREATE TABLE (?:IF NOT EXISTS )?(?:etl\.)?([a-z_]+)`)
	files, err := filepath.Glob(filepath.Join(moduleRoot(t), "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range create.FindAllStringSubmatch(string(b), -1) {
			tables = append(tables, strings.ToLower(m[1]))
		}
	}
	slices.Sort(tables)
	slices.Sort(inRule)
	if len(tables) != 85 {
		t.Errorf("migrations create %d tables, Appendix A has 85", len(tables))
	}
	if !slices.Equal(tables, inRule) {
		t.Errorf("scope-views-only names\n%v\nmigrations create\n%v", inRule, tables)
	}
}

// TestScopeRuleOnQueries: every real scope query passes, every fixture query and a few more shapes fail.
func TestScopeRuleOnQueries(t *testing.T) {
	_, patterns := scopeRule(t)
	repo := filepath.Join(moduleRoot(t), "internal", "scope", "repo")
	for name, sql := range queriesIn(t, repo) {
		if fails(patterns, sql) {
			t.Errorf("%s: a real scope query fails the rule:\n%s", name, sql)
		}
	}
	bad := queriesIn(t, filepath.Join(moduleRoot(t), "internal", "scope", "testdata", "vetcheck", "queries"))
	if len(bad) < 6 {
		t.Fatalf("the vet fixture has %d queries", len(bad))
	}
	for _, sql := range []string{
		"SELECT id FROM etl.quarantine",
		"SELECT t.id FROM scope_tasks t LEFT JOIN LATERAL (SELECT 1 FROM Billing_Statements) x ON true",
		"DELETE FROM scope_trips WHERE id = $1",
		"INSERT INTO scope_standby (id) VALUES ($1)",
	} {
		bad[sql] = sql
	}
	for name, sql := range bad {
		if !fails(patterns, sql) {
			t.Errorf("%s passes the rule:\n%s", name, sql)
		}
	}
}

// queriesIn splits the .sql files of dir at their "-- name:" lines.
func queriesIn(t *testing.T, dir string) map[string]string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	name := regexp.MustCompile(`(?m)^-- name: (\w+)`)
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		idx := name.FindAllStringSubmatchIndex(src, -1)
		for i, m := range idx {
			end := len(src)
			if i+1 < len(idx) {
				end = idx[i+1][0]
			}
			body := src[m[1]:end]
			if nl := strings.IndexByte(body, '\n'); nl >= 0 {
				body = body[nl+1:] // drop the rest of the name line (:many / :one)
			}
			out[src[m[2]:m[3]]] = strings.TrimSpace(body)
		}
	}
	return out
}

// TestVetFixtureCopiesTheRule: make gen-check runs sqlc vet over the fixture config, so its rule must be
// the one the real config applies.
func TestVetFixtureCopiesTheRule(t *testing.T) {
	real := readConfig(t, filepath.Join(moduleRoot(t), "sqlc.yaml"))
	fixture := readConfig(t, filepath.Join(moduleRoot(t), "internal", "scope", "testdata", "vetcheck", "sqlc.yaml"))
	if !reflect.DeepEqual(real.Rules, fixture.Rules) {
		t.Fatalf("internal/scope/testdata/vetcheck/sqlc.yaml rules differ from sqlc.yaml:\n%+v\n%+v", fixture.Rules, real.Rules)
	}
}
