# LINE Group Notifications — Setup Guide (Wanpenradchada OA)

The platform pushes a Thai message into a customer's / partner's **LINE group** automatically when a
driver **checks in** and when a **job completes**, sent from the **Wanpenradchada** LINE Official
Account. This guide covers the one-time setup the app cannot do for you.

> Feature ships **dark**: with no token and no `lineGroupId` configured, the callable is a silent
> no-op — nothing is sent, and check-in / delivery are never blocked. Turn it on per customer by
> filling in the group id, once the steps below are done.

## How it works (context)

- Mobile fires the callable `sendCustomerLineNotification` (region `asia-southeast1`) best-effort at
  check-in (`{ taskId, event: "checkin" }`) and at job-complete (`{ tripId, event: "delivered" }`).
- The callable resolves the destination group from the trip's linked customer/partner (`lineGroupId`),
  joins the driver's **Thai** name (`fullNameTh`) from the `drivers` collection, builds the message
  (SSOT: `logitrack-web/lib/lineMessage.ts`), and calls the LINE Messaging **push** API.
- Idempotency flags (`tasks.lineCheckinNotifiedAt`, `trip_records.lineDeliveredNotifiedAt`) prevent
  duplicate sends on retries.

## 1. Messaging API channel + access token

1. In the [LINE Developers console](https://developers.line.biz/), open the provider for the
   **Wanpenradchada** Official Account and ensure it has a **Messaging API channel** (not LINE Notify —
   LINE Notify is discontinued; we use Messaging API push).
2. Issue a **long-lived Channel access token** (Messaging API tab → "Channel access token").
3. In the OA settings, enable **"Allow bot to join group chats"** (a bot can only push to groups it
   is a member of).

## 2. Store the token as a Firebase Functions secret (NOT Firestore)

The token is a secret. Set it once per project — you run this (deploys/secrets are yours):

```bash
firebase functions:secrets:set LINE_CHANNEL_ACCESS_TOKEN
```

Paste the token when prompted. The callable binds it via `defineSecret("LINE_CHANNEL_ACCESS_TOKEN")`.
Re-deploy functions after setting/rotating it. Set it for each project you use (dev and prod).

## 3. Add the OA to each customer's LINE group

Invite the **Wanpenradchada** Official Account into the customer's / partner's LINE group. Until it is
a member, pushes to that group are rejected by LINE.

## 4. Capture the group id and paste it into the profile

LINE group ids (they start with `C…`) are **not shown in any console UI** — you must capture them from
a webhook event:

1. In the Messaging API channel, enable **Use webhook** and point it at a small webhook that logs the
   inbound event's `source.groupId` (a temporary Cloud Function, or any webhook receiver). A join
   event or any message the bot receives in the group carries `source.type === "group"` and
   `source.groupId`.
2. Trigger it (e.g. send any message in the group after inviting the bot), read the `groupId` from the
   log.
3. Paste that id into the customer/partner profile in the web admin:
   - **Customer** → Customers → edit → **LINE Notifications** card → `LINE Group ID`.
   - **Partner** → Subcontractors → edit → `LINE Group ID` field (Contact step on the create wizard).

Leave the field blank to keep notifications **off** for that entity.

## 5. Quotas

Check the OA plan's push-message quota (the free tier is limited). Two messages per job (check-in +
complete) multiply with delivery volume.

## Verifying end-to-end

1. Configure one test customer with a real group id (bot already in the group).
2. On a dev-flavor phone: check in a task for that customer → the check-in message appears in the
   group; complete the job → the job-complete message appears, with the driver's **Thai** name.
3. Re-running the same event must **not** duplicate the message (idempotency flags).
4. If the token is wrong/missing, the driver's check-in and delivery still succeed (best-effort) — the
   push just does not happen.

## Troubleshooting — messages not arriving

The callable is **best-effort and silent by design**: mobile swallows failures, and the callable
returns `{ ok: true, skipped: true, reason }` (no error) when it can't send. So "nothing arrives"
gives no signal by itself. Work the list below in order — step 1 gets you the exact reason without
any CLI or log access.

1. **Use the web "ส่งแจ้งเตือน LINE" button (fastest).** Open **Driver Monitor → a delivered trip →
   Edit** and click **ส่งแจ้งเตือน LINE** (Send LINE notification). Standby: **Standby Records → open a
   record → ส่งแจ้งเตือน LINE**. It calls the same callable with `force: true` and shows the real
   result in a toast:
   - *"ส่งแจ้งเตือนเข้ากลุ่ม LINE แล้ว"* → it works; the group/token/OA are all fine. If drivers still
     don't trigger it, the cause is the app path (see step 5).
   - *"ยังไม่ได้ตั้งค่า LINE token…"* → the secret isn't bound to the deployed function (step 2).
   - *"ลูกค้า/พาร์ทเนอร์นี้ยังไม่ได้ตั้งค่า LINE Group ID"* → fill the group id on the resolved entity (step 4).
   - a red error mentioning **LINE push failed / HTTP 401/403** → the token is wrong, or the
     Wanpenradchada OA is not a member of that group (step 3).

2. **Secret must be bound to the *deployed* revision (the common trap).** Setting the secret is not
   enough — a v2 function reads it at runtime, so you must **redeploy after setting it**:
   ```bash
   firebase functions:secrets:access LINE_CHANNEL_ACCESS_TOKEN --project <dev|prod>
   firebase deploy --only functions:sendCustomerLineNotification --project <dev|prod>
   ```
   Set the secret for **each** project (dev and prod) and redeploy each.

3. **The OA must be in the group, with a valid token.** The bot can only push to groups it has
   joined; enable "Allow bot to join group chats" and invite the Wanpenradchada OA into the group
   (setup steps 1 & 3 above). A wrong token or non-member group makes the push return 401/403.

4. **`lineGroupId` must sit on the entity the callable actually resolves.** Resolution precedence
   (`resolveLineTarget`, `functions/src/lineNotify.ts`): task `sourceHubLinkedCustomerId` →
   `destinationLinkedCustomerId` → trip `billingCustomerId`; standby resolves from
   `standby_records.customerId` **only** (skips silently when `customerResolved: false`). Put the
   group id on that customer/subcontractor doc.

5. **Read the function logs** to see the code's own diagnostics (`[lineNotify] … is not set —
   skipping send`, `… push failed` with the LINE HTTP status):
   ```bash
   firebase functions:log --only sendCustomerLineNotification --project <dev|prod>
   ```
   On the app side, the four call sites now log `[line] … skipped: <reason>` / `[line] … failed: …`
   (visible in `flutter run` / device logs) instead of swallowing the result.

6. **Idempotency.** Each record notifies once (`lineCheckinNotifiedAt` / `lineDeliveredNotifiedAt` /
   `lineNotifiedAt`). A second automatic attempt returns `already notified`; the web button passes
   `force: true` so it always re-sends.

> Note: jobs **closed from the web** (Driver Monitor → resolve a stuck job as *delivered*) now fire
> the delivered notification automatically, in addition to the manual button. Before this, only the
> mobile happy-path notified.

## Notes / future

- Origin/destination labels resolve hub `source_id → source_name_th`; SOC and unknown values pass
  through as-is.
- A scheduled safety-net sweep (retry pushes whose app-invoke failed) is a recommended follow-up,
  mirroring `autoComputeBillingOnDelivery` — not in the first cut.
- The `หมายเหตุ` (notes) line on the job-complete message is currently `-`; wiring an incident note
  is a future enhancement.
