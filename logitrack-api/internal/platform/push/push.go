// Package push sends Firebase Cloud Messaging HTTP v1 messages for the notify.fcm consumer (main spec
// §7.6, §11.8): one POST {BaseURL}/v1/projects/{FCM_PROJECT_ID}/messages:send per token, authorised by
// an OAuth2 access token of the FCM_SERVICE_ACCOUNT_JSON service account (RFC 7523 JWT bearer grant,
// scope firebase.messaging), at most Concurrency requests in flight per process. There is no Firebase
// SDK in Go: the wire format follows what the Firebase Admin SDK sends (android.priority,
// android.notification.channel_id, apns.headers, apns.payload.aps).
//
// Nothing here logs. Errors never carry a device token, a message body or a byte of the key file; an
// FCM error keeps only the HTTP status and Google's error code.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/oauth2"
)

// Endpoints and fixed values of FCM HTTP v1.
const (
	// DefaultBaseURL is the FCM API host.
	DefaultBaseURL = "https://fcm.googleapis.com"
	// Scope is the OAuth2 scope of messages:send.
	Scope = "https://www.googleapis.com/auth/firebase.messaging"
	// DefaultConcurrency bounds the sends in flight per process (main spec §7.6: concurrency 16).
	DefaultConcurrency = 16
	// requestTimeout bounds one send, token fetch included.
	requestTimeout = 10 * time.Second
	// maxResponseBytes bounds an FCM response body.
	maxResponseBytes = 64 << 10
)

// Android priorities and the APNs values of a silent push (main spec §7.6: data-only, Android normal,
// APNs content-available 1 with priority 5).
const (
	PriorityHigh   = "high"
	PriorityNormal = "normal"
)

// Message is the "message" object of messages:send for one token.
type Message struct {
	Token        string            `json:"token"`
	Notification *Notification     `json:"notification,omitempty"`
	Data         map[string]string `json:"data,omitempty"`
	Android      *AndroidConfig    `json:"android,omitempty"`
	APNS         *APNSConfig       `json:"apns,omitempty"`
}

// Notification is the visible part of a message.
type Notification struct {
	Title string `json:"title,omitempty"`
	Body  string `json:"body,omitempty"`
}

// AndroidConfig is message.android.
type AndroidConfig struct {
	Priority     string               `json:"priority,omitempty"`
	Notification *AndroidNotification `json:"notification,omitempty"`
}

// AndroidNotification is message.android.notification.
type AndroidNotification struct {
	ChannelID string `json:"channel_id,omitempty"`
}

// APNSConfig is message.apns: HTTP/2 headers for APNs and the aps payload.
type APNSConfig struct {
	Headers map[string]string `json:"headers,omitempty"`
	Payload *APNSPayload      `json:"payload,omitempty"`
}

// APNSPayload is message.apns.payload.
type APNSPayload struct {
	Aps Aps `json:"aps"`
}

// Aps is the aps dictionary.
type Aps struct {
	Sound            string `json:"sound,omitempty"`
	ContentAvailable int    `json:"content-available,omitempty"`
}

// Sender delivers one message to one token.
type Sender interface {
	Send(ctx context.Context, m Message) error
}

// Config configures Client.
type Config struct {
	// ProjectID is FCM_PROJECT_ID. Required.
	ProjectID string
	// Tokens supplies the OAuth2 access tokens (LoadCredentials). Required.
	Tokens oauth2.TokenSource
	// HTTPClient carries the sends; nil uses a client without its own timeout (each send is bounded
	// by a 10 s context). Tests pass the client of an httptest TLS server.
	HTTPClient *http.Client
	// BaseURL replaces DefaultBaseURL (tests). It must be https.
	BaseURL string
	// Concurrency bounds the sends in flight; 0 = DefaultConcurrency.
	Concurrency int
}

// Client sends messages through FCM HTTP v1. It is safe for concurrent use.
type Client struct {
	endpoint string
	client   *http.Client
	tokens   oauth2.TokenSource
	sem      chan struct{}
}

// New builds the client; it performs no I/O.
func New(cfg Config) (*Client, error) {
	if !ValidProjectID(cfg.ProjectID) {
		return nil, errors.New("push: FCM_PROJECT_ID is not a project id")
	}
	if cfg.Tokens == nil {
		return nil, errors.New("push: a token source is required")
	}
	base := cfg.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	if u, err := url.Parse(base); err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, errors.New("push: the FCM base URL must be an https URL")
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	n := cfg.Concurrency
	if n <= 0 {
		n = DefaultConcurrency
	}
	return &Client{
		endpoint: strings.TrimRight(base, "/") + "/v1/projects/" + cfg.ProjectID + "/messages:send",
		client:   client,
		tokens:   oauth2.ReuseTokenSource(nil, cfg.Tokens),
		sem:      make(chan struct{}, n),
	}, nil
}

// Send posts m. nil means FCM accepted it; any other error is an *Error (classify it with
// IsTokenInvalid, MaybeDelivered and IsTransient).
func (c *Client) Send(ctx context.Context, m Message) error {
	if m.Token == "" {
		return &Error{Code: "INVALID_ARGUMENT", tokenNamed: true, cause: errors.New("empty token")}
	}
	body, err := json.Marshal(struct {
		Message Message `json:"message"`
	}{m})
	if err != nil {
		return &Error{Code: "INVALID_ARGUMENT", cause: errors.New("the message does not encode")}
	}
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return &Error{transport: true, cause: ctx.Err()}
	}
	defer func() { <-c.sem }()
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	tok, err := c.tokens.Token()
	if err != nil {
		// The token endpoint was unreachable or refused the assertion: nothing was sent. oauth2 errors
		// carry Google's error code and description, never the assertion or the key.
		return &Error{transport: true, cause: fmt.Errorf("access token: %w", redactRetrieve(err))}
	}
	// wrote tells a request that never left (dial, TLS, a refused or reset connection before the write)
	// from one FCM may have taken: it is set once the transport has written the whole request on the
	// connection of the last attempt (the transport's own retries pick a new connection first). net/http
	// reports the write before its last flush, so a failure in between counts as maybe delivered: that
	// push is then lost, never sent twice.
	var wrote atomic.Bool
	traced := httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { wrote.Store(false) },
		WroteRequest: func(i httptrace.WroteRequestInfo) {
			if i.Err == nil {
				wrote.Store(true)
			}
		},
	})
	req, err := http.NewRequestWithContext(traced, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return &Error{transport: true, cause: err}
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	tok.SetAuthHeader(req)
	resp, err := c.client.Do(req)
	if err != nil {
		return &Error{transport: true, maybeDelivered: wrote.Load(), cause: errors.New("request failed: " + transportReason(ctx, err))}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	return parseError(resp.StatusCode, raw)
}

// transportReason names a failed round trip without the URL and its query (they carry no secret, but
// the wrapped net errors may name resolver and proxy details nobody needs in a delivery row).
func transportReason(ctx context.Context, err error) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.Timeout() {
		return "timeout"
	}
	return "network error"
}

// redactRetrieve keeps the OAuth2 error code of a token-endpoint refusal and drops the rest of the
// response body (the JWT grant of golang.org/x/oauth2/jwt leaves ErrorCode empty and the body raw).
func redactRetrieve(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) {
		code := re.ErrorCode
		if code == "" {
			var body struct {
				Error string `json:"error"`
			}
			if json.Unmarshal(re.Body, &body) == nil {
				code = body.Error
			}
		}
		if code == "" {
			code = "no error code"
		}
		status := 0
		if re.Response != nil {
			status = re.Response.StatusCode
		}
		return fmt.Errorf("token endpoint HTTP %d (%s)", status, sanitizeOAuthCode(code))
	}
	return errors.New("token endpoint unreachable")
}

// Error is a send FCM did not accept: an HTTP error answer (Status, Code) or a transport failure
// (Status 0). Error() names the status and Google's code only.
type Error struct {
	Status int    // HTTP status; 0 when no answer arrived
	Code   string // FcmError errorCode (UNREGISTERED, INVALID_ARGUMENT, ...), else the google.rpc status

	tokenNamed     bool // the answer names message.token as the offending field
	transport      bool
	maybeDelivered bool // transport: the whole request was written before the failure
	cause          error
}

func (e *Error) Error() string {
	if e.transport || e.Status == 0 {
		if e.cause != nil {
			return "fcm: " + e.cause.Error()
		}
		return "fcm: no answer"
	}
	code := e.Code
	if code == "" {
		code = "no error code"
	}
	return fmt.Sprintf("fcm: HTTP %d (%s)", e.Status, code)
}

func (e *Error) Unwrap() error { return e.cause }

// IsTokenInvalid reports whether FCM says the token can never receive a message again, so its
// device_tokens row is deleted: UNREGISTERED (the app was uninstalled or the token expired), or
// INVALID_ARGUMENT that names the token (main spec §7.6). An INVALID_ARGUMENT about the payload keeps
// the token: a bug in a message must not wipe the devices it was sent to.
func IsTokenInvalid(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == "UNREGISTERED" || (e.Code == "INVALID_ARGUMENT" && e.tokenNamed)
}

// MaybeDelivered reports a send whose outcome is unknown: the whole request was written to FCM and
// then no answer arrived (the connection broke, the 10 s timeout fired, the context was cancelled).
// FCM may have accepted the message, and FCM HTTP v1 has no idempotency key, so sending it again could
// show the notification twice: such a send is final for this message (never transient).
func MaybeDelivered(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.transport && e.maybeDelivered
}

// IsTransient reports whether a retry may succeed without sending the message twice: a request that
// never reached FCM (network failure or timeout before the request was written, the token endpoint, a
// cancelled wait for a slot), 429 QUOTA_EXCEEDED, 5xx UNAVAILABLE / INTERNAL, and the answers that mean
// the sender is misconfigured rather than the token wrong (401 without THIRD_PARTY_AUTH_ERROR, 403
// without SENDER_ID_MISMATCH, 404 without UNREGISTERED): those retry, dead-letter after the last rung
// and alert, and a replay after the fix delivers. Everything else (the token, the payload, APNs
// credentials of the app, and no answer after the request was written: MaybeDelivered) is final for
// this message.
func IsTransient(err error) bool {
	var e *Error
	if !errors.As(err, &e) {
		return err != nil
	}
	if e.transport {
		return !e.maybeDelivered
	}
	switch e.Code {
	case "UNREGISTERED", "INVALID_ARGUMENT", "SENDER_ID_MISMATCH", "THIRD_PARTY_AUTH_ERROR":
		return false
	case "QUOTA_EXCEEDED", "UNAVAILABLE", "INTERNAL":
		return true
	}
	switch {
	case e.Status == http.StatusTooManyRequests, e.Status >= 500:
		return true
	case e.Status == http.StatusUnauthorized, e.Status == http.StatusForbidden, e.Status == http.StatusNotFound:
		return true
	}
	return false
}

// parseError reads the google.rpc error of an FCM answer:
//
//	{"error":{"code":404,"message":"...","status":"NOT_FOUND","details":[
//	  {"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"},
//	  {"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"message.token",...}]}]}}
//
// The FcmError errorCode wins over the generic status. message is consulted only to tell whether an
// INVALID_ARGUMENT is about the registration token; it is never stored.
func parseError(status int, raw []byte) *Error {
	e := &Error{Status: status}
	var body struct {
		Error struct {
			Message string `json:"message"`
			Status  string `json:"status"`
			Details []struct {
				Type            string `json:"@type"`
				ErrorCode       string `json:"errorCode"`
				FieldViolations []struct {
					Field string `json:"field"`
				} `json:"fieldViolations"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return e
	}
	e.Code = sanitizeCode(body.Error.Status)
	for _, d := range body.Error.Details {
		switch {
		case strings.HasSuffix(d.Type, "google.firebase.fcm.v1.FcmError") && d.ErrorCode != "":
			e.Code = sanitizeCode(d.ErrorCode)
		case strings.HasSuffix(d.Type, "google.rpc.BadRequest"):
			for _, v := range d.FieldViolations {
				if v.Field == "message.token" {
					e.tokenNamed = true
				}
			}
		}
	}
	if e.Code == "INVALID_ARGUMENT" && strings.Contains(strings.ToLower(body.Error.Message), "registration token") {
		e.tokenNamed = true
	}
	return e
}

// sanitizeCode keeps an error code to the shape Google uses (A-Z, 0-9, _), so nothing else from the
// response ever reaches a log line or a delivery row.
func sanitizeCode(s string) string {
	if len(s) > 64 {
		return ""
	}
	for _, r := range s {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return ""
		}
	}
	return s
}

// sanitizeOAuthCode keeps an RFC 6749 error code (invalid_grant, ...) and drops anything else.
func sanitizeOAuthCode(s string) string {
	if len(s) > 64 {
		return "unexpected error code"
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' && r != ' ' {
			return "unexpected error code"
		}
	}
	return s
}

var projectIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`)

// ValidProjectID reports whether id has the shape of a Google Cloud / Firebase project id: 6 to 30
// lower-case letters, digits or hyphens, starting with a letter and not ending with a hyphen.
func ValidProjectID(id string) bool { return projectIDPattern.MatchString(id) }
