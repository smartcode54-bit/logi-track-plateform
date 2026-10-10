package firebase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/jwt"
)

// maxResponseBytes bounds an Identity Toolkit response body.
const maxResponseBytes = 1 << 20

// Sentinel errors of Accounts; an *APIError matches them with errors.Is.
var (
	// ErrUserNotFound is Google's USER_NOT_FOUND: no Firebase account has the uid.
	ErrUserNotFound = errors.New("firebase: no account with this uid")
	// ErrUIDExists is DUPLICATE_LOCAL_ID: an account with the uid already exists.
	ErrUIDExists = errors.New("firebase: an account with this uid exists")
	// ErrEmailExists is EMAIL_EXISTS / DUPLICATE_EMAIL: another account holds the email.
	ErrEmailExists = errors.New("firebase: another account holds this email")
)

// APIError is an Identity Toolkit call Google answered with an error. It keeps the HTTP status and
// Google's error code (the part of error.message before ':'), never the message detail, which may echo
// request data.
type APIError struct {
	Method string // "lookup", "update", "create", "delete"
	Status int
	Code   string // USER_NOT_FOUND, INVALID_PASSWORD, ...; "" when the body had none
}

func (e *APIError) Error() string {
	code := e.Code
	if code == "" {
		code = "no error code"
	}
	return fmt.Sprintf("firebase: identity toolkit %s: HTTP %d (%s)", e.Method, e.Status, code)
}

// Is maps Google's codes onto the sentinels; a 429 or 5xx is ErrUnavailable (nothing was judged).
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrUserNotFound:
		return e.Code == "USER_NOT_FOUND"
	case ErrUIDExists:
		return e.Code == "DUPLICATE_LOCAL_ID"
	case ErrEmailExists:
		return e.Code == "EMAIL_EXISTS" || e.Code == "DUPLICATE_EMAIL"
	case ErrUnavailable:
		return e.Status == http.StatusTooManyRequests || e.Status >= 500
	}
	return false
}

// AccountsConfig configures Accounts.
type AccountsConfig struct {
	// ProjectID is FIREBASE_PROJECT_ID: the project whose accounts are written. Required.
	ProjectID string
	// Credentials sign the OAuth2 assertion (GOOGLE_APPLICATION_CREDENTIALS). Required.
	Credentials *ServiceAccount
	// HTTPClient carries the token and API requests; nil uses a client with a 10 s timeout. Tests pass an
	// in-process transport (firebasetest).
	HTTPClient *http.Client
}

// Accounts writes Firebase Auth accounts through the Identity Toolkit v1 REST API (Appendix C §C.6.4).
// It is safe for concurrent use; the access token is fetched on first use and reused until it expires.
type Accounts struct {
	base   string // IdentityToolkitURL/projects/{project}
	client *http.Client
	tokens oauth2.TokenSource
}

// NewAccounts builds the client; it performs no I/O.
func NewAccounts(cfg AccountsConfig) (*Accounts, error) {
	if !ValidProjectID(cfg.ProjectID) {
		return nil, errors.New("firebase: FIREBASE_PROJECT_ID is not a project id")
	}
	if cfg.Credentials == nil {
		return nil, errors.New("firebase: a service account is required")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	}
	jc := &jwt.Config{
		Email: cfg.Credentials.clientEmail, PrivateKey: cfg.Credentials.keyPEM, PrivateKeyID: cfg.Credentials.privateKeyID,
		Scopes: Scopes, TokenURL: cfg.Credentials.tokenURL,
	}
	return &Accounts{
		base:   IdentityToolkitURL + "/projects/" + cfg.ProjectID,
		client: client,
		tokens: jc.TokenSource(context.WithValue(context.Background(), oauth2.HTTPClient, client)),
	}, nil
}

// Account is the part of a Firebase account the bridge reads. The lookup response also carries the
// password hash and salt; they are never decoded.
type Account struct {
	UID              string
	Email            string
	Disabled         bool
	CustomAttributes string    // JSON object as a string; "" when the account has none
	ValidSince       time.Time // refresh tokens issued before it are revoked; zero when never set
}

// Lookup reads the account of uid (accounts:lookup). An unknown uid is ErrUserNotFound.
func (a *Accounts) Lookup(ctx context.Context, uid string) (*Account, error) {
	if !validUID(uid) {
		return nil, ErrUserNotFound
	}
	return a.lookup(ctx, map[string]any{"localId": []string{uid}}, func(acc *Account) bool { return acc.UID == uid })
}

// LookupEmail reads the account that holds email (accounts:lookup by email, as the Admin SDK's
// GetUserByEmail). Firebase keeps emails lower-cased and unique per project; no holder is
// ErrUserNotFound.
func (a *Accounts) LookupEmail(ctx context.Context, email string) (*Account, error) {
	if email == "" {
		return nil, ErrUserNotFound
	}
	return a.lookup(ctx, map[string]any{"email": []string{email}}, func(acc *Account) bool {
		return strings.EqualFold(acc.Email, email)
	})
}

func (a *Accounts) lookup(ctx context.Context, body map[string]any, match func(*Account) bool) (*Account, error) {
	var out struct {
		Users []struct {
			LocalID          string `json:"localId"`
			Email            string `json:"email"`
			Disabled         bool   `json:"disabled"`
			CustomAttributes string `json:"customAttributes"`
			ValidSince       string `json:"validSince"`
		} `json:"users"`
	}
	if err := a.post(ctx, "lookup", "/accounts:lookup", body, &out); err != nil {
		return nil, err
	}
	for _, u := range out.Users {
		acc := &Account{UID: u.LocalID, Email: u.Email, Disabled: u.Disabled, CustomAttributes: u.CustomAttributes}
		if !match(acc) {
			continue
		}
		if s, err := strconv.ParseInt(u.ValidSince, 10, 64); err == nil && s > 0 {
			acc.ValidSince = time.Unix(s, 0).UTC()
		}
		return acc, nil
	}
	return nil, ErrUserNotFound
}

// Update is one accounts:update call; nil fields are left as they are.
type Update struct {
	Password *string // a new password (plaintext, only in memory for the call)
	Disabled *bool   // disableUser
	// RevokeBefore sets validSince: every refresh token issued before it stops working (whole seconds).
	RevokeBefore *time.Time
	// CustomAttributes replaces the account's custom claims (a JSON object, at most MaxClaimsLength).
	CustomAttributes *string
	// Email replaces the account's email (PATCH /v1/users/{id}, T19); another account holding it is
	// ErrEmailExists.
	Email *string
}

// Update writes u to the account of uid (accounts:update). An unknown uid is ErrUserNotFound.
func (a *Accounts) Update(ctx context.Context, uid string, u Update) error {
	if !validUID(uid) {
		return ErrUserNotFound
	}
	body := map[string]any{"localId": uid}
	if u.Password != nil {
		body["password"] = *u.Password
	}
	if u.Disabled != nil {
		body["disableUser"] = *u.Disabled
	}
	if u.Email != nil {
		body["email"] = *u.Email
	}
	if u.RevokeBefore != nil {
		body["validSince"] = strconv.FormatInt(u.RevokeBefore.Unix(), 10)
	}
	if u.CustomAttributes != nil {
		if err := checkAttributes(*u.CustomAttributes); err != nil {
			return err
		}
		body["customAttributes"] = *u.CustomAttributes
	}
	if len(body) == 1 {
		return nil // nothing to write
	}
	return a.post(ctx, "update", "/accounts:update", body, nil)
}

// NewAccount is an account to create.
type NewAccount struct {
	UID           string
	Email         string // "" for none
	EmailVerified bool
	Password      string // "" for none (a Google-only or invited user)
	DisplayName   string
	Disabled      bool
}

// Create creates an account (POST /accounts). An existing uid is ErrUIDExists, an email another
// account holds ErrEmailExists.
func (a *Accounts) Create(ctx context.Context, n NewAccount) error {
	if !validUID(n.UID) {
		return fmt.Errorf("firebase: uid must be 1 to %d bytes", MaxUIDLength)
	}
	body := map[string]any{"localId": n.UID}
	if n.Email != "" {
		body["email"] = n.Email
		body["emailVerified"] = n.EmailVerified
	}
	if n.Password != "" {
		body["password"] = n.Password
	}
	if n.DisplayName != "" {
		body["displayName"] = n.DisplayName
	}
	if n.Disabled {
		body["disabled"] = true
	}
	return a.post(ctx, "create", "/accounts", body, nil)
}

// Delete deletes the account of uid (accounts:delete). An unknown uid is ErrUserNotFound.
func (a *Accounts) Delete(ctx context.Context, uid string) error {
	if !validUID(uid) {
		return ErrUserNotFound
	}
	return a.post(ctx, "delete", "/accounts:delete", map[string]any{"localId": uid}, nil)
}

func checkAttributes(s string) error {
	if len(s) > MaxClaimsLength {
		return fmt.Errorf("firebase: custom attributes exceed %d bytes", MaxClaimsLength)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil || m == nil {
		return errors.New("firebase: custom attributes must be a JSON object")
	}
	for k := range m {
		for _, r := range ReservedClaims {
			if k == r {
				return fmt.Errorf("firebase: custom attribute %q is reserved", k)
			}
		}
	}
	return nil
}

// post sends one Identity Toolkit request. A transport or token failure wraps ErrUnavailable; an error
// answer is an *APIError. Neither carries the request body.
func (a *Accounts) post(ctx context.Context, method, path string, body, out any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("firebase: identity toolkit %s: encode request: %w", method, err)
	}
	tok, err := a.tokens.Token()
	if err != nil {
		return fmt.Errorf("%w: identity toolkit %s: access token: %s", ErrUnavailable, method, tokenErrorText(err))
	}
	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.base+path, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("firebase: identity toolkit %s: %w", method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	tok.SetAuthHeader(req)
	res, err := a.client.Do(req)
	if err != nil {
		if ctx.Err() != nil && errors.Is(err, context.Canceled) {
			return fmt.Errorf("firebase: identity toolkit %s: %w", method, context.Canceled)
		}
		return fmt.Errorf("%w: identity toolkit %s: %s", ErrUnavailable, method, transportErrorText(err))
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("%w: identity toolkit %s: read response", ErrUnavailable, method)
	}
	if res.StatusCode/100 != 2 {
		return &APIError{Method: method, Status: res.StatusCode, Code: errorCode(raw)}
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("%w: identity toolkit %s: undecodable response", ErrUnavailable, method)
		}
	}
	return nil
}

// errorCode extracts Google's code from {"error":{"message":"CODE : detail"}}; only upper-case codes are
// kept, so no detail text (which may echo request data) survives.
func errorCode(raw []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return ""
	}
	code, _, _ := strings.Cut(e.Error.Message, ":")
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 64 || strings.ContainsFunc(code, func(r rune) bool {
		return (r < 'A' || r > 'Z') && r != '_' && (r < '0' || r > '9')
	}) {
		return ""
	}
	return code
}

// tokenErrorText describes a failed token fetch without the response body, which oauth2 includes in
// its RetrieveError text.
func tokenErrorText(err error) string {
	if re, ok := errors.AsType[*oauth2.RetrieveError](err); ok {
		if re.Response != nil {
			return "token endpoint answered HTTP " + strconv.Itoa(re.Response.StatusCode)
		}
		return "token endpoint refused the assertion"
	}
	return transportErrorText(err)
}

// transportErrorText names the failure class only; URLs in net/http errors are Google's fixed
// endpoints, but the class is all the logs need.
func transportErrorText(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	}
	return "transport error"
}
