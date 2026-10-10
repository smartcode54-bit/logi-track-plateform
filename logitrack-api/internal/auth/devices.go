package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/security"
)

// LegacyInstallPrefix starts the install id of a device_tokens row the ETL loaded from the 3.x token
// fields ('legacy:' + the first 16 hex of sha256(token), Appendix A §A.3.1). Clients cannot register
// one: the device's real install id replaces the row on its first PUT.
const LegacyInstallPrefix = "legacy:"

// Device platforms and flavors (device_tokens CHECK constraints).
var (
	DevicePlatforms = []string{"ios", "android", "web", "other"}
	DeviceFlavors   = []string{"dev", "prod"}
)

// Bounds of the device body.
const (
	maxInstallIDBytes = 200 // as installId at login (sessions.install_id must be able to equal it, R83)
	maxDeviceToken    = 4096
	maxAppVersion     = 64
)

// MaxDevicesPerUser is the most device_tokens rows a user keeps: a registration drops the least
// recently seen rows beyond it (notify.MaxDevicesPerDriver bounds the fan-out of a push the same way).
const MaxDevicesPerUser = 10

// Reasons of a refused registration.
const (
	// reasonInstallMismatch: the body's installId is not the install the caller's session signed in on.
	reasonInstallMismatch = "install_mismatch"
	// reasonTokenHeld (409 already_exists): another user's device holds the token and the caller's
	// session is not signed in on that install.
	reasonTokenHeld = "token_held_by_another_device"
)

// DeviceInput is the body of PUT /v1/me/devices (Appendix C §C.8): {installId, token, platform,
// appFlavor?}. AppVersion comes from the X-App-Version header (Appendix B §B.1.5).
type DeviceInput struct {
	InstallID string `json:"installId"`
	Token     string `json:"token"`
	Platform  string `json:"platform"`
	AppFlavor string `json:"appFlavor"`

	AppVersion string `json:"-"`
}

// RegisterDevice is PUT /v1/me/devices and its driver-app alias PUT /v1/mobile/me/devices: it upserts
// the caller's device_tokens row (user, installId) with the FCM token, the platform, the flavor and
// the caller's linked driver (R4), and keeps the user's MaxDevicesPerUser most recently seen rows.
//
// A session signed in on an install (a mobile login with installId, sessions.install_id) registers
// that install only (422 install_mismatch otherwise). The token moves to the caller in the same
// transaction from the caller's own other rows, and from another user's row only when that row is the
// install the caller's session is signed in on (a shared phone whose previous user did not log out),
// with a device_token_moved security event; any other holder keeps it (409 already_exists), so knowing
// someone's token is not enough to silence their pushes.
func (s *Service) RegisterDevice(ctx context.Context, p *authz.Principal, in DeviceInput) error {
	if v := validateDevice(&in); len(v) > 0 {
		return httpx.ErrInvalidArgument(v...)
	}
	var flavor, version *string
	if in.AppFlavor != "" {
		flavor = &in.AppFlavor
	}
	if in.AppVersion != "" {
		version = &in.AppVersion
	}
	upsert := func() error {
		return s.systemTx(ctx, func(tx pgx.Tx, q *authdb.Queries) error {
			sessionInstall, err := s.sessionInstall(ctx, q, p)
			if err != nil {
				return err
			}
			if sessionInstall != nil && *sessionInstall != in.InstallID {
				return httpx.ErrInvalidArgument(httpx.FieldViolation{Field: "installId", Reason: reasonInstallMismatch})
			}
			if err := q.LockDeviceToken(ctx, in.Token); err != nil {
				return err
			}
			released, err := q.ReleaseDeviceToken(ctx, authdb.ReleaseDeviceTokenParams{
				Token: in.Token, UserID: p.UserID, InstallID: in.InstallID, SessionInstall: sessionInstall,
			})
			if err != nil {
				return err
			}
			held, err := q.TokenHeldElsewhere(ctx, authdb.TokenHeldElsewhereParams{Token: in.Token, UserID: p.UserID})
			if err != nil {
				return err
			}
			if held {
				return httpx.NewError(http.StatusConflict, CodeAlreadyExists, "another device holds this push token").
					WithDetails(map[string]any{"reason": reasonTokenHeld})
			}
			now := s.clock()
			for _, r := range released {
				if r.UserID == p.UserID {
					continue
				}
				if err := security.Append(ctx, tx, security.Event{
					EventType: "device_token_moved", Severity: security.SeverityWarning,
					Summary:     "a push token moved to another user signed in on the same install",
					Details:     map[string]any{"installId": r.InstallID, "sessionId": p.SessionID, "legacy": r.LegacySource != nil},
					ActorUserID: &p.UserID, TargetUserID: &r.UserID, TenantID: p.TenantID, OccurredAt: now,
				}); err != nil {
					return err
				}
			}
			if err := q.UpsertDeviceToken(ctx, authdb.UpsertDeviceTokenParams{
				UserID: p.UserID, InstallID: in.InstallID, Token: in.Token, Platform: in.Platform,
				AppFlavor: flavor, AppVersion: version, At: now,
			}); err != nil {
				return err
			}
			_, err = q.PruneUserDevices(ctx, authdb.PruneUserDevicesParams{UserID: p.UserID, InstallID: in.InstallID, Keep: MaxDevicesPerUser - 1})
			return err
		})
	}
	// Registrations of one token are serialised by LockDeviceToken; a conflict that still slips through
	// (another token's registration deleting this row, a deadlock with an unrelated writer) is retried.
	var err error
	for range 3 {
		if err = upsert(); !retryableDeviceWrite(err) {
			return err
		}
	}
	return err
}

// sessionInstall is the install the caller's session signed in on, nil for a session without one (web,
// a mobile login without installId) and for a principal without a session.
func (s *Service) sessionInstall(ctx context.Context, q *authdb.Queries, p *authz.Principal) (*string, error) {
	if p.SessionID == uuid.Nil {
		return nil, nil
	}
	sess, err := q.GetSession(ctx, p.SessionID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		return nil, httpx.ErrUnauthenticated() // the session vanished (clean-up) since the token was checked
	case err != nil:
		return nil, err
	case sess.UserID != p.UserID:
		return nil, httpx.ErrUnauthenticated()
	}
	return sess.InstallID, nil
}

// UnregisterDevice is DELETE /v1/me/devices/{installId}: the caller's row of that install goes (204
// whether or not it existed).
func (s *Service) UnregisterDevice(ctx context.Context, p *authz.Principal, installID string) error {
	if installID == "" || len(installID) > maxInstallIDBytes || !utf8.ValidString(installID) {
		return httpx.ErrNotFound()
	}
	return s.system(ctx, func(q *authdb.Queries) error {
		return q.DeleteDeviceToken(ctx, authdb.DeleteDeviceTokenParams{UserID: p.UserID, InstallID: installID})
	})
}

func validateDevice(in *DeviceInput) []httpx.FieldViolation {
	var v []httpx.FieldViolation
	switch {
	case in.InstallID == "":
		v = append(v, httpx.FieldViolation{Field: "installId", Reason: "required"})
	case len(in.InstallID) > maxInstallIDBytes || !utf8.ValidString(in.InstallID) || strings.IndexFunc(in.InstallID, unicode.IsControl) >= 0:
		v = append(v, httpx.FieldViolation{Field: "installId", Reason: "too_long"})
	case strings.HasPrefix(in.InstallID, LegacyInstallPrefix):
		v = append(v, httpx.FieldViolation{Field: "installId", Reason: "reserved"})
	}
	switch {
	case in.Token == "":
		v = append(v, httpx.FieldViolation{Field: "token", Reason: "required"})
	case len(in.Token) > maxDeviceToken || !printableASCII(in.Token):
		v = append(v, httpx.FieldViolation{Field: "token", Reason: "malformed"})
	}
	if !slices.Contains(DevicePlatforms, in.Platform) {
		v = append(v, httpx.FieldViolation{Field: "platform", Reason: "one_of", Params: map[string]any{"values": DevicePlatforms}})
	}
	if in.AppFlavor != "" && !slices.Contains(DeviceFlavors, in.AppFlavor) {
		v = append(v, httpx.FieldViolation{Field: "appFlavor", Reason: "one_of", Params: map[string]any{"values": DeviceFlavors}})
	}
	// The header is informational: a value that does not fit is dropped rather than refused.
	if len(in.AppVersion) > maxAppVersion || !utf8.ValidString(in.AppVersion) || strings.IndexFunc(in.AppVersion, unicode.IsControl) >= 0 {
		in.AppVersion = ""
	}
	return v
}

// printableASCII accepts the characters an FCM registration token is made of (and any other visible
// ASCII), refusing spaces, control characters and non-ASCII.
func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] > '~' {
			return false
		}
	}
	return true
}

// retryableDeviceWrite is a unique violation on device_tokens, a deadlock or a serialization failure.
func retryableDeviceWrite(err error) bool {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return false
	}
	switch pe.Code {
	case "40P01", "40001":
		return true
	case "23505":
		return pe.TableName == "device_tokens"
	}
	return false
}

// MountDevices registers PUT /devices and DELETE /devices/{installId} on a router whose group already
// requires authentication: /v1/me here, and /v1/mobile/me when the driver-app group lands (T55, P7a,
// R42; same handlers).
func (s *Service) MountDevices(r fiber.Router) {
	r.Put("/devices", s.handlePutDevice)
	r.Delete("/devices/:installId", s.handleDeleteDevice)
}

func (s *Service) handlePutDevice(c fiber.Ctx) error {
	var in DeviceInput
	if err := httpx.DecodeJSON(c, &in); err != nil {
		return err
	}
	in.AppVersion = c.Get("X-App-Version")
	if err := s.RegisterDevice(c.Context(), PrincipalFrom(c), in); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}

func (s *Service) handleDeleteDevice(c fiber.Ctx) error {
	id, err := url.PathUnescape(c.Params("installId"))
	if err != nil {
		return httpx.ErrNotFound()
	}
	if err := s.UnregisterDevice(c.Context(), PrincipalFrom(c), id); err != nil {
		return err
	}
	return c.SendStatus(http.StatusNoContent)
}
