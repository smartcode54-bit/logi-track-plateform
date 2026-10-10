// Package compute is the billing engine: a pure port of
// web:lib/billingCompute.ts and fn:core/billingCompute.ts (identical but for
// their header comment) plus the pure decisions of
// fn:tripBillingOnDelivered.ts, fn:standbyBilling.ts and
// fn:core/billingPeriodLock.ts (main spec §6).
//
// Pure means no I/O and no clock: callers load rate tables, hub names and
// period locks inside their own transaction and pass them in. The only
// imports outside the standard library are the pure leaf packages
// platform/clock (Bangkok calendar) and platform/jsmath (JavaScript rounding).
// TestImportsArePure enforces it for compute, billing/documents, jsmath and
// clock: standard-library imports come from a pure allow-list (no os, io,
// net, syscall, os/exec or database packages), and time.Now, time.Since,
// time.Until, timers, time.Local and fmt printing are rejected.
//
// Money stays float64 and reproduces V8 bit for bit (§6.2): jsmath.Round, no
// fused multiply-add (every product is wrapped in float64(...), enforced by
// TestNoFusedMultiplyAdd), unrounded float sums kept unrounded in input order.
// The golden vectors in testdata/golden/billing are exported from the
// TypeScript engine and pin the parity; the deliberate differences are:
//
//   - R15: a blank vehicle class is no class at all (no_vehicle_class), never
//     a guessed 4WJ (legacy normalizeVehicleClass, billingCompute.ts:147).
//   - R16: equal effective instants are ordered explicitly (legacy doc id,
//     then created_at, then id) instead of by load order.
//   - R19: a trip without plan, delivery or creation instant is
//     no_billing_date, never priced at Date.now() (billingCompute.ts:261).
//   - R20: voided standby rates are never selected.
package compute
