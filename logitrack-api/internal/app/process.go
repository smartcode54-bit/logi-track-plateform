package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/logx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/telemetry"
)

// Exit codes shared by every binary.
const (
	ExitOK             = 0
	ExitRuntimeError   = 1
	ExitConfigError    = 2
	ExitNotImplemented = 3
)

// SignalContext is cancelled on SIGINT or SIGTERM.
func SignalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

// NewLogger builds the process logger from the common settings.
func NewLogger(c Common, service string, out io.Writer) (zerolog.Logger, error) {
	return logx.New(logx.Options{
		Level: c.LogLevel, Format: c.LogFormat, Service: service,
		Env: c.AppEnv, Version: BuildVersion(), Out: out,
	})
}

// LogConfig writes which variables are set, never their values (§16.5).
func LogConfig[T any](log zerolog.Logger) {
	d := config.Describe[T]()
	names := make([]string, 0, len(d))
	for n := range d {
		names = append(names, n)
	}
	sort.Strings(names)
	ev := log.Info()
	for _, n := range names {
		ev = ev.Str(n, d[n])
	}
	ev.Msg("configuration (set/unset only)")
}

// SetupTracing starts tracing for a process.
func SetupTracing(ctx context.Context, c Common, process string) (func(context.Context) error, error) {
	name := c.OTelServiceName
	if name == "" {
		name = "logitrack-" + process
	}
	return telemetry.SetupTracing(ctx, telemetry.TracingOptions{
		Endpoint: c.OTelEndpoint, ServiceName: name, SamplerArg: c.OTelSamplerArg,
		Env: c.AppEnv, Version: BuildVersion(),
	})
}

// NotImplemented is the body of the one-shot binaries whose behaviour lands in
// later issues; it exits non-zero so a compose job never reports success.
func NotImplemented(name, issue string, stderr io.Writer) int {
	_, _ = fmt.Fprintf(stderr, "%s: not implemented yet; it lands in issue %s (developer-spec.md §18)\n", name, issue)
	return ExitNotImplemented
}
