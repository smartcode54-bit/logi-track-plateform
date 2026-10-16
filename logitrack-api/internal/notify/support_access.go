package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/notify/notifydb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/email"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/inbox"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/mq"
)

const RouteSupportAdminAction = "tenant.support_admin_action"

type SupportAdminAction struct {
	TenantID         uuid.UUID   `json:"tenantId"`
	ActorUserID      uuid.UUID   `json:"actorUserId"`
	TargetUserID     uuid.UUID   `json:"targetUserId"`
	Action           string      `json:"action"`
	Basis            string      `json:"basis"`
	Locale           string      `json:"locale,omitempty"`
	RecipientUserIDs []uuid.UUID `json:"recipientUserIds"`
}

func (e *Email) supportAdminAction(ctx context.Context, d *mq.Delivery) error {
	var req SupportAdminAction
	if err := d.Decode(&req); err != nil {
		return err
	}
	if req.TenantID == uuid.Nil || req.ActorUserID == uuid.Nil || req.TargetUserID == uuid.Nil || d.TenantID == nil || *d.TenantID != req.TenantID {
		return mq.Permanent(errors.New("notify.email: invalid support action"))
	}
	if !e.Enabled {
		return nil
	}
	recipients := slices.Clone(req.RecipientUserIDs)
	slices.SortFunc(recipients, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	recipients = slices.Compact(recipients)
	for _, id := range recipients {
		if id == uuid.Nil {
			return mq.Permanent(errors.New("notify.email: invalid recipient"))
		}
		// Each pre-operation recipient gets its own durable delivery claim. Disabled admins still receive it.
		copy := *d
		copy.MessageID = d.MessageID + ":" + id.String()
		_, err := inbox.Run(ctx, e.Pool, &copy, func(tx pgx.Tx) error {
			u, err := notifydb.New(tx).GetRecipient(ctx, id)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if u.Email == "" {
				return nil
			}
			var company string
			if err := tx.QueryRow(ctx, `SELECT name_th FROM tenants WHERE id=$1`, req.TenantID).Scan(&company); err != nil {
				return err
			}
			msg, err := supportActionMail(u.Email, req.Locale, company, req)
			if err != nil {
				return mq.Permanent(err)
			}
			if err := e.Sender.Send(ctx, msg); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

type supportActionCopy struct {
	Actions map[string]string `json:"actions"`
	Bases   map[string]string `json:"bases"`
}

var supportActionCopies = func() map[string]supportActionCopy {
	out := map[string]supportActionCopy{}
	for _, locale := range []string{LocaleTH, LocaleEN} {
		b, err := templateFS.ReadFile("templates/support_admin_action." + locale + ".json")
		if err != nil {
			panic(err)
		}
		var c supportActionCopy
		if err := json.Unmarshal(b, &c); err != nil {
			panic(err)
		}
		out[locale] = c
	}
	return out
}()

func supportActionMail(to, locale, company string, req SupportAdminAction) (email.Message, error) {
	if locale != LocaleEN {
		locale = LocaleTH
	}
	copy := supportActionCopies[locale]
	action, ok := copy.Actions[req.Action]
	basis, bok := copy.Bases[req.Basis]
	if !ok || !bok {
		return email.Message{}, fmt.Errorf("notify.email: invalid support action or basis")
	}
	company = strings.Join(strings.Fields(company), " ")
	data := struct{ Company, Action, Basis, Actor, Target, Tenant string }{company, action, basis, req.ActorUserID.String(), req.TargetUserID.String(), req.TenantID.String()}
	tpl := templates["support_admin_action."+locale]
	var subject, body, html bytes.Buffer
	if err := tpl.text.ExecuteTemplate(&subject, "subject", data); err != nil {
		return email.Message{}, err
	}
	if err := tpl.text.ExecuteTemplate(&body, "text", data); err != nil {
		return email.Message{}, err
	}
	if err := tpl.html.ExecuteTemplate(&html, "html", data); err != nil {
		return email.Message{}, err
	}
	return email.Message{To: to, Subject: strings.TrimSpace(subject.String()), Text: body.String(), HTML: html.String()}, nil
}
