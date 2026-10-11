package apikey

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"
)

const testPepper = "test-pepper-0123456789abcdefghijklmnopqrstuvwxyz"

func pepper(t *testing.T, v string) Pepper {
	t.Helper()
	p, err := NewPepper(v)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNewPepper(t *testing.T) {
	if p, err := NewPepper(""); err != nil || p.Set() {
		t.Fatalf("empty pepper: set=%v err=%v", p.Set(), err)
	}
	if _, err := NewPepper(strings.Repeat("x", MinPepperLen-1)); err == nil {
		t.Fatal("a short pepper was accepted")
	}
	p := pepper(t, testPepper)
	if !p.Set() {
		t.Fatal("pepper not set")
	}
	for _, s := range []string{p.String(), p.GoString(), fmt.Sprintf("%v %+v %#v %s", p, p, p, p)} {
		if strings.Contains(s, "test-pepper") {
			t.Fatalf("the pepper leaked into %q", s)
		}
	}
}

func TestGenerateFormatAndHash(t *testing.T) {
	p := pepper(t, testPepper)
	seen := map[string]bool{}
	for range 200 {
		k, err := Generate(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(k.Secret) != Len || !strings.HasPrefix(k.Secret, Scheme+k.Prefix+"_") || len(k.Prefix) != PrefixLen {
			t.Fatalf("bad secret shape %q (prefix %q)", k.Secret, k.Prefix)
		}
		pre, ok := Prefix(k.Secret)
		if !ok || pre != k.Prefix {
			t.Fatalf("Prefix(%q) = %q, %v", k.Secret, pre, ok)
		}
		want := sha256.Sum256([]byte(k.Secret + testPepper))
		if string(k.Hash) != string(want[:]) {
			t.Fatal("hash is not sha256(secret || pepper)")
		}
		if !p.Matches(k.Secret, k.Hash) {
			t.Fatal("a fresh key does not match its hash")
		}
		if seen[k.Secret] {
			t.Fatal("duplicate secret")
		}
		seen[k.Secret] = true
	}
}

func TestMatchesRefuses(t *testing.T) {
	p := pepper(t, testPepper)
	k, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	other := pepper(t, testPepper+"-rotated")
	repl := "A"
	if strings.HasSuffix(k.Secret, repl) {
		repl = "E"
	}
	switch {
	case other.Matches(k.Secret, k.Hash):
		t.Fatal("a rotated pepper still matches")
	case (Pepper{}).Matches(k.Secret, k.Hash):
		t.Fatal("the unset pepper matches")
	case p.Matches(k.Secret[:Len-1]+repl, k.Hash):
		t.Fatal("a changed secret matches")
	case p.Matches(k.Secret, k.Hash[:31]):
		t.Fatal("a short stored hash matches")
	}
	if _, err := Generate(Pepper{}); err == nil {
		t.Fatal("Generate without a pepper")
	}
}

func TestPrefixRefusesMalformed(t *testing.T) {
	p := pepper(t, testPepper)
	k, err := Generate(p)
	if err != nil {
		t.Fatal(err)
	}
	body := k.Secret[len(Scheme)+PrefixLen+1:]
	for name, v := range map[string]string{
		"empty":          "",
		"short":          k.Secret[:Len-1],
		"long":           k.Secret + "A",
		"scheme":         "ltx_" + k.Secret[len(Scheme):],
		"separator":      k.Secret[:len(Scheme)+PrefixLen] + "-" + body,
		"prefix upper":   Scheme + "ABCDEFGH_" + body,
		"prefix digit 1": Scheme + "abcdefg1_" + body,
		"body alphabet":  Scheme + k.Prefix + "_" + strings.Repeat("+", BodyLen),
		"jwt-like":       "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJlLXNpZ25hdHVyZS1zaWduYXR1cmU",
	} {
		if _, ok := Prefix(v); ok {
			t.Errorf("%s: %q accepted", name, v)
		}
	}
}
