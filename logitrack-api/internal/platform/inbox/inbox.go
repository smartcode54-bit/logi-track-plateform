// Package inbox makes consumers idempotent (main spec §7.2, Appendix B §B.5.3): the side-effect
// transaction of a delivery starts with an insert of (consumer, message_id) into consumer_inbox, so a
// redelivered or duplicated message commits nothing the second time.
package inbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/outbox/outboxdb"
)

// Claim inserts (consumer, messageID) in tx; false means another transaction already committed it.
// It must be the first statement of the transaction whose effects it guards.
func Claim(ctx context.Context, tx pgx.Tx, consumer, messageID string) (bool, error) {
	if consumer == "" || messageID == "" {
		return false, mq.Permanent(errors.New("inbox: consumer and message id are required"))
	}
	n, err := outboxdb.New(tx).ClaimInbox(ctx, outboxdb.ClaimInboxParams{Consumer: consumer, MessageID: messageID})
	if err != nil {
		return false, fmt.Errorf("inbox: claim: %w", err)
	}
	return n == 1, nil
}

// Run executes fn for d exactly once per consumer: in a system transaction (db.WithSystem with the
// event's tenant, R12) whose first statement is Claim. A duplicate commits nothing, skips fn and
// returns processed = false with a nil error, so the caller acks it. fn's error rolls everything
// back, the claim included, so the retry runs fn again.
func Run(ctx context.Context, b db.Beginner, d *mq.Delivery, fn func(tx pgx.Tx) error) (processed bool, err error) {
	err = db.WithSystem(ctx, b, d.TenantID, func(tx pgx.Tx) error {
		ok, err := Claim(ctx, tx, d.Queue, d.MessageID)
		if err != nil || !ok {
			return err
		}
		processed = true
		return fn(tx)
	})
	if err != nil {
		return false, err
	}
	return processed, nil
}
