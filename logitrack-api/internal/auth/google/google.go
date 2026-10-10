// Package google verifies the Google ID tokens of POST /v1/auth/google (main spec §4.2, Appendix C
// §C.4.10): the Google Identity Services token the web button hands to the BFF, and the id_token of the
// driver app's google_sign_in (serverClientId = dart-define GOOGLE_OIDC_CLIENT_ID). There is no
// authorization-code flow (R23): no client secret, no redirect URL, no token endpoint.
//
// Verification is github.com/coreos/go-oidc/v3 against the issuer https://accounts.google.com (go-oidc
// also accepts Google's "accounts.google.com" spelling of iss): an RS256 signature by a key of the JWKS
// named in the discovery document, iss, exp and nbf. go-oidc's client-id check takes a single id, so it
// is skipped and every aud value must instead be one of GOOGLE_OIDC_ALLOWED_CLIENT_IDS; email_verified
// must be true and sub and email present. The nonce is the caller's (it lives in Redis, single use), and
// so is the decision whether Google's word on the email is enough to link an account by it
// (Identity.EmailAuthoritative).
//
// Discovery and key fetches are lazy: the api starts while Google is unreachable, and a token whose key
// cannot be fetched is ErrUnavailable (503), never an InvalidError that blames the token. A failed
// discovery is retried at most every discoveryBackoff.
package google

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Issuer is Google's OpenID Connect issuer.
const Issuer = "https://accounts.google.com"

// Fixed operating rules (code constants, no environment name).
const (
	httpTimeout      = 10 * time.Second // one discovery or JWKS request
	discoveryBackoff = 10 * time.Second // pause after a failed discovery before the next attempt
	maxSubject       = 255
	maxEmail         = 320
)

// ErrUnavailable means Google's discovery document or key set could not be fetched: the token was not
// judged. The api answers 503 unavailable.
var ErrUnavailable = errors.New("google: identity provider unavailable")

// Reasons of an InvalidError (logged, never sent to the client, which only sees 401 invalid_token).
const (
	ReasonInvalid         = "invalid"          // malformed, bad signature, wrong issuer or algorithm
	ReasonExpired         = "expired"          // exp passed (or nbf ahead)
	ReasonAudience        = "audience"         // an aud value outside GOOGLE_OIDC_ALLOWED_CLIENT_IDS
	ReasonEmailUnverified = "email_unverified" // email_verified is not true
	ReasonClaims          = "claims"           // sub or email missing or unreadable
)

// InvalidError is an ID token that was judged and rejected.
type InvalidError struct {
	Reason string
	Err    error
}

func (e *InvalidError) Error() string {
	if e.Err == nil {
		return "google: invalid ID token (" + e.Reason + ")"
	}
	return "google: invalid ID token (" + e.Reason + "): " + e.Err.Error()
}

func (e *InvalidError) Unwrap() error { return e.Err }

func invalid(reason string, err error) error { return &InvalidError{Reason: reason, Err: err} }

// Identity is what a verified token says about the Google account.
type Identity struct {
	Subject  string   // sub: the stable Google account id (auth_identities.provider_subject)
	Email    string   // ASCII letters lower-cased (no Unicode case folding); Google verified it (email_verified == true)
	Nonce    string   // the nonce claim, empty when the sign-in carried none
	ClientID string   // the aud the token was issued for (the first one)
	Audience []string // every aud value, all in GOOGLE_OIDC_ALLOWED_CLIENT_IDS
	// AuthorizedParty is azp, the OAuth client that requested the token; empty when the token has none.
	AuthorizedParty string
	// HostedDomain is hd, lower-cased: the Google Workspace (or Cloud Identity) domain of the account;
	// empty for a consumer Google account.
	HostedDomain string
	Name         string
}

// NativeApp reports whether the token was requested by a client other than its audience: the driver
// app's google_sign_in asks for a token whose aud is the server client (serverClientId) while azp is
// the Android or iOS client (Android Credential Manager; GoogleSignIn-iOS sends audience=serverClientID).
// A Google Identity Services (web button) token has azp == aud. Google mints a token for another
// client's aud only inside one Cloud project (cross-client identity), and aud is already in the allow
// list, so azp needs no list of its own.
func (i *Identity) NativeApp() bool {
	return i.AuthorizedParty != "" && !slices.Contains(i.Audience, i.AuthorizedParty)
}

// gmailDomains are the consumer domains Google itself hosts (Gmail).
var gmailDomains = map[string]bool{"gmail.com": true, "googlemail.com": true}

// EmailAuthoritative reports whether Google is authoritative for Email, so that email_verified proves
// the account holder controls that mailbox now: a Gmail address, or an account of a Google Workspace
// (hd set; its admin verified the domain). For any other address email_verified only says the address
// was verified when the Google account was created, and the mailbox may have changed hands since
// (Google, "Authenticate with a backend server": email, email_verified and hd). An address with a
// non-ASCII character is never authoritative here: Google issues none for Gmail or Workspace users, and
// a case-insensitive match could fold such a character onto an ASCII one (U+212A KELVIN SIGN -> k).
func (i *Identity) EmailAuthoritative() bool {
	at := strings.LastIndexByte(i.Email, '@')
	if at < 1 || at == len(i.Email)-1 || !isASCII(i.Email) {
		return false
	}
	return i.HostedDomain != "" || gmailDomains[i.Email[at+1:]]
}

// Config configures a Verifier.
type Config struct {
	// ClientIDs are the accepted aud values (GOOGLE_OIDC_ALLOWED_CLIENT_IDS): the web GIS client and the
	// OAuth client the installed APKs pass as serverClientId. Required.
	ClientIDs []string
	// HTTPClient fetches discovery and keys; nil uses a client with a 10 s timeout. Tests pass an
	// in-process transport (googletest), so no test opens a socket.
	HTTPClient *http.Client
	// Now is the clock of the exp / nbf checks and the discovery backoff; nil uses time.Now.
	Now func() time.Time
}

// Verifier checks Google ID tokens. It is safe for concurrent use.
type Verifier struct {
	allowed map[string]bool
	client  *http.Client
	now     func() time.Time

	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier // built by the first successful discovery
	failedAt time.Time             // last failed discovery
}

// New builds a Verifier; it performs no I/O.
func New(cfg Config) (*Verifier, error) {
	v := &Verifier{allowed: map[string]bool{}, client: cfg.HTTPClient, now: cfg.Now}
	for _, id := range cfg.ClientIDs {
		if id = strings.TrimSpace(id); id != "" {
			v.allowed[id] = true
		}
	}
	if len(v.allowed) == 0 {
		return nil, errors.New("google: at least one allowed client id is required")
	}
	if v.client == nil {
		v.client = &http.Client{Timeout: httpTimeout}
	}
	if v.now == nil {
		v.now = time.Now
	}
	return v, nil
}

// Verify checks raw and returns the identity it carries. A rejected token is an *InvalidError; a token
// that could not be judged because Google was unreachable wraps ErrUnavailable.
func (v *Verifier) Verify(ctx context.Context, raw string) (*Identity, error) {
	iv, err := v.idTokenVerifier(ctx)
	if err != nil {
		return nil, err
	}
	p := &probe{}
	t, err := iv.Verify(context.WithValue(ctx, probeKey{}, p), raw)
	if err != nil {
		if _, expired := errors.AsType[*oidc.TokenExpiredError](err); expired {
			return nil, invalid(ReasonExpired, err)
		}
		if p.fetchFailed {
			return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
		}
		if strings.Contains(err.Error(), "before the nbf") {
			return nil, invalid(ReasonExpired, err)
		}
		return nil, invalid(ReasonInvalid, err)
	}
	if len(t.Audience) == 0 {
		return nil, invalid(ReasonAudience, errors.New("no aud"))
	}
	for _, aud := range t.Audience {
		if !v.allowed[aud] {
			return nil, invalid(ReasonAudience, nil) // the value is not logged: it may be any client's id
		}
	}
	var c struct {
		Email           string   `json:"email"`
		EmailVerified   flexBool `json:"email_verified"`
		Name            string   `json:"name"`
		AuthorizedParty string   `json:"azp"`
		HostedDomain    string   `json:"hd"`
	}
	if err := t.Claims(&c); err != nil {
		return nil, invalid(ReasonClaims, err)
	}
	if !c.EmailVerified {
		return nil, invalid(ReasonEmailUnverified, nil)
	}
	// Only ASCII letters are lower-cased: strings.ToLower would fold U+212A KELVIN SIGN onto "k" and let
	// a look-alike address match an ASCII one.
	email := asciiLower(strings.TrimSpace(c.Email))
	switch {
	case t.Subject == "" || len(t.Subject) > maxSubject:
		return nil, invalid(ReasonClaims, errors.New("sub missing or too long"))
	case email == "" || len(email) > maxEmail || !strings.Contains(email, "@"):
		return nil, invalid(ReasonClaims, errors.New("email missing or malformed"))
	}
	return &Identity{
		Subject: t.Subject, Email: email, Nonce: t.Nonce, ClientID: t.Audience[0],
		Audience: slices.Clone(t.Audience), AuthorizedParty: strings.TrimSpace(c.AuthorizedParty),
		HostedDomain: asciiLower(strings.TrimSpace(c.HostedDomain)), Name: c.Name,
	}, nil
}

// asciiLower lower-cases the ASCII letters of s and leaves every other byte as it is.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

func isASCII(s string) bool {
	for i := range len(s) {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// idTokenVerifier returns the go-oidc verifier, running discovery on first use. The keys come from the
// jwks_uri of the discovery document through a RemoteKeySet that caches them and refetches when a token
// names an unknown kid.
func (v *Verifier) idTokenVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.verifier != nil {
		return v.verifier, nil
	}
	if !v.failedAt.IsZero() && v.now().Sub(v.failedAt) < discoveryBackoff {
		return nil, fmt.Errorf("%w: discovery failed less than %s ago", ErrUnavailable, discoveryBackoff)
	}
	// The discovery request outlives a client that hangs up, so one cancelled sign-in does not cost the
	// next ones a backoff; it is bounded by its own timeout.
	dctx, cancel := context.WithTimeout(oidc.ClientContext(context.WithoutCancel(ctx), v.client), httpTimeout)
	defer cancel()
	p, err := oidc.NewProvider(dctx, Issuer)
	if err != nil {
		v.failedAt = v.now()
		return nil, fmt.Errorf("%w: discovery: %v", ErrUnavailable, err)
	}
	var meta struct {
		JWKSURL string `json:"jwks_uri"`
	}
	if err := p.Claims(&meta); err != nil || !strings.HasPrefix(meta.JWKSURL, "https://") {
		v.failedAt = v.now()
		return nil, fmt.Errorf("%w: discovery document has no https jwks_uri", ErrUnavailable)
	}
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), v.client), meta.JWKSURL)
	v.verifier = oidc.NewVerifier(Issuer, probedKeySet{keys}, &oidc.Config{
		SkipClientIDCheck:    true, // aud is checked against the whole allow list in Verify
		SupportedSigningAlgs: []string{oidc.RS256},
		Now:                  v.now,
	})
	return v.verifier, nil
}

// probe records, for one Verify call, whether the key set failed to fetch keys. go-oidc flattens the
// key set's error into text, so the key set reports it through the request context instead.
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

// flexBool reads email_verified as a JSON boolean or, as some Google tokens carry it, the string
// "true" / "false"; anything else is false.
type flexBool bool

func (b *flexBool) UnmarshalJSON(data []byte) error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	switch t := v.(type) {
	case bool:
		*b = flexBool(t)
	case string:
		*b = flexBool(strings.EqualFold(t, "true"))
	default:
		*b = false
	}
	return nil
}
