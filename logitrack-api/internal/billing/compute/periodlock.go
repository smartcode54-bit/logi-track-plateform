package compute

import (
	"fmt"
	"time"

	"github.com/smartcode54-bit/logi-track-plateform/logitrack-api/internal/platform/clock"
)

// Statement statuses that lock a billing period: a document already went to
// the customer (fn:core/billingPeriodLock.ts:23). draft and cancelled never
// lock.
const (
	StatementSent = "sent"
	StatementPaid = "paid"
)

// IsLockingStatus reports whether a statement status locks its period.
func IsLockingStatus(status string) bool {
	return status == StatementSent || status == StatementPaid
}

// LockedPeriod is an issued statement covering (party, year, month).
type LockedPeriod struct {
	PartyID       string
	Year          int
	Month         int // 1-12
	InvoiceNumber string
	Status        string
	GeneratedAt   time.Time // generated_at; the latest statement of a period wins
}

// PeriodKey is the legacy key of a (party, period) pair, month zero-padded so
// month 7 and month 70 never collide: "cust__2026-07"
// (billingPeriodKey, fn:core/billingPeriodLock.ts:34-36).
func PeriodKey(party string, year, month int) string {
	return fmt.Sprintf("%s__%d-%02d", party, year, month)
}

type periodKey struct {
	party       string
	year, month int
}

// PeriodLocks is the set of locked periods, for checks that run outside the
// pricing transaction's own FOR SHARE read (the backfill pre-check,
// fn:tripBillingOnDelivered.ts:1100-1111). The pricing paths read the lock
// from PostgreSQL in their transaction (§6.11, R17) and use PeriodOf for the
// key.
type PeriodLocks struct {
	byKey map[periodKey]LockedPeriod
}

// NewPeriodLocks indexes statements by period. Statuses other than sent and
// paid are ignored; party ids are trimmed; when several locking statements
// cover one period the latest generated_at supplies the invoice number (ties:
// the later one in the input, as the legacy map kept the last).
func NewPeriodLocks(statements []LockedPeriod) PeriodLocks {
	l := PeriodLocks{byKey: make(map[periodKey]LockedPeriod, len(statements))}
	for _, s := range statements {
		if !IsLockingStatus(s.Status) {
			continue
		}
		s.PartyID = trim(s.PartyID)
		if s.PartyID == "" {
			continue
		}
		k := periodKey{s.PartyID, s.Year, s.Month}
		if prev, ok := l.byKey[k]; ok && prev.GeneratedAt.After(s.GeneratedAt) {
			continue
		}
		l.byKey[k] = s
	}
	return l
}

// Len is the number of locked periods.
func (l PeriodLocks) Len() int { return len(l.byKey) }

// PeriodOf is the Bangkok (year, month) a billing instant belongs to.
func PeriodOf(bill time.Time) (year, month int) {
	return clock.YearMonth(bill)
}

// LockFor is the statement that blocks repricing a record of party billed at
// bill, if any (lockFor, fn:core/billingPeriodLock.ts:56-61). A blank party or
// an absent instant (the zero time, or the epoch the legacy 0 stood for) never
// blocks, so a fixable row stays fixable. The party id is trimmed.
func (l PeriodLocks) LockFor(party string, bill time.Time) (LockedPeriod, bool) {
	p := trim(party)
	if p == "" || bill.IsZero() || bill.UnixMilli() == 0 {
		return LockedPeriod{}, false
	}
	y, m := PeriodOf(bill)
	lock, ok := l.byKey[periodKey{p, y, m}]
	return lock, ok
}
