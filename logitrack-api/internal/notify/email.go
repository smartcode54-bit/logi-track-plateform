// Package notify holds the notification consumers of the worker (main spec §7.6): notify.email (T10),
// notify.fcm (T13, fcm.go, with the legacy push payloads in pushes.go); notify.line follows in T49.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/jobs"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify/notifydb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/db"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/email"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/inbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

// QueueEmail is the work queue of this consumer (Appendix B §B.5.3).
const QueueEmail = "notify.email"

// Routing keys bound to notify.email.
const (
	RouteResetRequested = "auth.password_reset_requested"
	RouteUserCreated    = "user.created"
	RouteUserInvited    = "user.invited"
	RouteJob            = "job.notify.email"
)

// LinkRequest is the payload of auth.password_reset_requested, user.created and user.invited, and the
// params of a notify.email job (Appendix C §C.4.9): who gets a link and for what. The address is read
// from users, never from the message.
type LinkRequest struct {
	UserID      uuid.UUID  `json:"userId"`
	Purpose     string     `json:"purpose,omitempty"` // reset | invite; defaults by routing key
	Locale      string     `json:"locale,omitempty"`  // th | en; default th
	RequestedIP string     `json:"requestedIp,omitempty"`
	RequestedBy *uuid.UUID `json:"requestedBy,omitempty"` // the inviting admin
	SendInvite  *bool      `json:"sendInvite,omitempty"`  // user.created: mail (an invite) only when true
}

// Email is the notify.email consumer: it creates the reset or invite token, mails the
// {PUBLIC_WEB_BASE_URL}/reset-password#token=... link in Thai or English, and commits only after the
// SMTP server accepted the mail (Appendix C §C.4.9). The token row and the consumer_inbox claim share
// that transaction: a crash after the send and before the commit sends a fresh link on the retry
// (duplicates are accepted, main spec §7.3). Passwords are never mailed (R29).
type Email struct {
	Pool       db.Beginner
	Sender     email.Sender
	Tokens     TokenIssuer
	WebBaseURL string // PUBLIC_WEB_BASE_URL
	ResetTTL   time.Duration
	Enabled    bool // EMAIL_ENABLED
	Log        zerolog.Logger
}

// Registration binds the consumer to its queue.
func (e *Email) Registration() mq.Registration {
	return mq.Registration{Queue: QueueEmail, Handler: e.Handle, Timeout: time.Minute}
}

// Handle implements mq.Handler.
func (e *Email) Handle(ctx context.Context, d *mq.Delivery) error {
	if d.RoutingKey == RouteJob {
		return e.handleJob(ctx, d)
	}
	var req LinkRequest
	if err := d.Decode(&req); err != nil {
		return err
	}
	switch d.RoutingKey {
	case RouteResetRequested:
		req.Purpose = orDefault(req.Purpose, PurposeReset)
	case RouteUserInvited:
		req.Purpose = orDefault(req.Purpose, PurposeInvite)
	case RouteUserCreated:
		// sendInvite alone decides (Appendix C §C.4.9): without it the admin hands over a temporary
		// password (R29), whatever else the payload carries.
		if req.SendInvite == nil || !*req.SendInvite {
			return nil
		}
		if req.Purpose != "" && req.Purpose != PurposeInvite {
			return mq.Permanent(fmt.Errorf("notify.email: user.created carries purpose %q; only invite is allowed", req.Purpose))
		}
		req.Purpose = PurposeInvite
	default:
		return mq.Permanent(fmt.Errorf("notify.email: unexpected routing key %s", d.RoutingKey))
	}
	_, err := e.sendLink(ctx, d, req, nil)
	return err
}

// handleJob is job.notify.email: the params are a LinkRequest, and the job row follows the send. A
// job whose command is dead-lettered after the last retry stays queued until a replay delivers it
// again (Appendix B §B.5.4): marking it failed would make the replayed command a no-op.
func (e *Email) handleJob(ctx context.Context, d *mq.Delivery) error {
	cmd, err := jobs.DecodeCommand(d)
	if err != nil {
		return err
	}
	var req LinkRequest
	if jerr := json.Unmarshal(cmd.Params, &req); jerr != nil {
		err = mq.Permanent(fmt.Errorf("notify.email: job params: %w", jerr))
	} else if !e.Enabled {
		// Nothing is sent, but the job ends: a row left queued would never finish.
		return db.WithSystem(ctx, e.Pool, d.TenantID, func(tx pgx.Tx) error {
			_, err := jobs.Succeed(ctx, tx, cmd.JobID, map[string]any{"sent": 0, "skipped": "EMAIL_ENABLED is false"})
			if errors.Is(err, jobs.ErrNotActive) {
				return nil // a duplicate of a command already closed
			}
			return err
		})
	} else {
		req.Purpose = orDefault(req.Purpose, PurposeReset)
		_, err = e.sendLink(ctx, d, req, &cmd.JobID)
	}
	if mq.IsPermanent(err) {
		if ferr := db.WithSystem(ctx, e.Pool, d.TenantID, func(tx pgx.Tx) error {
			_, ferr := jobs.Fail(ctx, tx, cmd.JobID, err.Error(), nil)
			if errors.Is(ferr, jobs.ErrNotActive) {
				return nil
			}
			return ferr
		}); ferr != nil {
			return ferr // retry so the job does not stay queued forever
		}
	}
	return err
}

// sendLink runs the side-effect transaction: inbox claim, recipient, token, mail, (job), commit.
func (e *Email) sendLink(ctx context.Context, d *mq.Delivery, req LinkRequest, jobID *uuid.UUID) (bool, error) {
	if req.UserID == uuid.Nil {
		return false, mq.Permanent(errors.New("notify.email: userId is required"))
	}
	if req.Purpose != PurposeReset && req.Purpose != PurposeInvite {
		return false, mq.Permanent(fmt.Errorf("notify.email: unknown purpose %q", req.Purpose))
	}
	log := zerolog.Ctx(ctx).With().Str("user_id", req.UserID.String()).Str("purpose", req.Purpose).Logger()
	if !e.Enabled {
		log.Info().Msg("EMAIL_ENABLED is false: no link created, nothing sent")
		return false, nil
	}
	sent, err := inbox.Run(ctx, e.Pool, d, func(tx pgx.Tx) error {
		if jobID != nil {
			if _, err := jobs.Start(ctx, tx, *jobID); err != nil {
				return mq.Permanent(err)
			}
		}
		u, err := notifydb.New(tx).GetRecipient(ctx, req.UserID)
		if errors.Is(err, pgx.ErrNoRows) {
			return mq.Permanent(errors.New("notify.email: user not found"))
		}
		if err != nil {
			return err
		}
		if u.Status == "disabled" || u.Status == "deleted" || u.Email == "" {
			return mq.Permanent(fmt.Errorf("notify.email: user is %s or has no email: skipped", u.Status))
		}
		token, err := e.Tokens.IssuePasswordResetToken(ctx, tx, u.ID, req.Purpose, req.RequestedIP, req.RequestedBy)
		if err != nil {
			return err
		}
		locale := LocaleTH
		if req.Locale == LocaleEN {
			locale = LocaleEN
		}
		ttl := e.ResetTTL
		if req.Purpose == PurposeInvite {
			ttl = InviteTTL
		}
		name := u.Email
		if u.DisplayName != nil && strings.TrimSpace(*u.DisplayName) != "" {
			name = strings.TrimSpace(*u.DisplayName)
		}
		msg, err := renderLink(req.Purpose, locale, u.Email, linkData{
			Name: name, Link: ResetLink(e.WebBaseURL, token), ValidFor: validFor(ttl, locale),
		})
		if err != nil {
			return mq.Permanent(err)
		}
		if err := e.Sender.Send(ctx, msg); err != nil {
			if email.IsPermanent(err) {
				return mq.Permanent(err)
			}
			return err
		}
		if jobID != nil {
			if _, err := jobs.Succeed(ctx, tx, *jobID, map[string]any{"sent": 1}); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil && sent {
		log.Info().Msg("link mailed")
	}
	return sent, err
}

// ResetLink is {PUBLIC_WEB_BASE_URL}/reset-password#token=<token>: the token rides in the fragment, so
// it never reaches a server log or a Referer header (Appendix C §C.4.9).
func ResetLink(base, token string) string {
	return strings.TrimRight(base, "/") + "/reset-password#token=" + token
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
