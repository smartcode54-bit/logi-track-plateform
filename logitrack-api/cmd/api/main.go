// Command api serves the HTTP API on two listeners: internal (every route,
// reached by the web BFF over the private network) and public (mobile, auth,
// postbacks, evidence, local storage objects under /media, liveness). See
// developer-spec.md §2.
//
//	api           serve (configuration from the environment)
//	api routes    check the ingress policy per route and print the route table
//
//go:generate go run . routes -o ../../api/routes.txt
package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/ingress"
)

func main() {
	ctx, stop := app.SignalContext()
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// newAPI is the single place where the route registry meets the listeners: serving and
// `api routes` both build the API here, so the checked table is the served one. Domain route
// groups are added to this call: the auth groups (T05), the jobs groups (T10) and the SSE streams
// (T12) come from deps, which app.BuildAPI wires, GET /v1/roles (T07) and the users and tenants
// administration (T19, deps.Admin, with the carrier profiles of T20), the API keys administration (T32K,
// deps.APIKeys), the customers and companies (T20, deps.Parties) and the hubs, distances and
// POST /v1/jobs/distances.compute (T21, deps.Hubs), the trucks (T22, deps.Fleet) with the truck assignments and
// the drivers (T23, deps.Fleet, deps.Drivers), the vehicle expenses and maintenance with the PM check below /v1/trucks
// (T40, deps.Expenses, deps.Maintenance), the billing rate tables, statements, fuel snapshots and POST
// /v1/jobs/bangchak.snapshot (T37, deps.Billing), the compute-on-delivery routes of T38 (deps.Billing: the trip and
// standby billing routes below /v1/trips and /v1/standby, mounted by operations.Groups, POST
// /v1/jobs/billing.{backfill-trips,backfill-standby,impact-report} and the shim target below /v1/mobile), the R13 task
// reassignment (T28, deps.Operations), the LINE force and evidence revoke routes below /v1/tasks, /v1/trips and
// /v1/standby (T49, deps.LineNotify; the gallery comes with the storage groups, the shim targets with /v1/mobile) and
// the payroll runs,
// penalties, compensation config versions and POST /v1/jobs/payroll.run (T44, deps.HR) and the staff chat console
// /v1/chats* (T47, deps.Chats; the driver's own /v1/mobile/chat* joins the /v1/mobile group) and the staff broadcasts
// /v1/broadcasts* (T48, deps.Broadcasts; the driver's /v1/mobile/broadcasts* joins the /v1/mobile group) sit behind
// deps.Auth.RequireAuth, the web flags (T17) come from the configuration and the storage groups (T11)
// from deps.Storage; the /v1/mobile group (T12's mobile stream and T52's version gate and heartbeat) and the
// installations and release routes of T52 come from deps.Mobile (the publish routes also admit release_publisher
// API keys); the vehicle locations, the Cartrack probe, the dashboard summary, the badges and the forms of T53 come from
// deps.Locations, deps.Dashboard and deps.Forms (app.DashboardGroups: the anonymous form posts are internal-only and
// rate-limited); extra exists for tests. `api routes` passes zero deps: Groups and RequireAuth
// only register handlers and never read a service, so the table needs no database, Redis or signing
// key.
func newAPI(cfg *app.APIConfig, log zerolog.Logger, deps app.APIDeps, extra ...ingress.Group) (*app.API, error) {
	return app.FullAPI(cfg, log, deps, extra...)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		if args[0] == "routes" {
			return routes(args[1:], stdout, stderr)
		}
		_, _ = fmt.Fprintf(stderr, "usage: api            serve (configuration from the environment)\n"+
			"       api routes [-o FILE]  check the ingress policy and print the route table\n")
		return app.ExitConfigError
	}
	return serve(ctx, stdout, stderr)
}

func serve(ctx context.Context, stdout, stderr io.Writer) int {
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

	api, closeDeps, err := app.BuildAPI(ctx, cfg, log, func(deps app.APIDeps) (*app.API, error) {
		return newAPI(cfg, log, deps)
	})
	if err != nil {
		log.Error().Err(err).Msg("api build failed")
		if _, ok := errors.AsType[*config.Error](err); ok {
			return app.ExitConfigError
		}
		return app.ExitRuntimeError
	}
	defer closeDeps()
	if err := api.CheckRoutes(); err != nil {
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

// routes builds the API with every public group allow-listed (the widest PUBLIC_ROUTE_GROUPS),
// fails when a group or a route breaks the ingress policy (main spec §2.6), and otherwise
// writes one line per route: method, path, and the listeners that may serve it. go generate
// keeps api/routes.txt current and make gen-check fails on a stale table (main spec §17.2).
func routes(args []string, stdout, stderr io.Writer, extra ...ingress.Group) int {
	fl := flag.NewFlagSet("routes", flag.ContinueOnError)
	fl.SetOutput(stderr)
	out := fl.String("o", "", "write the table to this file instead of stdout")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		_, _ = fmt.Fprintln(stderr, "usage: api routes [-o FILE]")
		return app.ExitConfigError
	}
	cfg := &app.APIConfig{PublicRouteGroups: ingress.PublicPrefixes}
	a, err := newAPI(cfg, zerolog.Nop(), app.APIDeps{}, extra...)
	if err == nil {
		err = a.CheckRoutes()
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "api routes: %v\n", err)
		return app.ExitRuntimeError
	}
	table := renderRoutes(a.Routes())
	if *out == "" {
		_, _ = io.WriteString(stdout, table)
		return app.ExitOK
	}
	if err := os.WriteFile(*out, []byte(table), 0o644); err != nil {
		_, _ = fmt.Fprintf(stderr, "api routes: %v\n", err)
		return app.ExitRuntimeError
	}
	return app.ExitOK
}

func renderRoutes(byListener map[string][]ingress.Route) string {
	on := map[ingress.Route][]string{}
	var all []ingress.Route
	for _, l := range []string{ingress.Internal, ingress.Public} {
		for _, r := range byListener[l] {
			if on[r] == nil {
				all = append(all, r)
			}
			on[r] = append(on[r], l)
		}
	}
	slices.SortFunc(all, func(a, b ingress.Route) int {
		return cmp.Or(strings.Compare(a.Path, b.Path), strings.Compare(a.Method, b.Method))
	})
	var b strings.Builder
	b.WriteString("# Route table of cmd/api, one route per line: METHOD PATH LISTENERS.\n" +
		"# Generated by `go generate ./cmd/api` (make gen); go-ci gen-check fails when it is stale.\n" +
		"# USE = middleware or a mounted sub-app (Use), matching every path at or below PATH; the\n" +
		"# listener-wide middleware at / is not listed.\n" +
		"# public = served on API_PUBLIC_ADDR when its group is in PUBLIC_ROUTE_GROUPS; only\n" +
		"# /v1/mobile/*, /v1/auth/*, /public/v1/*, /evidence/*, /media/* and /healthz qualify (main spec\n" +
		"# §2.6), and USE only at or below the first five.\n")
	for _, r := range all {
		fmt.Fprintf(&b, "%s %s %s\n", r.Method, r.Path, strings.Join(on[r], ","))
	}
	return b.String()
}
