package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

var nameRx = regexp.MustCompile(`^[a-z0-9]+(?:_[a-z0-9]+)*$`)

// Create writes the next migration file into dir (the source tree, not the embedded copy) and
// returns its path. The new file fails Check until both sections hold SQL, so an unfinished
// migration cannot slip through review.
func Create(dir, name string) (string, error) {
	if !nameRx.MatchString(name) {
		return "", fmt.Errorf("migrate: name %q must be lower_snake_case (a-z, 0-9, _)", name)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("migrate: read %s: %w", dir, err)
	}
	var last int64
	for _, e := range entries {
		if m := fileRx.FindStringSubmatch(e.Name()); m != nil {
			v, _ := strconv.ParseInt(m[1], 10, 64)
			last = max(last, v)
		}
	}
	next := last + 1
	if next > 9999 {
		return "", errors.New("migrate: version 9999 reached; file names have four digits")
	}
	file := fmt.Sprintf("%04d_%s.sql", next, name)
	p := filepath.Join(dir, file)
	body := fmt.Sprintf(`-- %s. Never edit this file once it is merged; follow-ups take the next number (R31).
-- A data migration that cannot be undone keeps an empty Down section and the line "-- irreversible".
-- +goose Up


-- +goose Down

`, file)
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", fmt.Errorf("migrate: create %s: %w", p, err)
	}
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("migrate: write %s: %w", p, err)
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("migrate: write %s: %w", p, err)
	}
	return p, nil
}
