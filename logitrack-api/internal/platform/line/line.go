// Package line is the LINE Messaging API client of the Go worker (P5; main spec §7.6, Appendix B §B.5.3): one
// POST {LINE_API_BASE_URL}/v2/bot/message/push per card, bearer LINE_CHANNEL_ACCESS_TOKEN, with an X-Line-Retry-Key so a
// retry of a push whose answer was lost is refused by LINE (409, the original request accepted) instead of posting the
// card twice. The notify.line consumer (internal/notify) is its only caller: the channel token lives in the worker alone
// (main spec §16.1), never in the api.
//
// Nothing here reaches LINE unless a deployment turns LINE_NOTIFY_ENABLED on with a token: the tests use the in-process
// stand-in of package linetest. Errors never carry the token or the request body: only the HTTP status and LINE's own
// short message (which names a field, never a value of ours), so a job's error column and the delivery log name
// nothing secret.
package line

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultBaseURL is the production origin of the Messaging API (legacy LINE_PUSH_URL, fn:lineNotify.ts:43).
const DefaultBaseURL = "https://api.line.me"

// PushPath is the push endpoint below the base URL.
const PushPath = "/v2/bot/message/push"

// Timeout bounds one push (the legacy fetch had none).
const Timeout = 10 * time.Second

// Headers of the Messaging API.
const (
	HeaderRetryKey        = "X-Line-Retry-Key"
	HeaderRequestID       = "X-Line-Request-Id"
	HeaderAcceptedRequest = "X-Line-Accepted-Request-Id"
)

// maxErrorMessage bounds LINE's error message kept in an error text.
const maxErrorMessage = 200

// Result is an accepted push: the request id LINE gave it and whether this answer was LINE refusing a retry of a
// request it had accepted already (409 with X-Line-Accepted-Request-Id: the card is in the group once, from the first
// attempt).
type Result struct {
	RequestID string
	Replayed  bool
}

// Pusher sends one push request (the interface the consumer depends on; tests use linetest or a stub). body is the
// whole JSON request {"to": ..., "messages": [...]}; retryKey is a UUID that stays the same for every attempt of one
// notification.
type Pusher interface {
	Push(ctx context.Context, retryKey string, body []byte) (Result, error)
}

// HTTPError is a non-2xx answer other than an accepted retry.
type HTTPError struct {
	Status  int
	Message string // LINE's "message", cut to 200 bytes; may be empty
}

func (e *HTTPError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("line: push HTTP %d", e.Status)
	}
	return fmt.Sprintf("line: push HTTP %d: %s", e.Status, e.Message)
}

// IsTransient reports an error the consumer retries (main spec §7.6: 429 and 5xx retry; other 4xx are recorded as
// skipped): an answer 429 or 5xx, and every request that got no answer (a refused connection, a reset, the timeout).
// A request whose answer was lost may have been delivered; its retry carries the same X-Line-Retry-Key, which LINE
// answers with 409 (Result.Replayed) instead of posting the card again.
func IsTransient(err error) bool {
	if err == nil {
		return false
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Status == http.StatusTooManyRequests || he.Status >= 500
	}
	return !errors.Is(err, ErrConfig)
}

// ErrConfig marks a client that cannot send at all (no request was built).
var ErrConfig = errors.New("line: client misconfigured")

// Config configures the HTTP client.
type Config struct {
	// BaseURL is LINE_API_BASE_URL (an origin, optionally with a path prefix: the local stack's WireMock is
	// http://mocks:8080/line); empty = DefaultBaseURL.
	BaseURL string
	// Token is LINE_CHANNEL_ACCESS_TOKEN. Required.
	Token string
	// Timeout bounds one push (default Timeout).
	Timeout time.Duration
	// HTTP is the client to use (default: http.Client with Timeout and no redirects).
	HTTP *http.Client
}

// ValidateBaseURL checks LINE_API_BASE_URL: an absolute http(s) URL without credentials, query or fragment.
func ValidateBaseURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" ||
		u.User != nil {
		return errors.New("must be an absolute http(s) URL without credentials, query or fragment")
	}
	return nil
}

// ErrNoToken is New's error when the channel access token is blank.
var ErrNoToken = errors.New("line: LINE_CHANNEL_ACCESS_TOKEN is required")

// HTTP calls the Messaging API.
type HTTP struct {
	url     string
	token   string
	timeout time.Duration
	http    *http.Client
}

// New builds the client. It does no I/O.
func New(c Config) (*HTTP, error) {
	base := strings.TrimSpace(c.BaseURL)
	if base == "" {
		base = DefaultBaseURL
	}
	if err := ValidateBaseURL(base); err != nil {
		return nil, fmt.Errorf("line: base URL %w", err)
	}
	if strings.TrimSpace(c.Token) == "" {
		return nil, ErrNoToken
	}
	if c.Timeout <= 0 {
		c.Timeout = Timeout
	}
	if c.HTTP == nil {
		// A redirect would carry the bearer token to another origin: never followed.
		c.HTTP = &http.Client{Timeout: c.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
	}
	return &HTTP{url: strings.TrimRight(base, "/") + PushPath, token: strings.TrimSpace(c.Token), timeout: c.Timeout,
		http: c.HTTP}, nil
}

// Push sends one push request. A 2xx answer is a Result; 409 with X-Line-Accepted-Request-Id is a Result with
// Replayed (LINE accepted this retry key before); any other answer is an *HTTPError; a request that got no answer
// returns the transport error without the URL.
func (h *HTTP) Push(ctx context.Context, retryKey string, body []byte) (Result, error) {
	if h == nil {
		return Result{}, ErrConfig
	}
	ctx, cancel := context.WithTimeout(ctx, h.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.url, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("%w: cannot build the request", ErrConfig)
	}
	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")
	if retryKey != "" {
		req.Header.Set(HeaderRetryKey, retryKey)
	}
	res, err := h.http.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // the URL is configuration, not part of the message
		}
		return Result{}, fmt.Errorf("line: push: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
	switch {
	case res.StatusCode >= 200 && res.StatusCode < 300:
		return Result{RequestID: res.Header.Get(HeaderRequestID)}, nil
	case res.StatusCode == http.StatusConflict && res.Header.Get(HeaderAcceptedRequest) != "":
		return Result{RequestID: res.Header.Get(HeaderAcceptedRequest), Replayed: true}, nil
	}
	return Result{}, &HTTPError{Status: res.StatusCode, Message: errorMessage(raw)}
}

// errorMessage is LINE's "message" of an error body, cut to maxErrorMessage bytes on a rune boundary.
func errorMessage(raw []byte) string {
	var b struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &b) != nil {
		return ""
	}
	m := strings.TrimSpace(b.Message)
	if len(m) <= maxErrorMessage {
		return m
	}
	cut := maxErrorMessage
	for cut > 0 && (m[cut]&0xC0) == 0x80 {
		cut--
	}
	return m[:cut]
}
