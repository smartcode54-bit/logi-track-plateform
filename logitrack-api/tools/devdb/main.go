// Command devdb prints ALTER ROLE statements that set the local LOGIN passwords
// of the roles created by deploy/postgres-init/00-roles.sql from the URLs in
// .env (developer-spec.md §15.3). `make dev-db` pipes them into psql inside the
// postgres container.
//
// The statements carry a SCRAM-SHA-256 verifier, never the cleartext, so the
// password does not reach pg_stat_statements, the server log or the terminal.
// The session also turns off utility tracking and failed-statement logging.
package main

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/tools/internal/dotenv"
)

var urls = []struct{ env, role string }{
	{"DATABASE_URL", "logitrack_app"},
	{"MIGRATE_DATABASE_URL", "logitrack_migrator"},
	{"ETL_DATABASE_URL", "logitrack_etl"},
}

func main() {
	envPath := flag.String("env", ".env", "dotenv file")
	flag.Parse()
	lines, err := dotenv.Read(*envPath)
	if err != nil {
		fail(err.Error())
	}
	env := dotenv.Map(lines)
	if env["APP_ENV"] != "local" {
		fail("only for APP_ENV=local")
	}
	var b strings.Builder
	b.WriteString("SET pg_stat_statements.track_utility = off;\nSET log_min_error_statement = panic;\n")
	for _, u := range urls {
		raw := env[u.env]
		if raw == "" {
			fail(u.env + " is empty (run make env)")
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.User == nil {
			fail(u.env + " is not a postgres URL with credentials")
		}
		if parsed.User.Username() != u.role {
			fail(fmt.Sprintf("%s must log in as %s (R66)", u.env, u.role))
		}
		pw, ok := parsed.User.Password()
		if !ok || pw == "" {
			fail(u.env + " has no password")
		}
		verifier, err := scramVerifier(pw)
		if err != nil {
			fail(err.Error())
		}
		fmt.Fprintf(&b, "ALTER ROLE %s WITH PASSWORD '%s';\n", u.role, verifier)
	}
	fmt.Print(b.String())
}

func fail(msg string) {
	fmt.Fprintln(os.Stderr, "devdb:", msg)
	os.Exit(1)
}

// scramVerifier returns the PostgreSQL SCRAM-SHA-256 verifier for password
// (RFC 5802/7677, 4096 iterations, 16-byte random salt):
// SCRAM-SHA-256$<iterations>:<salt>$<StoredKey>:<ServerKey>. PostgreSQL stores
// a verifier given to ALTER ROLE ... PASSWORD as is. Passwords made by `make
// env` are ASCII, so SASLprep leaves them unchanged.
func scramVerifier(password string) (string, error) {
	const iterations = 4096
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	salted, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
	if err != nil {
		return "", err
	}
	clientKey := mac(salted, "Client Key")
	storedKey := sha256.Sum256(clientKey)
	serverKey := mac(salted, "Server Key")
	enc := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", iterations, enc(salt), enc(storedKey[:]), enc(serverKey)), nil
}

func mac(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}
