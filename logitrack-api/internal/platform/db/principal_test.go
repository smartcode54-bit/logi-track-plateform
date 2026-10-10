package db

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
)

type ptrPrincipal struct{ r RLS }

func (p *ptrPrincipal) RLS() RLS { return p.r }

// failBeginner fails the test when WithPrincipal gets as far as BEGIN.
type failBeginner struct{ t *testing.T }

func (b failBeginner) Begin(context.Context) (pgx.Tx, error) {
	b.t.Fatal("BEGIN reached")
	return nil, errors.New("unreachable")
}

// TestWithPrincipalRefusesNil: an untyped nil and a typed nil pointer (authz.PrincipalFrom on a route
// without RequireAuth) are both refused with ErrNoPrincipal before BEGIN, without calling RLS.
func TestWithPrincipalRefusesNil(t *testing.T) {
	ctx := context.Background()
	var typed *ptrPrincipal
	for name, p := range map[string]Principal{"untyped nil": nil, "typed nil pointer": typed} {
		err := WithPrincipal(ctx, failBeginner{t}, p, func(pgx.Tx) error { return nil })
		if !errors.Is(err, ErrNoPrincipal) {
			t.Errorf("%s: %v, want ErrNoPrincipal", name, err)
		}
	}
	if err := WithPrincipal(ctx, failBeginner{t}, &ptrPrincipal{RLS{Bypass: true}}, func(pgx.Tx) error { return nil }); !errors.Is(err, ErrBypassNeedsReadOnly) {
		t.Errorf("a writable bypass: %v, want ErrBypassNeedsReadOnly", err)
	}
}
