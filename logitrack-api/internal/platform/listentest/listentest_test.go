package listentest_test

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/listentest"
)

// The listener stays bound: a connection reaches it, and nothing else can bind its address until the test ends.
func TestLoopbackHoldsTheAddress(t *testing.T) {
	ln := listentest.Loopback(t)
	addr := ln.Addr().String()
	if host, _, err := net.SplitHostPort(addr); err != nil || host != "127.0.0.1" {
		t.Fatalf("Loopback bound %s (%v), want 127.0.0.1", addr, err)
	}
	accepted := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.Close()
		}
		accepted <- err
	}()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	_ = c.Close()
	if err := <-accepted; err != nil {
		t.Fatalf("accept: %v", err)
	}
	if other, err := net.Listen("tcp", addr); err == nil {
		_ = other.Close()
		t.Fatalf("%s could be bound again while Loopback holds it", addr)
	}
}

// Helpers that pick a port and release it for a server to bind later are gone from the module (FLAKE3); a copy
// of one (from a branch cut before, or from an older harness) fails here. Use listentest.Loopback and hand the
// listener to the server. push_test's "nobody listens" case releases a port on purpose, inline, to test a
// refused connection, and is not a helper.
func TestNoPickAndReleaseHelpers(t *testing.T) {
	root, err := moduleRoot()
	if err != nil {
		t.Fatal(err)
	}
	helper := regexp.MustCompile(`^func (?:freeAddr|freePort)\(`)
	var found []string
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "testdata" || d.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		s := bufio.NewScanner(f)
		s.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for n := 1; s.Scan(); n++ {
			if helper.MatchString(s.Text()) {
				rel, _ := filepath.Rel(root, path)
				found = append(found, fmt.Sprintf("%s:%d: %s", rel, n, strings.TrimSpace(s.Text())))
			}
		}
		return s.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range found {
		t.Errorf("%s: a port picked and released is bound again later and can be taken in between; "+
			"bind it with listentest.Loopback and give the listener to the server (app.Listeners)", f)
	}
}

// moduleRoot is the directory of the go.mod above the working directory (the package directory under go test).
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("listentest: no go.mod above the working directory")
		}
		dir = parent
	}
}
