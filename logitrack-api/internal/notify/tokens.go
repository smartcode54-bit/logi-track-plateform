package notify

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth"
)

// InviteTTL is the life of an invite link (Appendix C §C.4.9: code constant, 72 h).
const InviteTTL = auth.InviteTTL

// TokenIssuer creates a reset or invite token inside the consumer's transaction and returns the
// secret to mail. *auth.Service implements it; the worker, which has no auth service, uses
// ResetTokens.
type TokenIssuer interface {
	IssuePasswordResetToken(ctx context.Context, tx pgx.Tx, userID uuid.UUID, purpose string,
		requestedIP string, requestedBy *uuid.UUID) (string, error)
}

// ResetTokens is the TokenIssuer of the worker: auth.IssueResetToken with PASSWORD_RESET_TTL, so the
// row is exactly what POST /v1/auth/password/reset looks up (Appendix C §C.4.9).
type ResetTokens struct {
	ResetTTL time.Duration // PASSWORD_RESET_TTL
	Now      func() time.Time
}

// IssuePasswordResetToken implements TokenIssuer.
func (r ResetTokens) IssuePasswordResetToken(ctx context.Context, tx pgx.Tx, userID uuid.UUID, purpose string,
	requestedIP string, requestedBy *uuid.UUID) (string, error) {
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	return auth.IssueResetToken(ctx, tx, now, r.ResetTTL, userID, purpose, requestedIP, requestedBy)
}

var _ TokenIssuer = (*auth.Service)(nil)
