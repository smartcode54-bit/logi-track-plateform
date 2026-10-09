// Command devenv creates logitrack-api/.env from .env.example for the local
// stack: secret names get random local-only values and the per-process
// connection URLs are composed from them (developer-spec.md §15.7). It refuses
// to run unless APP_ENV is local and never prints a value.
package main

import (
	"crypto/rand"
	"flag"
	"fmt"
	"math/big"
	"os"
	"sort"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/tools/internal/dotenv"
)

const alphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func random(n int) string {
	b := make([]byte, n)
	for i := range b {
		k, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			panic(err)
		}
		b[i] = alphabet[k.Int64()]
	}
	return string(b)
}

func main() {
	src := flag.String("example", ".env.example", "template")
	dst := flag.String("out", ".env", "output")
	force := flag.Bool("force", false, "overwrite an existing output file")
	flag.Parse()

	if _, err := os.Stat(*dst); err == nil && !*force {
		fmt.Fprintf(os.Stderr, "devenv: %s already exists; keep it, or rerun with FORCE=1 (local data volumes then need `make reset`)\n", *dst)
		os.Exit(1)
	}
	lines, err := dotenv.Read(*src)
	if err != nil {
		fmt.Fprintln(os.Stderr, "devenv:", err)
		os.Exit(1)
	}
	ex := dotenv.Map(lines)
	if ex["APP_ENV"] != "local" {
		fmt.Fprintln(os.Stderr, "devenv: only for APP_ENV=local")
		os.Exit(2)
	}

	db := ex["POSTGRES_DB"]
	pw := map[string]string{"app": random(32), "migrator": random(32), "etl": random(32)}
	rabbitUser, rabbitPass := ex["RABBITMQ_DEFAULT_USER"], random(32)
	set := map[string]string{
		"POSTGRES_PASSWORD":     random(32),
		"DATABASE_URL":          "postgres://logitrack_app:" + pw["app"] + "@postgres:5432/" + db + "?sslmode=disable",
		"MIGRATE_DATABASE_URL":  "postgres://logitrack_migrator:" + pw["migrator"] + "@postgres:5432/" + db + "?sslmode=disable",
		"ETL_DATABASE_URL":      "postgres://logitrack_etl:" + pw["etl"] + "@postgres:5432/" + db + "?sslmode=disable",
		"REDIS_URL":             "redis://redis:6379/0",
		"RABBITMQ_DEFAULT_PASS": rabbitPass,
		"RABBITMQ_URL":          "amqp://" + rabbitUser + ":" + rabbitPass + "@rabbitmq:5672/",
		"MINIO_ROOT_PASSWORD":   random(32),
		"S3_ACCESS_KEY_ID":      "logitrack-app",
		"S3_SECRET_ACCESS_KEY":  random(40),
		"API_KEY_PEPPER":        random(48),
		"SEED_DEFAULT_PASSWORD": random(20),
	}
	// Keep the key id of an existing .env so FORCE=1 does not orphan the
	// signing key written by `make dev-keys`.
	if old, err := dotenv.Read(*dst); err == nil {
		if kid := dotenv.Map(old)["JWT_ACTIVE_KID"]; kid != "" {
			set["JWT_ACTIVE_KID"] = kid
		}
	}
	for k := range set {
		if _, ok := ex[k]; !ok {
			fmt.Fprintf(os.Stderr, "devenv: %s is not in %s\n", k, *src)
			os.Exit(1)
		}
	}
	if err := dotenv.Write(*dst, lines, set, 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "devenv:", err)
		os.Exit(1)
	}
	names := make([]string, 0, len(set))
	for k := range set {
		names = append(names, k)
	}
	sort.Strings(names)
	fmt.Printf("devenv: wrote %s (mode 0600) with local values for %v\n", *dst, names)
	fmt.Println("devenv: integration credentials (FCM, LINE, Cartrack, Google, SMTP relay) stay blank; those features are off locally")
}
