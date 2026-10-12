// Package listentest binds loopback listeners for tests that need an address before the server that serves it
// starts: an API harness whose local storage backend builds its PublicBaseURL from API_PUBLIC_ADDR, or a worker or
// scheduler test that probes /readyz on METRICS_ADDR.
//
// A port picked and released (listen on 127.0.0.1:0, read the address, close) and bound again later is a race:
// between the close and the second bind another process can take the port (testcontainers' docker-proxy, a test
// binary of another package running in parallel), and the server fails with "bind: address already in use" (go-ci
// run 38146681561, FLAKE3). Loopback keeps the listener open; the test hands the listener itself to the server
// (app.API.ListenOn with app.Listeners, app.RunWorkerOn, app.RunSchedulerOn) and uses its address for the config.
package listentest

import (
	"net"
	"testing"
)

// Loopback binds 127.0.0.1:0 and returns the open listener. The server it is given owns it from then on; the
// cleanup closes it for a test that ends before the server takes it (closing it again after the server's shutdown
// is harmless).
func Loopback(tb testing.TB) net.Listener {
	tb.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("listentest: listen: %v", err)
	}
	tb.Cleanup(func() { _ = ln.Close() })
	return ln
}
