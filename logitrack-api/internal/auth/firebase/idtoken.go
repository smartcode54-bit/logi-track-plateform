package firebase

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Operating rules of the verifier (code constants).
const (
	httpTimeout = 10 * time.Second // one JWKS, token or Identity Toolkit request
	// clockSkew is the tolerance for iat and auth_time in the future, as the Admin SDK allows.
	clockSkew = 5 * time.Minute
)

// Reasons of an InvalidError (logged, never sent to the client, which only sees 401 invalid_token).
const (
	ReasonInvalid = "invalid" // malformed, bad signature, wrong issuer, audience or algorithm
	ReasonExpired = "expired" // exp passed
	ReasonClaims  = "claims"  // sub, iat or auth_time missing or impossible; a tenant token
)

// InvalidError is an ID token that was judged and rejected.
type InvalidError struct {
	Reason string
	Err    error
}

func (e *InvalidError) Error() string {
	if e.Err == nil {
		return "firebase: invalid ID token (" + e.Reason + ")"
	}
	return "firebase: invalid ID token (" + e.Reason + "): " + e.Err.Error()
}

func (e *InvalidError) Unwrap() error { return e.Err }

func invalid(reason string, err error) error { return &InvalidError{Reason: reason, Err: err} }

// IDToken is what a verified Firebase ID token says.
type IDToken struct {
	UID            string    // sub = the Firebase uid (users.legacy_auth_uid)
	AuthTime       time.Time // auth_time: when the user signed in to Firebase (a refresh keeps it)
	IssuedAt       time.Time
	Expiry         time.Time
	SignInProvider string // firebase.sign_in_provider: "password", "google.com", "custom", ...
	Email          string
}

// VerifierConfig configures a Verifier.
type VerifierConfig struct {
	// ProjectID is FIREBASE_PROJECT_ID: the audience, and the issuer suffix (never the ETL project
	// variable, R74). Required.
	ProjectID string
	// HTTPClient fetches the keys; nil uses a client with a 10 s timeout. Tests pass an in-process
	// transport (firebasetest).
	HTTPClient *http.Client
	// Now is the clock of the time checks; nil uses time.Now.
	Now func() time.Time
}

// Verifier checks Firebase ID tokens (Appendix C §C.6.2). It is safe for concurrent use and performs no
// I/O until the first token: the keys are fetched then, cached, and refetched when a token names an
// unknown kid.
type Verifier struct {
	projectID string
	now       func() time.Time
	verifier  *oidc.IDTokenVerifier
}

// NewVerifier builds a Verifier for the project.
func NewVerifier(cfg VerifierConfig) (*Verifier, error) {
	if !ValidProjectID(cfg.ProjectID) {
		return nil, errors.New("firebase: FIREBASE_PROJECT_ID is not a project id")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), client), SecureTokenJWKSURL)
	return &Verifier{
		projectID: cfg.ProjectID, now: now,
		verifier: oidc.NewVerifier(SecureTokenIssuerPrefix+cfg.ProjectID, probedKeySet{keys}, &oidc.Config{
			ClientID:             cfg.ProjectID,
			SupportedSigningAlgs: []string{oidc.RS256},
			Now:                  now,
		}),
	}, nil
}

// Verify checks raw: an RS256 signature by a current securetoken key, iss =
// https://securetoken.google.com/{project}, aud = the project, exp in the future (go-oidc), then sub
// (1-128 bytes), iat and auth_time not in the future beyond the clock skew, and no Identity Platform
// tenant (the project has none; a tenant token would name a uid of another namespace). A rejected token
// is an *InvalidError; a token that could not be judged because the keys could not be fetched wraps
// ErrUnavailable.
func (v *Verifier) Verify(ctx context.Context, raw string) (*IDToken, error) {
	p := &probe{}
	t, err := v.verifier.Verify(context.WithValue(ctx, probeKey{}, p), raw)
	if err != nil {
		if _, expired := errors.AsType[*oidc.TokenExpiredError](err); expired {
			return nil, invalid(ReasonExpired, err)
		}
		if p.fetchFailed {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		return nil, invalid(ReasonInvalid, err)
	}
	var c struct {
		AuthTime *int64 `json:"auth_time"`
		Email    string `json:"email"`
		Firebase struct {
			SignInProvider string `json:"sign_in_provider"`
			Tenant         string `json:"tenant"`
		} `json:"firebase"`
	}
	if err := t.Claims(&c); err != nil {
		return nil, invalid(ReasonClaims, err)
	}
	now := v.now()
	switch {
	case !validUID(t.Subject):
		return nil, invalid(ReasonClaims, errors.New("sub missing or longer than 128 bytes"))
	case t.IssuedAt.IsZero() || t.IssuedAt.After(now.Add(clockSkew)):
		return nil, invalid(ReasonClaims, errors.New("iat missing or in the future"))
	case c.AuthTime == nil || time.Unix(*c.AuthTime, 0).After(now.Add(clockSkew)):
		return nil, invalid(ReasonClaims, errors.New("auth_time missing or in the future"))
	case c.Firebase.Tenant != "":
		return nil, invalid(ReasonClaims, errors.New("token of an Identity Platform tenant"))
	}
	return &IDToken{
		UID: t.Subject, AuthTime: time.Unix(*c.AuthTime, 0).UTC(), IssuedAt: t.IssuedAt.UTC(), Expiry: t.Expiry.UTC(),
		SignInProvider: c.Firebase.SignInProvider, Email: strings.TrimSpace(c.Email),
	}, nil
}

// probe records, for one Verify call, whether the key set failed to fetch keys. go-oidc flattens the
// key set's error into text, so the key set reports it through the request context instead (as
// internal/auth/google does).
type probe struct{ fetchFailed bool }

type probeKey struct{}

// probedKeySet marks the probe when RemoteKeySet could not refresh its keys (network, HTTP status,
// undecodable document, cancelled request): that says nothing about the token. RemoteKeySet prefixes
// exactly those errors with "fetching keys" (go-oidc v3.21.0 jwks.go); the unit tests pin it.
type probedKeySet struct{ oidc.KeySet }

func (k probedKeySet) VerifySignature(ctx context.Context, jwt string) ([]byte, error) {
	payload, err := k.KeySet.VerifySignature(ctx, jwt)
	if err != nil && strings.HasPrefix(err.Error(), "fetching keys") {
		if p, ok := ctx.Value(probeKey{}).(*probe); ok {
			p.fetchFailed = true
		}
	}
	return payload, err
}
