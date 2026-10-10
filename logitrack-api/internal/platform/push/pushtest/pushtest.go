// Package pushtest is an in-process stand-in for the Google endpoints of FCM HTTP v1: the OAuth2 token
// endpoint of the service-account JWT bearer grant and POST /v1/projects/{project}/messages:send. It is
// served through an http.RoundTripper, so tests never open a socket or reach Google. Every accepted
// message is recorded; answers can be scripted per device token (UNREGISTERED, 503, ...). Test support
// only; production code never imports it.
package pushtest

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/push"
)

// Hosts of the stand-in (.invalid never resolves, so a request that escaped the transport fails).
const (
	TokenURL = "https://oauth2.pushtest.invalid/token"
	BaseURL  = "https://fcm.pushtest.invalid"
	// ProjectID is the project the stand-in serves.
	ProjectID = "pushtest-project"
	// ClientEmail is the service account of Credentials.
	ClientEmail = "fcm-sender@pushtest-project.iam.gserviceaccount.com"
)

var (
	keyOnce sync.Once
	saKey   *rsa.PrivateKey
	keyErr  error
)

// Key is the service account's RSA key (one per test binary: 2048-bit generation is slow under -race).
func Key(t testing.TB) *rsa.PrivateKey {
	t.Helper()
	keyOnce.Do(func() { saKey, keyErr = rsa.GenerateKey(rand.Reader, 2048) })
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	return saKey
}

// KeyFile is a service-account key file for key with the given token_uri and project_id.
func KeyFile(t testing.TB, key *rsa.PrivateKey, tokenURI, projectID string) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]string{
		"type": "service_account", "project_id": projectID, "private_key_id": "pushtest-key-1",
		"private_key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})),
		"client_email": ClientEmail, "token_uri": tokenURI,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Answer is one scripted reply of messages:send.
type Answer struct {
	Status int
	Body   string
}

// Unregistered is FCM's answer for a token whose app was uninstalled.
func Unregistered() Answer {
	return Answer{Status: 404, Body: `{"error":{"code":404,"message":"Requested entity was not found.","status":"NOT_FOUND","details":[` +
		`{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`}
}

// InvalidToken is FCM's answer for a token that is not a registration token at all.
func InvalidToken() Answer {
	return Answer{Status: 400, Body: `{"error":{"code":400,"message":"The registration token is not a valid FCM registration token",` +
		`"status":"INVALID_ARGUMENT","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"INVALID_ARGUMENT"},` +
		`{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"message.token","description":"Invalid registration token"}]}]}}`}
}

// InvalidPayload is an INVALID_ARGUMENT about the message itself (not the token).
func InvalidPayload() Answer {
	return Answer{Status: 400, Body: `{"error":{"code":400,"message":"Invalid value at 'message.data[0].value'","status":"INVALID_ARGUMENT","details":[` +
		`{"@type":"type.googleapis.com/google.rpc.BadRequest","fieldViolations":[{"field":"message.data[0].value"}]}]}}`}
}

// Unavailable is FCM overloaded.
func Unavailable() Answer {
	return Answer{Status: 503, Body: `{"error":{"code":503,"message":"The service is currently unavailable.","status":"UNAVAILABLE",` +
		`"details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNAVAILABLE"}]}}`}
}

// Sent is one message FCM accepted.
type Sent struct {
	Message push.Message
	Raw     json.RawMessage // the request body's "message" object as sent
}

// Server is the stand-in.
type Server struct {
	t        testing.TB
	key      *rsa.PrivateKey
	mu       sync.Mutex
	sent     []Sent
	answers  map[string][]Answer
	tokens   map[string]bool // access tokens issued
	tokenReq int
	calls    int
}

// New starts a stand-in for t.
func New(t testing.TB) *Server {
	return &Server{t: t, key: Key(t), answers: map[string][]Answer{}, tokens: map[string]bool{}}
}

// Credentials is the service account the stand-in's token endpoint accepts.
func (s *Server) Credentials() *push.Credentials {
	s.t.Helper()
	c, err := push.ParseCredentials(KeyFile(s.t, s.key, TokenURL, ProjectID))
	if err != nil {
		s.t.Fatal(err)
	}
	return c
}

// HTTPClient routes the two hosts of the stand-in to it in process.
func (s *Server) HTTPClient() *http.Client { return &http.Client{Transport: s} }

// Client is a push.Client wired to the stand-in.
func (s *Server) Client(concurrency int) *push.Client {
	s.t.Helper()
	c, err := push.New(push.Config{ProjectID: ProjectID, Tokens: s.Credentials().TokenSource(s.HTTPClient()),
		HTTPClient: s.HTTPClient(), BaseURL: BaseURL, Concurrency: concurrency})
	if err != nil {
		s.t.Fatal(err)
	}
	return c
}

// Respond scripts the next answers for a device token, in order; afterwards the token is accepted.
func (s *Server) Respond(token string, answers ...Answer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.answers[token] = append(s.answers[token], answers...)
}

// Sent returns the accepted messages in order.
func (s *Server) Sent() []Sent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Sent(nil), s.sent...)
}

// SentTo returns the accepted messages of one device token.
func (s *Server) SentTo(token string) []Sent {
	var out []Sent
	for _, m := range s.Sent() {
		if m.Message.Token == token {
			out = append(out, m)
		}
	}
	return out
}

// Calls is the number of messages:send requests (accepted or not).
func (s *Server) Calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// TokenRequests is the number of access tokens issued.
func (s *Server) TokenRequests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokenReq
}

// RoundTrip implements http.RoundTripper.
func (s *Server) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		defer func() { _ = r.Body.Close() }()
	}
	rec := httptest.NewRecorder()
	switch {
	case r.URL.Scheme == "https" && r.URL.Host == "oauth2.pushtest.invalid" && r.URL.Path == "/token":
		s.token(rec, r)
	case r.URL.Scheme == "https" && r.URL.Host == "fcm.pushtest.invalid":
		s.send(rec, r)
	default:
		rec.WriteHeader(http.StatusBadGateway)
		_, _ = rec.WriteString(`{"error":"pushtest: unexpected host"}`)
	}
	res := rec.Result()
	res.Request = r
	return res, nil
}

// token is the JWT bearer grant (RFC 7523): the assertion must be signed by the service account's key
// for this endpoint with the firebase.messaging scope.
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
		http.Error(w, `{"error":"unsupported_grant_type"}`, http.StatusBadRequest)
		return
	}
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(r.PostForm.Get("assertion"), claims, func(*jwt.Token) (any, error) { return &s.key.PublicKey, nil },
		jwt.WithValidMethods([]string{"RS256"}), jwt.WithAudience(TokenURL), jwt.WithIssuer(ClientEmail))
	if err != nil || claims["scope"] != push.Scope {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Invalid JWT Signature."}`))
		return
	}
	s.mu.Lock()
	s.tokenReq++
	tok := "pushtest-access-" + strconv.Itoa(s.tokenReq)
	s.tokens[tok] = true
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": tok, "token_type": "Bearer", "expires_in": 3600})
}

func (s *Server) send(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	if r.Method != http.MethodPost || r.URL.Path != "/v1/projects/"+ProjectID+"/messages:send" {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":404,"message":"not found","status":"NOT_FOUND"}}`))
		return
	}
	tok, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	s.calls++
	known := s.tokens[tok]
	s.mu.Unlock()
	if !known {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":401,"message":"Request had invalid authentication credentials.","status":"UNAUTHENTICATED"}}`))
		return
	}
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Message json.RawMessage `json:"message"`
	}
	var m push.Message
	if json.Unmarshal(raw, &body) != nil || json.NewDecoder(bytes.NewReader(body.Message)).Decode(&m) != nil || m.Token == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"code":400,"message":"bad message","status":"INVALID_ARGUMENT"}}`))
		return
	}
	s.mu.Lock()
	var a *Answer
	if q := s.answers[m.Token]; len(q) > 0 {
		a, s.answers[m.Token] = &q[0], q[1:]
	} else {
		s.sent = append(s.sent, Sent{Message: m, Raw: body.Message})
	}
	s.mu.Unlock()
	if a != nil {
		w.WriteHeader(a.Status)
		_, _ = w.Write([]byte(a.Body))
		return
	}
	_, _ = w.Write([]byte(`{"name":"projects/` + ProjectID + `/messages/0:1"}`))
}
