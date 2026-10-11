// Package compute is the driver compensation engine: a pure port of
// web:lib/compensationCompute.ts and fn:core/compensationCompute.ts (identical
// but for their header comment) plus the per-driver assembly of
// fn:driverCompensation.ts generateDriverPayoutRun (main spec §6.15).
//
// Pure means no I/O and no clock: the payroll service (internal/hr) loads the
// compensation config, holidays, trips, tasks and open penalties inside its
// transaction and passes them in. The only imports outside the standard
// library are the pure leaf packages platform/clock (Bangkok calendar) and
// platform/jsmath (JavaScript rounding); TestImportsArePure and
// TestNoFusedMultiplyAdd (internal/billing/compute/purity_test.go) enforce it,
// as for the billing engine.
//
// Money stays float64 and reproduces V8 (§6.2): RoundTHB is jsmath.Round, the
// only rounding site; every product is wrapped in float64(...) so it cannot be
// fused into a multiply-add; sums are accumulated in array order. Payroll
// amounts are whole baht, so every sum is exact. The golden vectors in
// testdata/golden/hr are exported from the TypeScript engine and pin the
// parity of the ported functions.
//
// The assembly (Compute) differs from the legacy orchestrator only where the
// main spec decides so: trip pay is two line items, weekday and holiday
// (ADR 0013, §6.18 #23; their sum is the legacy "Trip pay"), every line
// carries its breakdown (quantity, unit rate, meta), and open penalties are
// deducted in (incurred_at, id) order (legacy: Firestore document order). The
// driver identity fix (§6.18 #21: own tasks by drivers.id, not the auth uid)
// lives in the service's queries, not here.
package compute
