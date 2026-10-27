# Main feature sync — 2026-10-27

Status: Implemented; PR validation pending

## Source and scope

Sync the five main commits that mv-go `28e6c2e6` lacks, origin/main `30c32c72..11a358d5`:

| Commit | Main change | mv-go result |
|--------|-------------|--------------|
| `565c17af` | `updateDriverAccount` unlinks `google.com` when the driver's email changes; a failed Auth read stops the save | Merged (Cloud Function). Ported to Go `PATCH /v1/users/{id}` |
| `624eec20`, `cd8811a5` | Version bumps | Merged as main has them: root 3.6.1, web 3.6.3 |
| `e2e7e91b` | Audit page `pricePerLiter` input stops reformatting with `toFixed(2)` while the user types | mv-go page is the Go `ExpenseAuditPage`; `ExpenseDetailDialog` already keeps the field as typed text. Nothing to port |
| `11a358d5` | `drivers.authIdHistory` keeps the uids a re-link replaced; web name lookups and a backfill callable read it | Merged (callables, `lib/driverName.ts`, schema). Ported to the Go ETL |

The four Firestore files main edited were deleted in mv-go (`features/accounting/api/billing.ts`, `expenses.ts`,
`features/drivers/hooks/useDriverMonitor.ts`, `hooks/useDriverNamesByAuthId.ts`), so they stay deleted. The four
pages main edited are mv-go Go stubs (audit, incident reports, standby records, backfill utilities); mv-go's
version is kept. Their Go views name drivers through `driver_id`, not an auth uid.

## Implementation

- ETL `driverByRef` (`internal/etl/lookups.go`): after `legacy_doc_id` and `legacy_auth_uid`, match a past uid in
  the loaded driver document's `authIdHistory` (`etl.source_docs.raw`). The lookup covers every `driverId`-like
  reference: trips, expenses, standby, incidents, payroll and tenant stamping. It records `driver_ref_match`
  `auth_uid`, so no CHECK change, migration or new column is needed.
- IAM `PatchUser` (`internal/iam/users.go`): an email change deletes the user's Google identity (reusing
  `DeleteUserGoogleIdentity`). `user_updated` records `googleIdentityRemoved`. Without this, the new address's
  Google sign-in is `no_account` because the user is still linked to the old Google subject. The next Google
  sign-in with a Gmail or Workspace address links again by its verified email.

## Not ported (known gaps)

- The Firebase mirror (`MirrorEmailInTx`) updates the email but does not send `deleteProvider: ["google.com"]`.
  While the bridge mirror is on, a 3.x Google sign-in with the new address can still hit main's original bug.
- The shadow projection writes `drivers.authId` after a Go re-link but does not append the old uid to
  `authIdHistory`. main's web would then show raw uids on older trips of that driver.
- ETL re-resolution: a trip loaded before its driver's `authIdHistory` arrived keeps `driver_id` NULL until the
  trip document changes. A cut-over full load (drivers before trips) is unaffected.

## Validation results

- `TestDriverByRefResolvesAPastAuthUID` (new, PostgreSQL): doc id, current uid, past uid, another driver's uid and
  an unknown uid. Fails with the fallback removed.
- `TestUserAdministration`: an email change drops the Google identity, keeps the legacy uid, and records
  `googleIdentityRemoved: true`; a name-only change records `false`. Fails without the delete.
- Full checks: see the PR description.
