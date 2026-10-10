package firebasescrypt_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebasescrypt"
)

// The public vectors of the firebase/scrypt repository live in testdata/public-vectors.txt (values
// verbatim, never production values); locally and in CI the FIREBASE_SCRYPT_* variables hold the
// parameter set of its first set (Appendix D). Every set uses salt separator Bw==, rounds 8 and
// mem_cost 14.
const (
	vectorsFile       = "testdata/public-vectors.txt"
	testSaltSeparator = "Bw=="
	testRounds        = 8
	testMemCost       = 14
)

type vector struct{ hash, salt, plaintext string }

type vectorSet struct {
	name, signer string
	vectors      []vector
}

// publicVectors parses vectorsFile: "set NAME SIGNER" opens a set, "HASH SALT PLAINTEXT" adds a
// vector (the plaintext is the rest of the line), "#" starts a comment.
func publicVectors(t *testing.T) []vectorSet {
	t.Helper()
	raw, err := os.ReadFile(vectorsFile)
	if err != nil {
		t.Fatal(err)
	}
	var sets []vectorSet
	for n, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.SplitN(line, " ", 3)
		switch {
		case len(f) == 3 && f[0] == "set" && !strings.Contains(f[2], " "):
			sets = append(sets, vectorSet{name: f[1], signer: f[2]})
		case len(f) == 3 && len(sets) > 0 && f[2] != "":
			sets[len(sets)-1].vectors = append(sets[len(sets)-1].vectors, vector{hash: f[0], salt: f[1], plaintext: f[2]})
		default:
			t.Fatalf("%s:%d: malformed line", vectorsFile, n+1)
		}
	}
	return sets
}

// testSigner is the signer of the first set (tests/01-known-value.sh), the set the README example uses.
func testSigner(t *testing.T) string {
	t.Helper()
	sets := publicVectors(t)
	if len(sets) == 0 {
		t.Fatalf("%s holds no set", vectorsFile)
	}
	return sets[0].signer
}

func params(t *testing.T) firebasescrypt.Params {
	t.Helper()
	p, err := firebasescrypt.ParseParams(testSigner(t), testSaltSeparator, testRounds, testMemCost)
	if err != nil || p == nil {
		t.Fatalf("params: %v", err)
	}
	return *p
}

func decode(t *testing.T, v string) []byte {
	t.Helper()
	b, err := firebasescrypt.DecodeB64(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The firebase/scrypt README example: password "user1password", salt "42xEC+ixf3L2lw==".
func TestKnownVector(t *testing.T) {
	p := params(t)
	salt := decode(t, "42xEC+ixf3L2lw==")
	want := decode(t, "lSrfV15cpx95/sZS2W9c9Kp6i/LVgQNDNC/qzrCnh1SAyZvqmZqAjTdn3aoItz+VHjoZilo78198JAdRuid5lQ==")
	ok, err := firebasescrypt.Verify("user1password", salt, want, p)
	if err != nil || !ok {
		t.Fatalf("known vector: ok=%v err=%v", ok, err)
	}
	got, err := firebasescrypt.Hash("user1password", salt, p)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("Hash differs from the known vector")
	}
	for _, wrong := range []string{"user1Password", "user1password ", "", "user2password"} {
		if ok, err := firebasescrypt.Verify(wrong, salt, want, p); err != nil || ok {
			t.Errorf("%q must not verify (ok=%v err=%v)", wrong, ok, err)
		}
	}
	if ok, _ := firebasescrypt.Verify("user1password", decode(t, "42xEC+ixf3L2lA=="), want, p); ok {
		t.Error("another salt must not verify")
	}
	if ok, _ := firebasescrypt.Verify("user1password", nil, want, p); ok {
		t.Error("an empty salt never verifies")
	}
}

// TestFirebaseScryptPublicVectors runs the known values of the firebase/scrypt test suite (Appendix C
// §C.5.3): tests/01-known-value.sh, and the 13 lines of tests/02-more-values-passwords.good and of
// tests/03-more-values-different-key-passwords.good (same plaintexts and salts, another signer). The
// public vectors are all ASCII; the non-ASCII and real-export check is the dev-project fixture user
// (owner item, C.5.3).
func TestFirebaseScryptPublicVectors(t *testing.T) {
	sets := publicVectors(t)
	// A truncated or reordered file must fail rather than quietly test less.
	want := []struct {
		name string
		n    int
	}{{"01-known-value", 1}, {"02-more-values", 13}, {"03-other-signer", 13}}
	if len(sets) != len(want) {
		t.Fatalf("%s: %d sets, want %d", vectorsFile, len(sets), len(want))
	}
	for i, set := range sets {
		if set.name != want[i].name || len(set.vectors) != want[i].n {
			t.Fatalf("set %d: %s with %d vectors, want %s with %d", i+1, set.name, len(set.vectors), want[i].name, want[i].n)
		}
	}
	for _, set := range sets {
		t.Run(set.name, func(t *testing.T) {
			p, err := firebasescrypt.ParseParams(set.signer, testSaltSeparator, testRounds, testMemCost)
			if err != nil || p == nil {
				t.Fatalf("params: %v", err)
			}
			for i, v := range set.vectors {
				salt, want := decode(t, v.salt), decode(t, v.hash)
				if ok, err := firebasescrypt.Verify(v.plaintext, salt, want, *p); err != nil || !ok {
					t.Errorf("vector %d (%q): ok=%v err=%v", i+1, v.plaintext, ok, err)
				}
				if i == 0 { // one negative per set is enough; each verification costs a full scrypt
					if ok, _ := firebasescrypt.Verify(v.plaintext+"x", salt, want, *p); ok {
						t.Errorf("vector %d: another plaintext verified", i+1)
					}
				}
			}
		})
	}
}

// DummyVerify does the work of one verification and never panics on an empty or long password.
func TestDummyVerify(t *testing.T) {
	p := params(t)
	firebasescrypt.DummyVerify("", p)
	firebasescrypt.DummyVerify(strings.Repeat("x", 512), p)
}

// The export may use the standard or the URL-safe alphabet, padded or not.
func TestDecodeB64Alphabets(t *testing.T) {
	want := []byte{0xfb, 0xff, 0xbf, 0x01}
	for _, v := range []string{"+/+/AQ==", "+/+/AQ", "-_-_AQ==", "-_-_AQ"} {
		if got := decode(t, v); !bytes.Equal(got, want) {
			t.Errorf("%s -> %x", v, got)
		}
	}
	if _, err := firebasescrypt.DecodeB64("not base64!"); err == nil {
		t.Error("garbage must fail")
	}
}

func TestParseParams(t *testing.T) {
	if p, err := firebasescrypt.ParseParams("", "", 0, 0); p != nil || err != nil {
		t.Fatalf("unset parameters mean no legacy verification: %v %v", p, err)
	}
	signer := testSigner(t)
	for name, args := range map[string][4]any{
		"partial":     {"", "Bw==", 8, 14},
		"bad rounds":  {signer, "Bw==", 0, 14},
		"bad memcost": {signer, "Bw==", 8, 40},
		"bad base64":  {"%%%", "Bw==", 8, 14},
	} {
		if _, err := firebasescrypt.ParseParams(args[0].(string), args[1].(string), args[2].(int), args[3].(int)); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
