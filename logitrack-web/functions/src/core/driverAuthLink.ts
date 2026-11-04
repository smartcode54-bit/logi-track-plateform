import * as admin from "firebase-admin";

/**
 * Build the Firestore update that re-links a driver doc to `newAuthId` while
 * preserving the previously-linked Auth UID in `authIdHistory`.
 *
 * Why: trip_records / vehicle_expenses / standby_records store whichever Auth
 * UID was current when the doc was written. When an email / Google re-link
 * moves the driver to a NEW uid, those old docs would otherwise resolve to no
 * driver and a report would show a raw id (e.g. `W47P8pGs…`). Keeping every
 * past uid in `authIdHistory` lets name resolution find them again — see
 * lib/driverName.ts `driverIdentityKeys` on the web side.
 *
 * Every server path that OVERWRITES `drivers/{doc}.authId` with a different uid
 * must route through this (updateDriverAccount, createUser-link, linkDriverToUser)
 * so no past uid is silently dropped.
 *
 * Pass the driver doc's current authId (already read from the doc) as
 * `currentAuthId`; it may be undefined for a freshly-created driver.
 */
export function buildDriverRelinkUpdate(
    currentAuthId: string | undefined | null,
    newAuthId: string
): Record<string, unknown> {
    const update: Record<string, unknown> = { authId: newAuthId };
    const prev = String(currentAuthId ?? "").trim();
    if (prev && prev !== newAuthId) {
        update.authIdHistory = admin.firestore.FieldValue.arrayUnion(prev);
    }
    return update;
}
