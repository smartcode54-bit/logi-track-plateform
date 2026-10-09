// Command api serves the HTTP API on two listeners: internal (every route,
// reached by the web BFF over the private network) and public (mobile, auth,
// postbacks, evidence, liveness). See developer-spec.md §2.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
)

func main() {
	ctx, stop := app.SignalContext()
	code := run(ctx, os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, stdout, stderr io.Writer) int {
	cfg, err := config.Load[app.APIConfig]()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "api: %v\n", err)
		return app.ExitConfigError
	}
	log, err := app.NewLogger(cfg.Common, "api", stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "api: %v\n", err)
		return app.ExitConfigError
	}
	app.LogConfig[app.APIConfig](log)

	stopTracing, err := app.SetupTracing(ctx, cfg.Common, "api")
	if err != nil {
		log.Error().Err(err).Msg("tracing setup failed")
		return app.ExitRuntimeError
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := stopTracing(sctx); err != nil {
			log.Warn().Err(err).Msg("tracing shutdown")
		}
	}()

	api, err := app.NewAPI(cfg, log)
	if err != nil {
		log.Error().Err(err).Msg("api build failed")
		return app.ExitRuntimeError
	}
	if err := api.Listen(); err != nil {
		log.Error().Err(err).Msg("api listen failed")
		return app.ExitRuntimeError
	}
	if err := api.Serve(ctx); err != nil {
		log.Error().Err(err).Msg("api stopped with error")
		return app.ExitRuntimeError
	}
	return app.ExitOK
}
