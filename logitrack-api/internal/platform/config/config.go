// Package config loads process configuration from environment variables.
//
// Values are never logged or returned in errors: failures name the variables only,
// and Describe reports each variable as "set" or "unset" (main spec §16.5).
package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/caarlos0/env/v11"
)

// AppEnvs are the allowed APP_ENV values (main spec §16.1). APP_ENV is also part of the Redis key
// prefix lt:{APP_ENV}: (R26), so the process configuration (internal/app) and the keyspace
// (internal/platform/cache) both check against this one list.
var AppEnvs = []string{"local", "dev", "prod"}

// ValidAppEnv reports whether s is one of AppEnvs.
func ValidAppEnv(s string) bool { return slices.Contains(AppEnvs, s) }

// Validator is implemented by configuration structs that need checks beyond
// presence and type, such as enumerations or cross-field rules.
type Validator interface {
	Validate() error
}

// Error lists every configuration problem found while loading, so an operator
// can fix all of them in one pass.
type Error struct {
	Missing []string // required variables that are unset or empty
	Invalid []string // "NAME: reason" entries; never contains a value
}

func (e *Error) Error() string {
	var parts []string
	if len(e.Missing) > 0 {
		parts = append(parts, "missing required environment variables: "+strings.Join(e.Missing, ", "))
	}
	if len(e.Invalid) > 0 {
		parts = append(parts, "invalid environment variables: "+strings.Join(e.Invalid, "; "))
	}
	return "config: " + strings.Join(parts, "; ")
}

// Load parses environment variables into a new T and runs its Validate method
// when it has one. All problems are reported together in an *Error.
func Load[T any]() (*T, error) {
	return LoadFrom[T](os.Environ())
}

// LoadFrom is Load with an explicit environment ("KEY=value" entries), for tests.
func LoadFrom[T any](environ []string) (*T, error) {
	cfg := new(T)
	cerr := &Error{}

	if err := env.ParseWithOptions(cfg, env.Options{Environment: toMap(environ)}); err != nil {
		var agg env.AggregateError
		if !errors.As(err, &agg) {
			return nil, fmt.Errorf("config: %w", err)
		}
		for _, e := range agg.Errors {
			var notSet env.VarIsNotSetError
			var empty env.EmptyVarError
			var parse env.ParseError
			switch {
			case errors.As(e, &notSet):
				cerr.Missing = append(cerr.Missing, notSet.Key)
			case errors.As(e, &empty):
				cerr.Missing = append(cerr.Missing, empty.Key)
			case errors.As(e, &parse):
				cerr.Invalid = append(cerr.Invalid, fmt.Sprintf("%s: cannot parse as %s", envName(cfg, parse.Name), parse.Type))
			default:
				cerr.Invalid = append(cerr.Invalid, sanitize(e.Error()))
			}
		}
	}

	if len(cerr.Missing) == 0 && len(cerr.Invalid) == 0 {
		if v, ok := any(cfg).(Validator); ok {
			if err := v.Validate(); err != nil {
				if verr, ok := errors.AsType[*Error](err); ok {
					cerr.Missing = append(cerr.Missing, verr.Missing...)
					cerr.Invalid = append(cerr.Invalid, verr.Invalid...)
				} else {
					cerr.Invalid = append(cerr.Invalid, err.Error())
				}
			}
		}
	}

	if len(cerr.Missing) > 0 || len(cerr.Invalid) > 0 {
		sort.Strings(cerr.Missing)
		cerr.Missing = dedupe(cerr.Missing)
		return nil, cerr
	}
	return cfg, nil
}

// Invalidf builds an "invalid variable" entry for Validate implementations.
// The message must describe the rule, never echo the value.
func Invalidf(name, format string, args ...any) string {
	return name + ": " + fmt.Sprintf(format, args...)
}

// Describe returns every environment variable declared on T with "set" or
// "unset", sorted by name. It never reads values beyond their presence.
func Describe[T any]() map[string]string {
	out := map[string]string{}
	for _, name := range names(reflect.TypeFor[T]()) {
		if v, ok := os.LookupEnv(name); ok && v != "" {
			out[name] = "set"
		} else {
			out[name] = "unset"
		}
	}
	return out
}

func names(t reflect.Type) []string {
	var out []string
	for f := range t.Fields() {
		if tag, ok := f.Tag.Lookup("env"); ok {
			if name, _, _ := strings.Cut(tag, ","); name != "" && name != "-" {
				out = append(out, name)
			}
			continue
		}
		if f.Type.Kind() == reflect.Struct {
			out = append(out, names(f.Type)...)
		}
	}
	sort.Strings(out)
	return out
}

// envName maps a Go field name from a ParseError back to its variable name.
func envName[T any](cfg *T, field string) string {
	var find func(reflect.Type) string
	find = func(t reflect.Type) string {
		for f := range t.Fields() {
			if f.Name == field {
				if name, _, _ := strings.Cut(f.Tag.Get("env"), ","); name != "" && name != "-" {
					return name
				}
			}
			if f.Type.Kind() == reflect.Struct && f.Tag.Get("env") == "" {
				if n := find(f.Type); n != "" {
					return n
				}
			}
		}
		return ""
	}
	if n := find(reflect.TypeOf(cfg).Elem()); n != "" {
		return n
	}
	return field
}

// sanitize drops anything after a colon-quoted value that the env library may
// include in a message, keeping only the leading description.
func sanitize(msg string) string {
	if before, _, ok := strings.Cut(msg, `"`); ok {
		return strings.TrimSpace(before)
	}
	return msg
}

func toMap(environ []string) map[string]string {
	m := make(map[string]string, len(environ))
	for _, kv := range environ {
		k, v, ok := strings.Cut(kv, "=")
		if ok {
			m[k] = v
		}
	}
	return m
}

func dedupe(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
