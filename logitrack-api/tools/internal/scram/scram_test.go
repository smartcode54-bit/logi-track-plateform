package scram

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
)

// TestRFC7677Vector checks the derived keys against the SCRAM-SHA-256 exchange of RFC 7677 §3
// (user "user", password "pencil"): the client proof and the server signature it prints.
func TestRFC7677Vector(t *testing.T) {
	salt, _ := base64.StdEncoding.DecodeString("W22ZaJ0SNY7soEsUEjb6gQ==")
	storedKey, serverKey, err := keys("pencil", salt, 4096)
	if err != nil {
		t.Fatal(err)
	}
	const authMessage = "n=user,r=rOprNGfwEbeRWgbNEkqO," +
		"r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0,s=W22ZaJ0SNY7soEsUEjb6gQ==,i=4096," +
		"c=biws,r=rOprNGfwEbeRWgbNEkqO%hvYDpWUa2RaTCAfuxFIlj)hNlF$k0"
	if got := base64.StdEncoding.EncodeToString(mac(serverKey, authMessage)); got != "6rriTRBi23WpRR/wtup+mMhUZUn/dB5nLTJRsjl95G4=" {
		t.Fatalf("server signature %s", got)
	}
	// ClientProof = ClientKey XOR HMAC(StoredKey, AuthMessage); H(ClientKey) must be StoredKey.
	proof, _ := base64.StdEncoding.DecodeString("dHzbZapWIk4jUhN+Ute9ytag9zjfMHgsqmmiz7AndVQ=")
	sig := mac(storedKey, authMessage)
	clientKey := make([]byte, len(proof))
	for i := range proof {
		clientKey[i] = proof[i] ^ sig[i]
	}
	if sum := sha256.Sum256(clientKey); string(sum[:]) != string(storedKey) {
		t.Fatal("stored key does not match the RFC client proof")
	}
}

func TestVerifierFormat(t *testing.T) {
	a, err := Verifier("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Verifier("correct horse")
	if !strings.HasPrefix(a, "SCRAM-SHA-256$4096:") || strings.Count(a, "$") != 2 || strings.Count(a, ":") != 2 {
		t.Fatalf("format: %s", a)
	}
	if a == b {
		t.Fatal("two verifiers share a salt")
	}
	if strings.Contains(a, "correct horse") {
		t.Fatal("the verifier holds the password")
	}
}
