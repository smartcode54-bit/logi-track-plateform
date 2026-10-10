// Package password hashes and checks passwords (Appendix C §C.4.8): Argon2id PHC strings through
// alexedwards/argon2id, parameters read back from the stored hash so a login with weaker stored
// parameters re-hashes, and the password policy (length, NFC, not the email local part, not the mobile
// digits, not a common password). No composition rules, no periodic expiry.
package password

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/alexedwards/argon2id"
	"golang.org/x/text/unicode/norm"
)

// Params are ARGON2_MEMORY_KB, ARGON2_ITERATIONS and ARGON2_PARALLELISM.
type Params struct {
	MemoryKB    uint32
	Iterations  uint32
	Parallelism uint8
}

const (
	saltLength = 16 // bytes (C.4.8)
	keyLength  = 32 // bytes
	// MaxLength bounds the input both to the policy (128 characters) and, in bytes, to what any hash is
	// ever computed over, so a huge body cannot buy CPU.
	MaxLength = 128
	// MaxBytes is that bound in bytes (UTF-8 needs at most 4 per character).
	MaxBytes = MaxLength * 4
)

// SlotWait bounds how long a hash waits for a free slot before it gives up with ErrBusy.
const SlotWait = 3 * time.Second

// ErrBusy means every hashing slot stayed taken for SlotWait: the caller answers 503 unavailable.
var ErrBusy = errors.New("password: hashing capacity exhausted")

// Hasher creates and verifies Argon2id hashes with the configured parameters. Every memory-hard
// computation of the process (Argon2id here, the legacy Firebase scrypt through Gate) holds one of a
// fixed number of slots, so unauthenticated logins cannot allocate ARGON2_MEMORY_KB per request without
// bound: GOMAXPROCS / ARGON2_PARALLELISM slots (at least one), since more concurrent hashes than cores
// add memory without adding throughput.
type Hasher struct {
	params *argon2id.Params
	dummy  string
	slots  chan struct{}
}

// NewHasher validates p and precomputes the hash used by DummyVerify.
func NewHasher(p Params) (*Hasher, error) {
	if p.MemoryKB < 8*1024 || p.Iterations < 1 || p.Parallelism < 1 {
		return nil, errors.New("password: ARGON2_MEMORY_KB must be at least 8192, ARGON2_ITERATIONS and ARGON2_PARALLELISM at least 1")
	}
	h := &Hasher{params: &argon2id.Params{
		Memory: p.MemoryKB, Iterations: p.Iterations, Parallelism: p.Parallelism,
		SaltLength: saltLength, KeyLength: keyLength,
	}, slots: make(chan struct{}, max(1, runtime.GOMAXPROCS(0)/int(p.Parallelism)))}
	d, err := argon2id.CreateHash("logitrack-dummy-password", h.params)
	if err != nil {
		return nil, fmt.Errorf("password: %w", err)
	}
	h.dummy = d
	return h, nil
}

// Capacity is the number of hashes that may run at once.
func (h *Hasher) Capacity() int { return cap(h.slots) }

// Gate runs fn while it holds one hashing slot. It waits at most SlotWait (ErrBusy) and returns the
// context's error when ctx ends first; fn then does not run. fn must not call another gated method.
func (h *Hasher) Gate(ctx context.Context, fn func()) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	t := time.NewTimer(SlotWait)
	defer t.Stop()
	select {
	case h.slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return ErrBusy
	}
	defer func() { <-h.slots }()
	fn()
	return nil
}

// Hash returns the PHC string of the NFC form of pw.
func (h *Hasher) Hash(ctx context.Context, pw string) (string, error) {
	if len(pw) > MaxBytes {
		return "", errors.New("password: too long")
	}
	var s string
	var err error
	if gerr := h.Gate(ctx, func() { s, err = argon2id.CreateHash(Normalize(pw), h.params) }); gerr != nil {
		return "", gerr
	}
	if err != nil {
		return "", fmt.Errorf("password: %w", err)
	}
	return s, nil
}

// Verify compares the NFC form of pw with a stored PHC string. needsRehash is true when the stored
// parameters are weaker than the configured ones (memory, iterations, salt or key length). The only
// errors are an unreadable stored hash, ErrBusy and the context's error.
func (h *Hasher) Verify(ctx context.Context, pw, phc string) (ok, needsRehash bool, err error) {
	if len(pw) > MaxBytes {
		return false, false, h.DummyVerify(ctx, pw)
	}
	var match bool
	var stored *argon2id.Params
	if gerr := h.Gate(ctx, func() { match, stored, err = argon2id.CheckHash(Normalize(pw), phc) }); gerr != nil {
		return false, false, gerr
	}
	if err != nil {
		return false, false, fmt.Errorf("password: stored hash: %w", err)
	}
	if !match {
		return false, false, nil
	}
	weaker := stored.Memory < h.params.Memory || stored.Iterations < h.params.Iterations ||
		stored.SaltLength < h.params.SaltLength || stored.KeyLength < h.params.KeyLength
	return true, weaker, nil
}

// DummyVerify spends the time of one verification so an unknown email, or a user without a usable
// password, answers no faster than a wrong password (C.4.8: no account enumeration by timing). Its
// only errors are ErrBusy and the context's error.
func (h *Hasher) DummyVerify(ctx context.Context, pw string) error {
	if len(pw) > MaxBytes {
		pw = pw[:MaxBytes]
	}
	return h.Gate(ctx, func() { _, _, _ = argon2id.CheckHash(Normalize(pw), h.dummy) })
}

// Normalize is the NFC form of pw; Argon2id hashes are computed over it.
func Normalize(pw string) string { return norm.NFC.String(pw) }

// Violation is a policy failure; Reason is a stable code for the error details.
type Violation struct {
	Reason string // too_short, too_long, equals_email, equals_mobile, too_common, invalid_utf8
	Min    int
	Max    int
}

func (v *Violation) Error() string { return "password policy: " + v.Reason }

//go:embed common.txt
var commonList string

// Policy is the password policy of C.4.8.
type Policy struct {
	MinLength int
	common    map[string]struct{}
}

// NewPolicy builds the policy with PASSWORD_MIN_LENGTH (default 10) and the embedded common list.
func NewPolicy(minLength int) (Policy, error) {
	if minLength < 8 || minLength > MaxLength {
		return Policy{}, fmt.Errorf("password: PASSWORD_MIN_LENGTH must be between 8 and %d", MaxLength)
	}
	p := Policy{MinLength: minLength, common: map[string]struct{}{}}
	sc := bufio.NewScanner(strings.NewReader(commonList))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		p.common[strings.ToLower(Normalize(line))] = struct{}{}
	}
	return p, nil
}

// Check applies the policy to a new password. email is the user's address (its local part is
// forbidden) and mobiles are phone numbers whose digits the password must not equal.
func (p Policy) Check(pw, email string, mobiles ...string) *Violation {
	if !utf8.ValidString(pw) {
		return &Violation{Reason: "invalid_utf8"}
	}
	n := Normalize(pw)
	length := utf8.RuneCountInString(n)
	switch {
	case length < p.MinLength:
		return &Violation{Reason: "too_short", Min: p.MinLength, Max: MaxLength}
	case length > MaxLength:
		return &Violation{Reason: "too_long", Min: p.MinLength, Max: MaxLength}
	}
	lower := strings.ToLower(n)
	if local, _, ok := strings.Cut(strings.ToLower(email), "@"); ok && local != "" && lower == local {
		return &Violation{Reason: "equals_email"}
	}
	if digits, ok := phoneDigits(n); ok {
		for _, m := range mobiles {
			if d := onlyDigits(m); d != "" && localForm(d) == localForm(digits) {
				return &Violation{Reason: "equals_mobile"}
			}
		}
	}
	if _, bad := p.common[lower]; bad {
		return &Violation{Reason: "too_common"}
	}
	return nil
}

// phoneDigits returns the digits of s when s is written like a phone number: digits and the
// separators space, '-', '+', '(' and ')' only.
func phoneDigits(s string) (string, bool) {
	for _, r := range s {
		if (r < '0' || r > '9') && !strings.ContainsRune(" -+()", r) {
			return "", false
		}
	}
	d := onlyDigits(s)
	return d, d != ""
}

func onlyDigits(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// localForm folds the Thai country code: 66812345678 and 0812345678 are the same mobile number.
func localForm(d string) string {
	if strings.HasPrefix(d, "66") && len(d) == 11 {
		return "0" + d[2:]
	}
	return d
}
