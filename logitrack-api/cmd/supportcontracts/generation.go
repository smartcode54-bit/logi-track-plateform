package main

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

func sqlcOutputs(root string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(root, "sqlc.yaml"))
	if err != nil {
		return nil, err
	}
	var out []string
	for line := range strings.Lines(string(data)) {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "out:" {
			out = append(out, strings.Trim(fields[1], "\"'"))
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no sqlc output directories discovered")
	}
	return out, nil
}
func generatedSnapshot(root string, dirs []string) (map[string][32]byte, error) {
	result := map[string][32]byte{}
	capture := func(path string) error {
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return err
		}
		result[path] = sha256.Sum256(data)
		return nil
	}
	for _, path := range []string{"api/routes.txt", "api/support-policies.json", "api/support-discovery.json", "deploy/rabbitmq-definitions.json", "../shared-docs/schemas/capabilities.ts", "../shared-docs/schemas/supportAccess.ts"} {
		if err := capture(path); err != nil {
			return nil, err
		}
	}
	for _, dir := range dirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			return capture(relative)
		})
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

// verifyGeneration provides the same orphan/staleness check in an uncommitted
// worktree. CI keeps gen-check's additional comparison with committed outputs.
func verifyGeneration(root string) error {
	dirs, err := sqlcOutputs(root)
	if err != nil {
		return err
	}
	before, err := generatedSnapshot(root, dirs)
	if err != nil {
		return err
	}
	run := func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Dir = root
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		return cmd.Run()
	}
	if err = run("go", "generate", "./..."); err != nil {
		return err
	}
	for _, dir := range dirs {
		files, err := filepath.Glob(filepath.Join(root, dir, "*.sql.go"))
		if err != nil {
			return err
		}
		for _, file := range files {
			if err := os.Remove(file); err != nil {
				return err
			}
		}
	}
	if err = run("make", "DOCKER_HOST=", "sqlc"); err != nil {
		return err
	}
	after, err := generatedSnapshot(root, dirs)
	if err != nil {
		return err
	}
	var changed []string
	for path, hash := range before {
		if after[path] != hash {
			changed = append(changed, path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			changed = append(changed, path)
		}
	}
	if len(changed) > 0 {
		slices.Sort(changed)
		return fmt.Errorf("stale/orphaned generated output: %s", strings.Join(changed, ", "))
	}
	fmt.Printf("generation: %d outputs in %d sqlc packages and shared contracts reproduced exactly\n", len(after), len(dirs))
	return nil
}
