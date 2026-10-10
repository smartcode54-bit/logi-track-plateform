// Command scheduler runs as one active replica (PostgreSQL advisory lock): the outbox relay to
// RabbitMQ and Redis, the Asia/Bangkok cron table and the dead-letter replays (developer-spec.md §2.1,
// §7.1, §7.4). It also serves /metrics on METRICS_ADDR.
package main

import (
	"os"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
)

func main() {
	ctx, stop := app.SignalContext()
	code := app.RunScheduler(ctx, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
