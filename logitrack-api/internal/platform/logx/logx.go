// Package logx builds the process logger: zerolog JSON (or console in local
// development) behind a writer that redacts sensitive keys on every line.
package logx

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// Redacted replaces the value of every sensitive key.
const Redacted = "[REDACTED]"

// sensitiveExact are key names (compared case-insensitively, ignoring '-' and
// '_') whose values are never written (main spec §16.5, Appendix B).
var sensitiveExact = map[string]bool{
	"authorization":  true,
	"cookie":         true,
	"setcookie":      true,
	"xapikey":        true,
	"apikey":         true,
	"secret":         true,
	"clientsecret":   true,
	"idcard":         true,
	"idcardnumber":   true,
	"trucklicenseid": true,
}

// isSensitive reports whether a JSON key must be redacted. Besides the exact
// list, any key containing "password" or "secret" and any key ending in
// "token" (accessToken, refreshToken, idToken, token) is redacted.
func isSensitive(key string) bool {
	k := normalize(key)
	if sensitiveExact[k] {
		return true
	}
	return strings.Contains(k, "password") || strings.Contains(k, "secret") || strings.HasSuffix(k, "token")
}

func normalize(key string) string {
	k := strings.ToLower(key)
	return strings.NewReplacer("_", "", "-", "").Replace(k)
}

// probes are cheap substring checks run on the line, lower-cased and stripped
// of '_' and '-' exactly like isSensitive normalises keys, before any JSON
// decoding; a line that contains none of them is passed through untouched.
var probes = [][]byte{
	[]byte("authorization"), []byte("cookie"), []byte("apikey"), []byte("secret"),
	[]byte("password"), []byte("token"), []byte("idcard"), []byte("trucklicenseid"),
}

// RedactingWriter rewrites zerolog JSON lines so sensitive values never reach
// the underlying writer. Lines that are not JSON objects pass through.
type RedactingWriter struct {
	mu  sync.Mutex
	out io.Writer
}

// NewRedactingWriter wraps out.
func NewRedactingWriter(out io.Writer) *RedactingWriter { return &RedactingWriter{out: out} }

func (w *RedactingWriter) Write(p []byte) (int, error) {
	line := p
	if mayContainSensitive(p) {
		if red, ok := redactLine(p); ok {
			line = red
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.out.Write(line); err != nil {
		return 0, err
	}
	return len(p), nil
}

func mayContainSensitive(p []byte) bool {
	norm := bytes.ToLower(p)
	norm = bytes.ReplaceAll(norm, []byte("_"), nil)
	norm = bytes.ReplaceAll(norm, []byte("-"), nil)
	for _, probe := range probes {
		if bytes.Contains(norm, probe) {
			return true
		}
	}
	return false
}

// redactLine decodes one JSON object, redacts it and re-encodes it with a
// trailing newline. Key order is not preserved on redacted lines.
func redactLine(p []byte) ([]byte, bool) {
	dec := json.NewDecoder(bytes.NewReader(p))
	dec.UseNumber()
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, false
	}
	changed := redactValue(obj)
	if !changed {
		return nil, false
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(obj); err != nil {
		return nil, false
	}
	return buf.Bytes(), true
}

// redactValue walks maps and slices in place and reports whether anything was
// replaced. String values that hold an embedded JSON object are left alone.
func redactValue(v any) bool {
	changed := false
	switch t := v.(type) {
	case map[string]any:
		for k, val := range t {
			if isSensitive(k) {
				if s, ok := val.(string); !ok || s != Redacted {
					t[k] = Redacted
					changed = true
				}
				continue
			}
			if redactValue(val) {
				changed = true
			}
		}
	case []any:
		for _, val := range t {
			if redactValue(val) {
				changed = true
			}
		}
	}
	return changed
}

// Options configure New.
type Options struct {
	Level   string // trace|debug|info|warn|error
	Format  string // json|console
	Service string // process name: api, worker, ...
	Env     string // APP_ENV
	Version string
	Out     io.Writer // defaults to os.Stdout
}

// New returns the process logger. Every line is redacted before it is written.
func New(o Options) (zerolog.Logger, error) {
	lvl, err := zerolog.ParseLevel(strings.ToLower(o.Level))
	if err != nil || o.Level == "" {
		return zerolog.Nop(), fmt.Errorf("logx: unknown log level %q", o.Level)
	}
	out := o.Out
	if out == nil {
		out = os.Stdout
	}
	var sink io.Writer
	switch o.Format {
	case "json", "":
		sink = NewRedactingWriter(out)
	case "console":
		sink = NewRedactingWriter(zerolog.ConsoleWriter{Out: out, TimeFormat: time.RFC3339})
	default:
		return zerolog.Nop(), fmt.Errorf("logx: unknown log format %q", o.Format)
	}
	zerolog.TimeFieldFormat = time.RFC3339Nano
	l := zerolog.New(sink).Level(lvl).With().Timestamp().
		Str("service", o.Service).Str("env", o.Env).Str("version", o.Version).Logger()
	return l, nil
}

// FromContext returns the request-scoped logger stored by the HTTP middleware,
// or the global disabled logger when none is present.
func FromContext(ctx context.Context) *zerolog.Logger { return zerolog.Ctx(ctx) }
