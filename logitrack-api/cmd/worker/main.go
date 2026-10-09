// Command worker is a long-running background process. Until issue T10 it only
// serves /metrics on METRICS_ADDR and waits for SIGTERM (developer-spec.md §2.1, §7).
package main

import (
	"os"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
)

func main() {
	ctx, stop := app.SignalContext()
	code := app.RunBackground(ctx, "worker", os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
