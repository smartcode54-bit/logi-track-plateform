// Package telemetry wires OpenTelemetry tracing and Prometheus metrics.
//
// Tracing exports over OTLP/HTTP only when OTEL_EXPORTER_OTLP_ENDPOINT is set;
// otherwise spans are not recorded but W3C traceparent is still propagated so
// ids flow from the BFF into logs and, later, outbox headers.
package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
)

const instrumentation = "github.com/smartcode54-bit/logi-track-plateform/logitrack-api"

// TracingOptions configure SetupTracing.
type TracingOptions struct {
	Endpoint    string  // OTEL_EXPORTER_OTLP_ENDPOINT; empty disables export
	ServiceName string  // OTEL_SERVICE_NAME; defaults to "logitrack-<process>"
	SamplerArg  float64 // OTEL_TRACES_SAMPLER_ARG, ratio for parent-based sampling
	Env         string
	Version     string
}

// SetupTracing installs the global propagator and, when an endpoint is set, an
// OTLP tracer provider. The returned function flushes and stops it.
func SetupTracing(ctx context.Context, o TracingOptions) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{}, propagation.Baggage{}))
	if o.Endpoint == "" {
		return func(context.Context) error { return nil }, nil
	}
	traces, err := TracesURL(o.Endpoint)
	if err != nil {
		return nil, err
	}
	exp, err := otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(traces))
	if err != nil {
		return nil, err
	}
	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(
		attribute.String("service.name", o.ServiceName),
		attribute.String("service.version", o.Version),
		attribute.String("deployment.environment.name", o.Env),
	))
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(o.SamplerArg))),
	)
	otel.SetTracerProvider(tp)
	return tp.Shutdown, nil
}

// TracesURL turns OTEL_EXPORTER_OTLP_ENDPOINT, a base URL by OTel convention
// (e.g. http://collector:4318), into the traces URL (…/v1/traces).
func TracesURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("OTEL_EXPORTER_OTLP_ENDPOINT: must be an absolute http(s) URL")
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + "/v1/traces"
	return u.String(), nil
}

// headerCarrier adapts Fiber request/response headers to the propagator API.
type headerCarrier struct{ c fiber.Ctx }

func (h headerCarrier) Get(k string) string { return strings.Clone(h.c.Get(k)) }
func (h headerCarrier) Set(k, v string)     { h.c.Request().Header.Set(k, v) }
func (h headerCarrier) Keys() []string {
	var keys []string
	for k := range h.c.Request().Header.All() {
		keys = append(keys, string(k))
	}
	return keys
}

// Tracing starts a server span per request, continues an incoming traceparent,
// and adds trace_id to the request logger when the span is sampled. It must run
// after httpx.Logger.
func Tracing(listener string) fiber.Handler {
	tracer := otel.Tracer(instrumentation)
	return func(c fiber.Ctx) error {
		if httpx.IsFrameworkError(c) {
			return c.Next()
		}
		ctx := otel.GetTextMapPropagator().Extract(c.Context(), headerCarrier{c})
		ctx, span := tracer.Start(ctx, c.Method(), trace.WithSpanKind(trace.SpanKindServer))
		defer span.End()

		if sc := span.SpanContext(); sc.IsValid() {
			l := zerolog.Ctx(ctx).With().Str("trace_id", sc.TraceID().String()).Logger()
			ctx = l.WithContext(ctx)
		}
		c.SetContext(ctx)

		err := c.Next()

		status := c.Response().StatusCode()
		if err != nil {
			status = httpx.AsError(err).Status
		}
		route := httpx.RouteTemplate(c)
		span.SetName(c.Method() + " " + route)
		span.SetAttributes(
			attribute.String("http.request.method", c.Method()),
			attribute.String("http.route", route),
			attribute.Int("http.response.status_code", status),
			attribute.String("logitrack.listener", listener),
			attribute.String("logitrack.request_id", httpx.RequestIDFrom(c)),
		)
		if status >= 500 {
			span.SetStatus(codes.Error, http.StatusText(status))
		}
		return err
	}
}

// Metrics owns the Prometheus registry and the HTTP instruments.
type Metrics struct {
	Registry *prometheus.Registry
	requests *prometheus.CounterVec
	latency  *prometheus.HistogramVec
}

// NewMetrics registers Go/process collectors and the HTTP instruments.
func NewMetrics(service string) *Metrics {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m := &Metrics{
		Registry: reg,
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "http_requests_total",
			Help:        "HTTP requests by listener, method, route template and status.",
			ConstLabels: prometheus.Labels{"service": service},
		}, []string{"listener", "method", "route", "status"}),
		latency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "http_request_duration_seconds",
			Help:        "HTTP request latency by listener, method and route template.",
			Buckets:     prometheus.DefBuckets,
			ConstLabels: prometheus.Labels{"service": service},
		}, []string{"listener", "method", "route"}),
	}
	reg.MustRegister(m.requests, m.latency)
	return m
}

// Middleware records one sample per request using the route template, never
// the raw path, to bound label cardinality.
func (m *Metrics) Middleware(listener string) fiber.Handler {
	return func(c fiber.Ctx) error {
		if httpx.IsFrameworkError(c) {
			return c.Next() // recorded by the edge handler with the final status
		}
		start := time.Now()
		err := c.Next()
		status := c.Response().StatusCode()
		if err != nil {
			status = httpx.AsError(err).Status
		}
		m.Observe(listener, c.Method(), httpx.RouteTemplate(c), status, time.Since(start))
		return err
	}
}

// Observe records one request. Requests that never reached a route use
// route "unmatched".
func (m *Metrics) Observe(listener, method, route string, status int, d time.Duration) {
	m.requests.WithLabelValues(listener, method, route, strconv.Itoa(status)).Inc()
	m.latency.WithLabelValues(listener, method, route).Observe(d.Seconds())
}

// MetricsServer serves /metrics on its own private address (METRICS_ADDR).
type MetricsServer struct {
	srv *http.Server
	ln  net.Listener
}

// ListenMetrics binds addr and serves the registry until Shutdown. When ready
// is not nil the server also answers GET /healthz (200) and GET /readyz, which
// compose uses for the worker and scheduler (main spec §15.1): 200 when ready
// returns nil, else 503 with the R48 envelope of the returned *httpx.Error
// (health.State.Ready).
func ListenMetrics(addr string, reg *prometheus.Registry, log zerolog.Logger, ready func(ctx context.Context) error) (*MetricsServer, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	if ready != nil {
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"status":"ok"}}`))
		})
		mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if err := ready(r.Context()); err != nil {
				w.WriteHeader(http.StatusServiceUnavailable)
				_, _ = w.Write(notReadyBody(err))
				return
			}
			_, _ = w.Write([]byte(`{"data":{"status":"ready"}}`))
		})
	}
	s := &MetricsServer{
		srv: &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second},
		ln:  ln,
	}
	go func() {
		if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error().Err(err).Msg("metrics server stopped")
		}
	}()
	return s, nil
}

// notReadyBody renders the 503 of /readyz in the R48 envelope: the code, message and details of an
// *httpx.Error, a generic text otherwise (never a driver error, which may name hosts).
func notReadyBody(err error) []byte {
	type body struct {
		Code      string         `json:"code"`
		Message   string         `json:"message"`
		Details   map[string]any `json:"details"`
		RequestID string         `json:"requestId"`
	}
	b := body{Code: "unavailable", Message: "not ready", Details: map[string]any{}}
	if he, ok := errors.AsType[*httpx.Error](err); ok {
		b.Message = he.Message
		if he.Details != nil {
			b.Details = he.Details
		}
	}
	out, mErr := json.Marshal(map[string]body{"error": b})
	if mErr != nil {
		return []byte(`{"error":{"code":"unavailable","message":"not ready","details":{},"requestId":""}}`)
	}
	return out
}

// Addr is the bound address (useful with ":0" in tests).
func (s *MetricsServer) Addr() string { return s.ln.Addr().String() }

// Shutdown stops the metrics server.
func (s *MetricsServer) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }
