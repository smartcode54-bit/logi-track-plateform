package push_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/push"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/push/pushtest"
)

const deviceToken = "dKx1-token:APA91bH_device-token-value"

// One message per token to the project's messages:send, with an OAuth2 access token from the
// service account's JWT bearer grant; the token is fetched once and reused; the JSON is what the
// Firebase Admin SDK sends (channel_id, apns.payload.aps).
func TestSendWireFormatAndToken(t *testing.T) {
	fcm := pushtest.New(t)
	c := fcm.Client(0)
	visible := push.Message{
		Token:        deviceToken,
		Notification: &push.Notification{Title: "New message from Admin", Body: "สวัสดี"},
		Data:         map[string]string{"type": "chat", "chatId": "c1", "messageId": "m1"},
		Android:      &push.AndroidConfig{Priority: push.PriorityHigh, Notification: &push.AndroidNotification{ChannelID: "chat"}},
		APNS:         &push.APNSConfig{Payload: &push.APNSPayload{Aps: push.Aps{Sound: "default"}}},
	}
	silent := push.Message{
		Token:   "second-token",
		Data:    map[string]string{"type": "tasks_changed"},
		Android: &push.AndroidConfig{Priority: push.PriorityNormal},
		APNS: &push.APNSConfig{Headers: map[string]string{"apns-priority": "5", "apns-push-type": "background"},
			Payload: &push.APNSPayload{Aps: push.Aps{ContentAvailable: 1}}},
	}
	for _, m := range []push.Message{visible, silent} {
		if err := c.Send(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	sent := fcm.Sent()
	if len(sent) != 2 || fcm.TokenRequests() != 1 {
		t.Fatalf("%d messages with %d access tokens, want 2 with 1", len(sent), fcm.TokenRequests())
	}
	want := []string{
		`{"token":"` + deviceToken + `","notification":{"title":"New message from Admin","body":"สวัสดี"},` +
			`"data":{"chatId":"c1","messageId":"m1","type":"chat"},"android":{"priority":"high","notification":{"channel_id":"chat"}},` +
			`"apns":{"payload":{"aps":{"sound":"default"}}}}`,
		`{"token":"second-token","data":{"type":"tasks_changed"},"android":{"priority":"normal"},` +
			`"apns":{"headers":{"apns-priority":"5","apns-push-type":"background"},"payload":{"aps":{"content-available":1}}}}`,
	}
	for i, s := range sent {
		if string(s.Raw) != want[i] {
			t.Errorf("message %d:\n got %s\nwant %s", i, s.Raw, want[i])
		}
	}
}

// Classification of FCM's answers (main spec §7.6): UNREGISTERED and an INVALID_ARGUMENT naming the
// token delete the token; payload errors, sender mismatch and APNs credentials are final for the
// message; overload, quota and sender misconfiguration retry. No error text carries the token or
// Google's message.
func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name               string
		answer             pushtest.Answer
		invalid, transient bool
		text               string
	}{
		{"unregistered", pushtest.Unregistered(), true, false, "fcm: HTTP 404 (UNREGISTERED)"},
		{"invalid token", pushtest.InvalidToken(), true, false, "fcm: HTTP 400 (INVALID_ARGUMENT)"},
		{"invalid token without details", pushtest.Answer{Status: 400, Body: `{"error":{"code":400,"message":"The registration token is not a valid FCM registration token","status":"INVALID_ARGUMENT"}}`}, true, false, "fcm: HTTP 400 (INVALID_ARGUMENT)"},
		{"invalid payload", pushtest.InvalidPayload(), false, false, "fcm: HTTP 400 (INVALID_ARGUMENT)"},
		{"sender mismatch", pushtest.Answer{Status: 403, Body: `{"error":{"code":403,"status":"PERMISSION_DENIED","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"SENDER_ID_MISMATCH"}]}}`}, false, false, "fcm: HTTP 403 (SENDER_ID_MISMATCH)"},
		{"apns auth", pushtest.Answer{Status: 401, Body: `{"error":{"code":401,"status":"UNAUTHENTICATED","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"THIRD_PARTY_AUTH_ERROR"}]}}`}, false, false, "fcm: HTTP 401 (THIRD_PARTY_AUTH_ERROR)"},
		{"unavailable", pushtest.Unavailable(), false, true, "fcm: HTTP 503 (UNAVAILABLE)"},
		{"quota", pushtest.Answer{Status: 429, Body: `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"QUOTA_EXCEEDED"}]}}`}, false, true, "fcm: HTTP 429 (QUOTA_EXCEEDED)"},
		{"internal", pushtest.Answer{Status: 500, Body: `not json at all ` + deviceToken}, false, true, "fcm: HTTP 500 (no error code)"},
		{"permission denied", pushtest.Answer{Status: 403, Body: `{"error":{"code":403,"message":"SenderId mismatch for ` + deviceToken + `","status":"PERMISSION_DENIED"}}`}, false, true, "fcm: HTTP 403 (PERMISSION_DENIED)"},
		{"project not found", pushtest.Answer{Status: 404, Body: `{"error":{"code":404,"status":"NOT_FOUND"}}`}, false, true, "fcm: HTTP 404 (NOT_FOUND)"},
		{"strange code", pushtest.Answer{Status: 400, Body: `{"error":{"code":400,"status":"<script>` + deviceToken + `"}}`}, false, false, "fcm: HTTP 400 (no error code)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fcm := pushtest.New(t)
			fcm.Respond(deviceToken, tc.answer)
			err := fcm.Client(0).Send(context.Background(), push.Message{Token: deviceToken, Data: map[string]string{"type": "chat"}})
			var pe *push.Error
			if !errors.As(err, &pe) {
				t.Fatalf("error %T %v", err, err)
			}
			if push.IsTokenInvalid(err) != tc.invalid || push.IsTransient(err) != tc.transient {
				t.Fatalf("%v: token invalid %v transient %v", err, push.IsTokenInvalid(err), push.IsTransient(err))
			}
			if err.Error() != tc.text || strings.Contains(err.Error(), "token-value") {
				t.Fatalf("error text %q, want %q", err.Error(), tc.text)
			}
		})
	}
}

// A refused or unreachable token endpoint sends nothing and retries; the error keeps the OAuth2 code
// and never the endpoint's body.
func TestTokenEndpointFailures(t *testing.T) {
	fcm := pushtest.New(t)
	// A key the endpoint does not know signs the assertion: invalid_grant.
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	creds, err := push.ParseCredentials(pushtest.KeyFile(t, other, pushtest.TokenURL, pushtest.ProjectID))
	if err != nil {
		t.Fatal(err)
	}
	c, err := push.New(push.Config{ProjectID: pushtest.ProjectID, Tokens: creds.TokenSource(fcm.HTTPClient()),
		HTTPClient: fcm.HTTPClient(), BaseURL: pushtest.BaseURL})
	if err != nil {
		t.Fatal(err)
	}
	err = c.Send(context.Background(), push.Message{Token: deviceToken})
	if !push.IsTransient(err) || push.IsTokenInvalid(err) || err.Error() != "fcm: access token: token endpoint HTTP 400 (invalid_grant)" {
		t.Fatalf("refused grant: %v", err)
	}
	if fcm.Calls() != 0 {
		t.Fatal("a message was sent without an access token")
	}
	// No route to the endpoint at all.
	broken := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("dial tcp: refused") })}
	c, err = push.New(push.Config{ProjectID: pushtest.ProjectID, Tokens: creds.TokenSource(broken), HTTPClient: broken, BaseURL: pushtest.BaseURL})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Send(context.Background(), push.Message{Token: deviceToken}); !push.IsTransient(err) || err.Error() != "fcm: access token: token endpoint unreachable" {
		t.Fatalf("unreachable token endpoint: %v", err)
	}
	// FCM itself unreachable, with a valid access token.
	c, err = push.New(push.Config{ProjectID: pushtest.ProjectID, Tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "x"}),
		HTTPClient: broken, BaseURL: pushtest.BaseURL})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Send(context.Background(), push.Message{Token: deviceToken}); !push.IsTransient(err) || err.Error() != "fcm: request failed: network error" {
		t.Fatalf("unreachable FCM: %v", err)
	}
	// A cancelled context while waiting for a slot is transient too.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Send(ctx, push.Message{Token: deviceToken}); !push.IsTransient(err) {
		t.Fatalf("cancelled: %v", err)
	}
	if err := c.Send(context.Background(), push.Message{}); push.IsTransient(err) || !push.IsTokenInvalid(err) {
		t.Fatalf("empty token: %v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// At most Concurrency sends are in flight per client (main spec §7.6: concurrency 16).
func TestConcurrencyBound(t *testing.T) {
	var inFlight, peak atomic.Int32
	slow := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
		return &http.Response{StatusCode: 200, Body: http.NoBody, Header: http.Header{}, Request: r}, nil
	})}
	c, err := push.New(push.Config{ProjectID: pushtest.ProjectID, Tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "x"}),
		HTTPClient: slow, BaseURL: pushtest.BaseURL, Concurrency: 3})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			if err := c.Send(context.Background(), push.Message{Token: fmt.Sprint("t", i)}); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if p := peak.Load(); p != 3 {
		t.Fatalf("peak in flight %d, want 3", p)
	}
}

func TestNewValidates(t *testing.T) {
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "x"})
	for _, cfg := range []push.Config{
		{ProjectID: "", Tokens: src},
		{ProjectID: "Bad_Project", Tokens: src},
		{ProjectID: "good-project"},
		{ProjectID: "good-project", Tokens: src, BaseURL: "http://fcm.googleapis.com"},
	} {
		if _, err := push.New(cfg); err == nil {
			t.Errorf("accepted %+v", cfg)
		}
	}
	if _, err := push.New(push.Config{ProjectID: "good-project", Tokens: src}); err != nil {
		t.Fatal(err)
	}
}

// The key file of FCM_SERVICE_ACCOUNT_JSON: every problem is named with the variable, and no error or
// formatted value ever carries a byte of the key.
func TestCredentials(t *testing.T) {
	key := pushtest.Key(t)
	good := pushtest.KeyFile(t, key, "", "pushtest-project")
	c, err := push.ParseCredentials(good)
	if err != nil {
		t.Fatal(err)
	}
	if c.ProjectID() != "pushtest-project" || fmt.Sprint(c) != "push.Credentials{redacted}" || fmt.Sprintf("%#v", c) != "push.Credentials{redacted}" {
		t.Fatalf("credentials %v", c)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "sa.json")
	if err := os.WriteFile(path, good, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := push.LoadCredentials(path); err != nil {
		t.Fatal(err)
	}

	small, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	ec, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ecDER, err := x509.MarshalPKCS8PrivateKey(ec)
	if err != nil {
		t.Fatal(err)
	}
	edit := func(f func(m map[string]any)) []byte {
		var m map[string]any
		if err := json.Unmarshal(good, &m); err != nil {
			t.Fatal(err)
		}
		f(m)
		b, _ := json.Marshal(m)
		return b
	}
	large := make([]byte, 70<<10)
	for i := range large {
		large[i] = ' '
	}
	cases := map[string][]byte{
		"not a JSON service-account key file": []byte("-----BEGIN PRIVATE KEY-----"),
		`type must be "service_account"`:      edit(func(m map[string]any) { m["type"] = "authorized_user" }),
		"client_email is missing":             edit(func(m map[string]any) { delete(m, "client_email") }),
		"private_key: not a PEM private key":  edit(func(m map[string]any) { m["private_key"] = "MIIE" }),
		"private_key: the RSA key is shorter": pushtest.KeyFile(t, small, "", ""),
		"private_key: not an RSA key": edit(func(m map[string]any) {
			m["private_key"] = string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: ecDER}))
		}),
		"token_uri must be an https URL": edit(func(m map[string]any) { m["token_uri"] = "http://oauth2.googleapis.com/token" }),
		"project_id is malformed":        edit(func(m map[string]any) { m["project_id"] = "Not A Project" }),
	}
	for want, b := range cases {
		_, err := push.ParseCredentials(b)
		if err == nil || !strings.HasPrefix(err.Error(), "FCM_SERVICE_ACCOUNT_JSON: ") || !strings.Contains(err.Error(), want) ||
			strings.Contains(err.Error(), "MII") {
			t.Errorf("%s: %v", want, err)
		}
	}
	if err := os.WriteFile(path, large, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := push.LoadCredentials(path); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Errorf("large file: %v", err)
	}
	if _, err := push.LoadCredentials(filepath.Join(dir, "missing.json")); err == nil ||
		err.Error() != "FCM_SERVICE_ACCOUNT_JSON: cannot open the service-account file" {
		t.Errorf("missing file: %v", err)
	}
}
