package notify

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

// countingBeginner records Begin calls and starts nothing: the first statement of a send is the
// inbox claim, so a Begin means the consumer was about to create a token and mail it.
type countingBeginner struct{ n int }

var errNoDatabase = errors.New("test: no database")

func (b *countingBeginner) Begin(context.Context) (pgx.Tx, error) {
	b.n++
	return nil, errNoDatabase
}

// user.created mails only with sendInvite: true (Appendix B §B.5.3, Appendix C §C.4.9), and then only
// an invite: a purpose never turns a creation into a mail.
func TestUserCreatedMailsOnlyWithSendInvite(t *testing.T) {
	uid := uuid.New()
	for _, c := range []struct {
		name    string
		payload map[string]any
		begins  int
		perm    bool
	}{
		{"no sendInvite", map[string]any{"userId": uid}, 0, false},
		{"sendInvite false", map[string]any{"userId": uid, "sendInvite": false}, 0, false},
		{"invite purpose, sendInvite false", map[string]any{"userId": uid, "purpose": "invite", "sendInvite": false}, 0, false},
		{"invite purpose, no sendInvite", map[string]any{"userId": uid, "purpose": "invite"}, 0, false},
		{"reset purpose, no sendInvite", map[string]any{"userId": uid, "purpose": "reset"}, 0, false},
		{"reset purpose, sendInvite true", map[string]any{"userId": uid, "purpose": "reset", "sendInvite": true}, 0, true},
		{"sendInvite true", map[string]any{"userId": uid, "sendInvite": true}, 1, false},
		{"invite purpose, sendInvite true", map[string]any{"userId": uid, "purpose": "invite", "sendInvite": true}, 1, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			b := &countingBeginner{}
			e := &Email{Pool: b, Enabled: true, Log: zerolog.Nop()}
			body, _ := json.Marshal(c.payload)
			err := e.Handle(context.Background(), &mq.Delivery{Queue: QueueEmail, MessageID: "1", RoutingKey: RouteUserCreated, Body: body})
			if b.n != c.begins {
				t.Fatalf("Begin called %d times, want %d (err %v)", b.n, c.begins, err)
			}
			switch {
			case c.perm && !mq.IsPermanent(err):
				t.Fatalf("want a permanent error, got %v", err)
			case !c.perm && c.begins == 0 && err != nil:
				t.Fatalf("a skipped creation returned %v", err)
			}
		})
	}
}
