// Package migrate applies the goose chain (developer-spec.md §3.5) and enforces the migration
// rules of R31 before goose sees a file:
//
//   - files are NNNN_name.sql, numbered 1..N without gaps or duplicates;
//   - every file has an Up and a Down section; the Down section holds SQL unless the file is a
//     data migration marked "-- irreversible" (the CI round trip stops above it);
//   - "-- +goose NO TRANSACTION" is used exactly when the file builds or drops an index
//     CONCURRENTLY (0010_d5_unique_constraints, R88);
//   - ids come from uuidv7(), never gen_random_uuid() (R34); goose ENVSUB is not used, so no value
//     from the environment can reach a migration.
package migrate

import (
	"bufio"
	"bytes"
	"cmp"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// File is one migration of a checked chain.
type File struct {
	Version       int64
	Name          string // e.g. 0001_preamble.sql
	NoTransaction bool
	Irreversible  bool
}

// CheckError lists every rule a chain breaks.
type CheckError struct {
	Problems []string
}

func (e *CheckError) Error() string {
	return "migration check failed:\n  " + strings.Join(e.Problems, "\n  ")
}

var fileRx = regexp.MustCompile(`^([0-9]{4})_([a-z0-9]+(?:_[a-z0-9]+)*)\.sql$`)

// Check lints the migration files at the root of fsys and returns them in version order. Go files
// (the embed package) and dot files are ignored; anything else must be a migration.
func Check(fsys fs.FS) ([]File, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("migrate: read migrations: %w", err)
	}
	var problems []string
	var files []File
	seen := map[int64]string{}
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasPrefix(name, "."), strings.HasSuffix(name, ".go"):
			continue
		case e.IsDir():
			problems = append(problems, fmt.Sprintf("%s: unexpected directory; migrations are flat NNNN_name.sql files", name))
			continue
		}
		m := fileRx.FindStringSubmatch(name)
		if m == nil {
			problems = append(problems, fmt.Sprintf("%s: file names are NNNN_name.sql (four digits, lower_snake name)", name))
			continue
		}
		v, _ := strconv.ParseInt(m[1], 10, 64)
		if prev, dup := seen[v]; dup {
			problems = append(problems, fmt.Sprintf("%s: version %d is also used by %s", name, v, prev))
			continue
		}
		seen[v] = name
		src, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("migrate: read %s: %w", name, err)
		}
		f, ps := inspect(name, src)
		f.Version = v
		problems = append(problems, ps...)
		files = append(files, f)
	}
	slices.SortFunc(files, func(a, b File) int { return cmp.Compare(a.Version, b.Version) })
	if len(files) == 0 && len(problems) == 0 {
		problems = append(problems, "no migration files")
	}
	for i, f := range files {
		if want := int64(i + 1); f.Version != want {
			problems = append(problems, fmt.Sprintf("%s: expected version %04d here; versions run 1..N without gaps", f.Name, want))
			break
		}
	}
	if len(problems) > 0 {
		return nil, &CheckError{Problems: problems}
	}
	return files, nil
}

// RoundTripFloor is the version the CI round trip rolls back to: the highest version marked
// "-- irreversible", or 0 (R31).
func RoundTripFloor(files []File) int64 {
	var floor int64
	for _, f := range files {
		if f.Irreversible && f.Version > floor {
			floor = f.Version
		}
	}
	return floor
}

type section int

const (
	sectionNone section = iota
	sectionUp
	sectionDown
)

var (
	// Statements that cannot run inside a transaction block.
	concurrentlyRx = regexp.MustCompile(`(?is)\bcreate\s+(?:unique\s+)?index\s+concurrently\b|` +
		`\bdrop\s+index\s+concurrently\b|` +
		`\breindex\s+(?:\([^)]*\)\s*)?(?:index|table|schema|database|system)\s+concurrently\b|` +
		`\bdetach\s+partition\s+\S+\s+concurrently\b`)
	genRandomUUIDRx = regexp.MustCompile(`(?i)\bgen_random_uuid\s*\(`)
)

// inspect checks one file the way goose's parser reads it: a line whose trimmed text starts with
// "--" and contains "+goose" is an annotation, any other line starting with "--" is a comment.
func inspect(name string, src []byte) (File, []string) {
	f := File{Name: name}
	var problems []string
	bad := func(format string, a ...any) { problems = append(problems, name+": "+fmt.Sprintf(format, a...)) }

	sec := sectionNone
	var seenUp, seenDown, inBlock, blockSQL bool
	sqlLines := map[section]int{}
	code := map[section]*strings.Builder{sectionUp: {}, sectionDown: {}}
	var cs codeScanner
	sc := bufio.NewScanner(bytes.NewReader(src))
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimRight(sc.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "--") && strings.Contains(line, "+goose"):
			if line != strings.TrimLeft(line, " \t") {
				bad("line %d: a goose annotation must start at column 1", n)
			}
			cmd := strings.TrimSpace(strings.Replace(strings.ReplaceAll(line, "--", ""), "+goose", "", 1))
			switch {
			case strings.EqualFold(cmd, "Up"):
				if seenUp || sec != sectionNone {
					bad("line %d: '-- +goose Up' must appear once, before everything else", n)
				}
				seenUp, sec = true, sectionUp
			case strings.EqualFold(cmd, "Down"):
				if seenDown || sec != sectionUp {
					bad("line %d: '-- +goose Down' must appear once, after the Up section", n)
				}
				if inBlock {
					bad("line %d: '-- +goose StatementBegin' is not closed before Down", n)
					inBlock = false
				}
				seenDown, sec = true, sectionDown
			case strings.EqualFold(cmd, "StatementBegin"):
				if inBlock || sec == sectionNone {
					bad("line %d: misplaced '-- +goose StatementBegin'", n)
				}
				inBlock, blockSQL = true, false
			case strings.EqualFold(cmd, "StatementEnd"):
				switch {
				case !inBlock:
					bad("line %d: '-- +goose StatementEnd' without StatementBegin", n)
				case !blockSQL:
					// goose v3.28 then drops the next SQL line without an error.
					bad("line %d: empty '-- +goose StatementBegin' block; goose would silently drop the next statement", n)
				}
				inBlock = false
			case strings.EqualFold(cmd, "NO TRANSACTION"):
				f.NoTransaction = true
			case strings.HasPrefix(strings.ToUpper(cmd), "ENVSUB"):
				bad("line %d: ENVSUB is not used; no environment value may reach a migration", n)
			default:
				bad("line %d: unknown goose annotation %q", n, cmd)
			}
		case strings.HasPrefix(trimmed, "--"):
			text := strings.TrimSpace(strings.TrimPrefix(trimmed, "--"))
			if text == "irreversible" || strings.HasPrefix(text, "irreversible:") {
				if sec != sectionNone {
					bad("line %d: the '-- irreversible' marker belongs in the file header, before '-- +goose Up'", n)
				} else {
					f.Irreversible = true
				}
			}
		default:
			if sec == sectionNone {
				bad("line %d: SQL before '-- +goose Up'", n)
				continue
			}
			sqlLines[sec]++
			if inBlock {
				blockSQL = true
			}
			stripped := cs.strip(line)
			code[sec].WriteString(stripped)
			code[sec].WriteByte('\n')
			if genRandomUUIDRx.MatchString(stripped) {
				bad("line %d: ids default to uuidv7(), not gen_random_uuid() (R34)", n)
			}
		}
	}
	if err := sc.Err(); err != nil {
		bad("%v", err)
	}
	switch {
	case !seenUp:
		bad("has no '-- +goose Up' section")
	case sqlLines[sectionUp] == 0:
		bad("the Up section is empty")
	}
	if !seenDown {
		bad("has no '-- +goose Down' section; every migration has one (R31), a data migration that cannot be undone keeps an empty Down and is marked '-- irreversible'")
	} else if sqlLines[sectionDown] == 0 && !f.Irreversible {
		bad("the Down section is empty; write the rollback, or mark a data migration '-- irreversible' (R31)")
	}
	if inBlock {
		bad("'-- +goose StatementBegin' is never closed")
	}
	// goose runs a file's Up and Down with the same transaction mode.
	concurrently := concurrentlyRx.MatchString(code[sectionUp].String()) || concurrentlyRx.MatchString(code[sectionDown].String())
	if f.NoTransaction && !concurrently {
		bad("'-- +goose NO TRANSACTION' is reserved for CREATE/DROP INDEX CONCURRENTLY (R31)")
	}
	if concurrently && !f.NoTransaction {
		bad("CONCURRENTLY cannot run inside a transaction; add '-- +goose NO TRANSACTION' and keep the file to index statements")
	}
	return f, problems
}

// codeScanner removes comments, quoted strings and quoted identifiers from SQL lines so the rules
// match code only. Dollar-quoted bodies stay visible: a function body is code too (R34 applies to
// the ids it generates).
type codeScanner struct {
	inString, inIdent, inBlockComment bool
}

func (c *codeScanner) strip(line string) string {
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case c.inBlockComment:
			if ch == '*' && i+1 < len(line) && line[i+1] == '/' {
				c.inBlockComment = false
				i++
			}
		case c.inString:
			if ch == '\'' {
				c.inString = false
			}
		case c.inIdent:
			if ch == '"' {
				c.inIdent = false
			}
		case ch == '-' && i+1 < len(line) && line[i+1] == '-':
			return b.String()
		case ch == '/' && i+1 < len(line) && line[i+1] == '*':
			c.inBlockComment = true
			i++
		case ch == '\'':
			c.inString = true
			b.WriteByte(' ')
		case ch == '"':
			c.inIdent = true
			b.WriteByte(' ')
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}
