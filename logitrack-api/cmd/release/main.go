// Command release publishes a driver-app APK from the private network (developer-spec.md §9.7, ADR 0007, R43, R82;
// replaces logitrack-web/scripts/publish-mobile-release.mjs). It reads exactly two environment names, GO_API_INTERNAL_URL
// (the api's internal listener) and RELEASE_API_KEY (an api_keys secret of scope release_publisher), and calls
// POST /v1/app-releases/presign, the signed upload, and POST /v1/app-releases. Publishing announces a build; it never
// moves the version floor, which a person raises on the Security Center's mobile-release page.
//
//	release publish --flavor=dev|prod --apk=FILE --version=X.Y.Z --build-number=N [flags]
//	release publish --flavor=dev|prod --metadata=build/app/outputs/apk/<flavor>/release/output-metadata.json [flags]
//	release status [--flavor=dev|prod]
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/app"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/config"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/release"
)

// timeout bounds one run: an APK of up to 200 MB over the private network.
const timeout = 30 * time.Minute

const usage = `usage: release publish --flavor=dev|prod (--apk=FILE --version=X.Y.Z --build-number=N | --metadata=FILE) [flags]
       release status [--flavor=dev|prod]

environment: GO_API_INTERNAL_URL (the api's internal listener), RELEASE_API_KEY (scope release_publisher; publish only)

publish flags:
  --flavor dev|prod      the build's flavor (required; the deployment refuses the other flavor unless --switch-flavor)
  --apk FILE             the APK (default: the universal APK named by --metadata)
  --version X.Y.Z        the pubspec version (default: versionName of --metadata)
  --build-number N       the pubspec build number (default: versionCode of --metadata)
  --metadata FILE        Gradle output-metadata.json: refuses a split-per-ABI build, a version or build that differs,
                         and an applicationId of the other flavor
  --notes TEXT           release notes shown on the mobile-release page and to drivers
  --force                replace an upload of this version that was never published (a published one is immutable)
  --switch-flavor        make --flavor the deployment's gate flavor
  --skip-url-check       publish without checking that the public URL serves the APK
  --dry-run              hash, print the plan and the current gate; upload and publish nothing
`

func main() {
	ctx, stop := app.SignalContext()
	code := run(ctx, os.Args[1:], os.Environ(), os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, usage)
		return app.ExitConfigError
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	switch args[0] {
	case "publish":
		return publish(ctx, args[1:], environ, stdout, stderr)
	case "status":
		return status(ctx, args[1:], environ, stdout, stderr)
	}
	_, _ = io.WriteString(stderr, usage)
	return app.ExitConfigError
}

func publish(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("publish", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	var b release.Build
	var notes, metadata string
	fl.StringVar(&b.Flavor, "flavor", "", "")
	fl.StringVar(&b.APK, "apk", "", "")
	fl.StringVar(&b.Version, "version", "", "")
	fl.StringVar(&b.BuildNumber, "build-number", "", "")
	fl.StringVar(&metadata, "metadata", "", "")
	fl.StringVar(&notes, "notes", "", "")
	fl.BoolVar(&b.Force, "force", false, "")
	fl.BoolVar(&b.SwitchFlavor, "switch-flavor", false, "")
	fl.BoolVar(&b.SkipURLCheck, "skip-url-check", false, "")
	fl.BoolVar(&b.DryRun, "dry-run", false, "")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 {
		_, _ = fmt.Fprintf(stderr, "release publish: %v\n%s", argError(err, fl), usage)
		return app.ExitConfigError
	}
	if notes != "" {
		b.Notes = &notes
	}
	if metadata != "" {
		if err := release.ApplyMetadata(&b, metadata); err != nil {
			_, _ = fmt.Fprintf(stderr, "release publish: %v\n", err)
			return app.ExitConfigError
		}
	}
	if err := b.Validate(); err != nil {
		_, _ = fmt.Fprintf(stderr, "release publish: %v\n", err)
		return app.ExitConfigError
	}
	cfg, err := config.LoadFrom[release.Config](environ)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "release: %v\n", err) // names only, never a value
		return app.ExitConfigError
	}
	c, err := release.NewClient(*cfg)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "release: %v\n", err)
		return app.ExitConfigError
	}
	res, err := c.Publish(ctx, b, stdout)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "release publish: %v\n", err)
		return app.ExitRuntimeError
	}
	if b.DryRun {
		return app.ExitOK
	}
	switch {
	case res.Replayed:
		_, _ = fmt.Fprintf(stdout, "Already published: %s %s+%s, %s\n", res.Flavor, res.Version, res.BuildNumber, res.APKURL)
	case res.Latest:
		_, _ = fmt.Fprintf(stdout, "Published %s %s+%s as the latest release: %s\n", res.Flavor, res.Version, res.BuildNumber, res.APKURL)
	default:
		_, _ = fmt.Fprintf(stdout, "Published %s %s+%s (recorded; not the latest release, the gate is unchanged): %s\n",
			res.Flavor, res.Version, res.BuildNumber, res.APKURL)
	}
	_, _ = io.WriteString(stdout, "The version floor is unchanged: raise it on /app/security-center/mobile-release when the fleet is ready (ADR 0007).\n")
	return app.ExitOK
}

// statusConfig is the environment of `release status`: the anonymous gate needs no key.
type statusConfig struct {
	GoAPIInternalURL string `env:"GO_API_INTERNAL_URL,required"`
}

func status(ctx context.Context, args, environ []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("status", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	flavor := fl.String("flavor", "", "")
	if err := fl.Parse(args); err != nil || fl.NArg() != 0 || (*flavor != "" && *flavor != "dev" && *flavor != "prod") {
		_, _ = fmt.Fprintf(stderr, "release status: %v\n%s", argError(err, fl), usage)
		return app.ExitConfigError
	}
	cfg, err := config.LoadFrom[statusConfig](environ)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "release: %v\n", err)
		return app.ExitConfigError
	}
	c, err := release.NewClient(release.Config{GoAPIInternalURL: cfg.GoAPIInternalURL})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "release: %v\n", err)
		return app.ExitConfigError
	}
	g, err := c.Gate(ctx, *flavor)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "release status: %v\n", err)
		return app.ExitRuntimeError
	}
	_, _ = fmt.Fprintf(stdout, "flavor %s\nlatest %s+%s\nfloor  %s\nlink   %s\n", val(g.Flavor), val(g.LatestVersion),
		val(g.LatestBuildNumber), val(g.MinAllowedVersion), val(g.APKDownloadURL))
	return app.ExitOK
}

func argError(err error, fl *flag.FlagSet) error {
	if err != nil {
		return err
	}
	return errors.New("unexpected arguments " + fmt.Sprint(fl.Args()))
}

func val(p *string) string {
	if p == nil || *p == "" {
		return "-"
	}
	return *p
}
