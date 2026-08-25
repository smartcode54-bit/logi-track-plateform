# ADR 0021 — Transactional email via a Google Workspace SMTP callable

- **Status:** Accepted (2026-08-25) — implementation pending
- **Deciders:** Samart Kas (product owner), Claude
- **Area:** logitrack-web Cloud Functions (`functions/src/`), logitrack-mobile (caller for warehouse-return email)

## Context

The Buzzebee distribution flow ([ADR 0020](0020-buzzebee-last-mile-distribution.md)) must **send email**: the
confirm-back manifest of accepted orders to Buzzebee, and the warehouse-return manifest to the warehouse and
Buzzebee when a driver submits a return. **The platform has no email-sending capability today** — exploration
found no mail dependency in any `package.json`, no Firebase "Trigger Email" extension, no `mail` collection, and
no SMTP/SendGrid/Resend code anywhere. This is greenfield.

Constraints from the existing architecture:

- **No Firestore document triggers.** The Firestore region `asia-southeast3` supports none; every reactive write
  is an app-invoked callable (glossary [[Denormalization (in this codebase)]]). So the Firebase Trigger-Email
  extension — which fires on a Firestore write — is a poor fit without standing up a second trigger-capable region.
- **Server logic convention.** All server business logic is an `onCall` callable in `functions/src/`, exported from
  `index.ts`, region `asia-southeast1` (`functions/src/index.ts:8-9`), called via `httpsCallable` — **not** a
  Next.js `app/api/` route. Callables set `enforceAppCheck: false` for web-admin calls and do a manual auth check.
- **Secrets pattern exists.** `GOOGLE_MAPS_API_KEY` is a `defineString` parameter read at runtime
  (`functions/src/distances.ts:22`); secrets live in Cloud env, never the client bundle.
- **Owner's choice (2026-08-25):** send via **SMTP through the company's Google Workspace** (an existing mailbox),
  rather than adopting a third-party email API provider now.

## Decision

1. **Send transactional email with `nodemailer` over Google Workspace SMTP**, authenticated by a Workspace mailbox
   using an **app password (or OAuth2)** supplied through Cloud env/secrets — never shipped to the client.

2. **Delivery mechanism is a single `onCall` callable, `sendTransactionalEmail`, in `functions/src/email.ts`**,
   exported from `functions/src/index.ts`, region `asia-southeast1`, `enforceAppCheck: false` with an explicit
   in-function auth check. It is **not** a Firestore trigger (region unsupported) and **not** an `app/api/` route
   (violates the server-logic convention). Callers pass a rendered `{ to, subject, html, attachments[] }`.

3. **Recipients are server-controlled, not caller-controlled.** Because one caller is the **mobile app** (a driver
   submitting a return), the callable must **not** relay to arbitrary addresses. Warehouse and Buzzebee addresses
   come from server-side config (e.g. `settings/*` or the customer/company doc); the client selects *which*
   configured recipient set, not a free-text address. This prevents the callable from becoming an open relay.

4. **Auth is per-caller.** Confirm-back email is **admin-only** (`request.auth.token.admin === true ||
   token.role === 'admin'`). The warehouse-return email is allowed for the **authenticated driver who owns that
   return** (verified against the `return_manifests` / task ownership), matching how mobile callables authenticate.

5. **The transport is a thin, provider-swappable module.** `email.ts` depends on a small
   `EmailTransport.send(msg)` interface; the SMTP implementation is one file behind it. Swapping to SendGrid/Resend
   later is a new implementation of the same interface — **callers never change**. This honours the owner's "keep
   it swappable" preference.

6. **Every send is auditable and best-effort idempotent.** On success the callable records `emailSentAt` +
   provider `messageId` onto the originating doc (`order_import_batches` for confirm-back, `return_manifests` for
   returns) via the shared `stripUndefined` write helper (`lib/firestoreWrite.ts`); a failed send is surfaced to
   the caller (`HttpsError`) and does not silently drop.

## Consequences

**Positive**
- Zero new third-party account; reuses an existing Workspace mailbox and the established callable + secret patterns.
- Provider abstraction means a future move to a dedicated ESP is a localized change.
- Server-controlled recipients + per-caller auth keep a mobile-invokable email path from being abused.

**Negative / risks**
- **Workspace SMTP has sending limits** (on the order of a couple thousand messages/day per account) and
  deliverability depends on the domain's SPF/DKIM/DMARC being correct — fine for manifests, not for bulk mail.
- App-password auth requires 2FA on the mailbox (or OAuth2 setup); the credential must be rotated and kept in Cloud
  secrets only.
- SMTP send latency inside a callable adds a few seconds; the mobile return-submit UX must treat email as a
  best-effort side effect (the return is saved first, email second), not block the driver on it.

**Follow-ups**
- If volume or deliverability ever outgrows Workspace SMTP, write a follow-up ADR to adopt an ESP behind the same
  `EmailTransport` interface.
- HTML templates for the two manifests (confirm-back, return) are a spec detail, not an architectural decision.

## Alternatives considered

- **Firebase "Trigger Email" extension.** Rejected: it is Firestore-triggered, and `asia-southeast3` supports no
  document triggers — adopting it would require standing up a second trigger-capable database/region just for mail.
- **A Next.js `app/api/` route.** Rejected: the project rule is that server business logic and secrets live in
  Cloud callables, not API routes.
- **Send SMTP directly from the mobile app.** Rejected: the SMTP credential cannot live on a device, and recipient
  control must be server-side.
- **Adopt SendGrid/Resend as the primary now.** Deferred, not forbidden: the owner chose the existing Workspace
  mailbox; decision 5 keeps the swap cheap if that changes.

## Related

- Consumer of this transport: [ADR 0020](0020-buzzebee-last-mile-distribution.md) (confirm-back + return manifests).
- Spec: `shared-docs/specs/buzzebee-distribution-last-mile.md`.
- Patterns reused: `functions/src/index.ts:8-9` (region), `functions/src/distances.ts:22` (secret via `defineString`),
  `lib/firestoreWrite.ts` (`stripUndefined`).
