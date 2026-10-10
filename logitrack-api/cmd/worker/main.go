// Command worker consumes the RabbitMQ work queues of Appendix B §B.5.3 selected by WORKER_CONSUMERS,
// with consumer_inbox dedupe and the five-rung retry ladder before {queue}.dead (developer-spec.md
// §2.1, §7.2-§7.3). It also serves /metrics on METRICS_ADDR.
package main

import (
	"os"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
)

func main() {
	ctx, stop := app.SignalContext()
	code := app.RunWorker(ctx, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}
