package auth

import (
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/authdb"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/firebasescrypt"
	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/auth/password"
)

// Every failed password check costs the same memory-hard work whichever credential the account holds,
// so login timing reveals neither whether an email has an account nor whether an imported account has
// signed in since the import (Appendix C §C.4.8, §C.9.3). The work is counted per computation, which is
// deterministic where a wall-clock comparison would be flaky.
func TestEveryFailedCheckCostsTheSameWork(t *testing.T) {
	ctx := context.Background()
	hasher, err := password.NewHasher(password.Params{MemoryKB: 8 * 1024, Iterations: 1, Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	// The public firebase/scrypt test signer key with a small mem_cost: the count, not the cost, is tested.
	sp, err := firebasescrypt.ParseParams("jxspr8Ki0RYycVU8zykbdLGjFQ3McFUH0uiiTvC8pVMXAn210wjLNmdZJzxUECKbm0QsEmYUSDzZvpjeJ9WmXA==",
		"Bw==", 8, 4)
	if err != nil {
		t.Fatal(err)
	}
	const right = "the right password"
	argon, err := hasher.Hash(ctx, right)
	if err != nil {
		t.Fatal(err)
	}
	salt := []byte("0123456789abcdef")
	legacy, err := firebasescrypt.Hash(right, salt, *sp)
	if err != nil {
		t.Fatal(err)
	}
	bad := "$2a$10$not-an-argon2id-hash"
	id := uuid.New()
	argonUser := &authdb.GetUserForLoginRow{ID: id, Status: "active", PasswordHash: &argon}
	legacyUser := &authdb.GetUserForLoginRow{ID: id, Status: "active", LegacyScryptHash: legacy, LegacyScryptSalt: salt}

	type counts = map[string]int
	both, argonOnly, scryptOnly := counts{kdfArgon2id: 1, kdfScrypt: 1}, counts{kdfArgon2id: 1}, counts{kdfScrypt: 1}
	cases := []struct {
		name   string
		scrypt bool
		pw     string
		u      *authdb.GetUserForLoginRow
		ok     bool
		want   counts
	}{
		{"unknown email", true, "whatever it is", nil, false, both},
		{"google-only user", true, "whatever it is", &authdb.GetUserForLoginRow{ID: id, Status: "active"}, false, both},
		{"reset_required tail", true, "whatever it is", &authdb.GetUserForLoginRow{ID: id, Status: "reset_required"}, false, both},
		{"argon2id user, wrong password", true, "a wrong password", argonUser, false, both},
		{"argon2id user, overlong password", true, strings.Repeat("x", 2000), argonUser, false, both},
		{"unreadable stored hash", true, right, &authdb.GetUserForLoginRow{ID: id, Status: "active", PasswordHash: &bad}, false, both},
		{"legacy user, wrong password", true, "a wrong password", legacyUser, false, both},
		{"no FIREBASE_SCRYPT_*: unknown email", false, "whatever it is", nil, false, argonOnly},
		{"no FIREBASE_SCRYPT_*: legacy user", false, right, legacyUser, false, argonOnly},
		{"no FIREBASE_SCRYPT_*: argon2id user, wrong password", false, "a wrong password", argonUser, false, argonOnly},
		{"argon2id user, right password", true, right, argonUser, true, argonOnly},
		{"legacy user, right password", true, right, legacyUser, true, scryptOnly},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := counts{}
			s := &Service{hasher: hasher, log: zerolog.Nop(), kdf: func(k string) { got[k]++ }}
			if tc.scrypt {
				s.cfg.Scrypt = sp
			}
			ok, rehash, err := s.verify(ctx, tc.pw, tc.u)
			if err != nil || ok != tc.ok {
				t.Fatalf("verify: ok=%v err=%v, want ok=%v", ok, err, tc.ok)
			}
			if !maps.Equal(got, tc.want) {
				t.Fatalf("memory-hard work %v, want %v", got, tc.want)
			}
			if tc.u == legacyUser && tc.ok && !strings.HasPrefix(rehash, "$argon2id$") {
				t.Fatalf("a verified legacy hash is re-hashed: %q", rehash)
			}
		})
	}
}

// A cancelled request stops before any hashing and is reported, never counted as a wrong password.
func TestVerifyReportsCancellation(t *testing.T) {
	hasher, err := password.NewHasher(password.Params{MemoryKB: 8 * 1024, Iterations: 1, Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := &Service{hasher: hasher, log: zerolog.Nop()}
	if ok, _, err := s.verify(ctx, "whatever it is", nil); ok || err == nil {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}
