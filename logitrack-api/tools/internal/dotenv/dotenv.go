// Package dotenv reads and rewrites simple KEY=VALUE files (.env, .env.example)
// for the local developer tools. Values are never printed.
package dotenv

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// Line is one line of a dotenv file; Key is empty for comments and blanks.
type Line struct {
	Raw   string
	Key   string
	Value string
}

// Read parses path, keeping comments so the file can be rewritten in place.
func Read(path string) ([]Line, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []Line
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	n := 0
	for sc.Scan() {
		n++
		raw := sc.Text()
		trim := strings.TrimSpace(raw)
		if trim == "" || strings.HasPrefix(trim, "#") {
			out = append(out, Line{Raw: raw})
			continue
		}
		k, v, ok := strings.Cut(raw, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("%s:%d: expected KEY=VALUE", path, n)
		}
		out = append(out, Line{Raw: raw, Key: strings.TrimSpace(k), Value: v})
	}
	return out, sc.Err()
}

// Map returns key -> value.
func Map(lines []Line) map[string]string {
	m := map[string]string{}
	for _, l := range lines {
		if l.Key != "" {
			m[l.Key] = l.Value
		}
	}
	return m
}

// Write renders lines, replacing values from set, with the given permissions.
func Write(path string, lines []Line, set map[string]string, perm os.FileMode) error {
	var b strings.Builder
	for _, l := range lines {
		if l.Key == "" {
			b.WriteString(l.Raw)
		} else if v, ok := set[l.Key]; ok {
			b.WriteString(l.Key + "=" + v)
		} else {
			b.WriteString(l.Key + "=" + l.Value)
		}
		b.WriteByte('\n')
	}
	// OpenFile applies perm only when it creates the file; Chmod first so an
	// existing, more permissive file never holds the new content.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
