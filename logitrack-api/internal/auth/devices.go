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
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/authz"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/httpx"
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
// the caller's linked driver (R4). A token already held by another (user, install) moves to this row
// in the same transaction, so pushes for the previous holder never reach the phone again.
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
		return s.system(ctx, func(q *authdb.Queries) error {
			if err := q.LockDeviceToken(ctx, in.Token); err != nil {
				return err
			}
			if _, err := q.ReleaseDeviceToken(ctx, authdb.ReleaseDeviceTokenParams{Token: in.Token, UserID: p.UserID, InstallID: in.InstallID}); err != nil {
				return err
			}
			return q.UpsertDeviceToken(ctx, authdb.UpsertDeviceTokenParams{
				UserID: p.UserID, InstallID: in.InstallID, Token: in.Token, Platform: in.Platform,
				AppFlavor: flavor, AppVersion: version, At: s.clock(),
			})
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
