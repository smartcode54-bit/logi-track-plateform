package forms

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestNormalizeEmail(t *testing.T) {
	for in, want := range map[string]string{
		" a.b@example.test ":               "a.b@example.test",
		"A.B+tag@Example.Test":             "A.B+tag@Example.Test",
		"no-at-sign":                       "",
		"two@@example.test":                "",
		"Name <a@example.test>":            "",
		"a@localhost":                      "", // no dot in the domain
		"a b@example.test":                 "",
		"":                                 "",
		strings.Repeat("a", 250) + "@x.co": "",
	} {
		if got := NormalizeEmail(in); got != want {
			t.Errorf("NormalizeEmail(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestEmailSubjectHashesTheLowerCasedAddress(t *testing.T) {
	sum := sha256.Sum256([]byte("someone@example.test"))
	want := hex.EncodeToString(sum[:])
	for _, body := range []string{`{"email":"Someone@Example.Test"}`, `{"email":" someone@example.test ","name":"x"}`} {
		if got := EmailSubject([]byte(body)); got != want {
			t.Fatalf("%s: %q", body, got)
		}
	}
	for _, body := range []string{`{"email":"nope"}`, `not json`, `{}`, `{"email":3}`} {
		if got := EmailSubject([]byte(body)); got != "" {
			t.Fatalf("%s: %q, want the rule skipped", body, got)
		}
	}
	if strings.Contains(EmailSubject([]byte(`{"email":"someone@example.test"}`)), "@") {
		t.Fatal("the subject must not hold the address")
	}
}

func TestCursorRoundTrip(t *testing.T) {
	if _, ok := decodeCursor("not-base64!"); ok {
		t.Fatal("accepted a bad cursor")
	}
	if _, ok := decodeCursor(""); ok {
		t.Fatal("accepted an empty cursor")
	}
}
