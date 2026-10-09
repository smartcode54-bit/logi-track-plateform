package app

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/health"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/telemetry"
)

// RunBackground runs a long-lived process without HTTP listeners (worker,
// scheduler): it serves /metrics on METRICS_ADDR and blocks until ctx is
// cancelled. Queue consumers and cron jobs are registered from issue T10.
func RunBackground(ctx context.Context, process string, stdout, stderr io.Writer) int {
	cfg, err := config.Load[WorkerConfig]()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", process, err)
		return ExitConfigError
	}
	log, err := NewLogger(cfg.Common, process, stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: %v\n", process, err)
		return ExitConfigError
	}
	LogConfig[WorkerConfig](log)

	stopTracing, err := SetupTracing(ctx, cfg.Common, process)
	if err != nil {
		log.Error().Err(err).Msg("tracing setup failed")
		return ExitRuntimeError
	}
	metrics := telemetry.NewMetrics(process)
	state := health.NewState()
	srv, err := telemetry.ListenMetrics(cfg.MetricsAddr, metrics.Registry, log, func() bool {
		return !state.Draining()
	})
	if err != nil {
		log.Error().Err(err).Msg("listen METRICS_ADDR")
		return ExitRuntimeError
	}
	log.Info().Str("metrics_addr", srv.Addr()).Msg(process + " started; no consumers registered yet (issue T10)")

	<-ctx.Done()
	state.BeginDrain()
	log.Info().Msg("shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	code := ExitOK
	if err := srv.Shutdown(sctx); err != nil {
		log.Error().Err(err).Msg("metrics shutdown")
		code = ExitRuntimeError
	}
	tctx, tcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer tcancel()
	if err := stopTracing(tctx); err != nil {
		log.Warn().Err(err).Msg("tracing shutdown")
	}
	return code
}
