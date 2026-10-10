import { doc, setDoc } from "firebase/firestore";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";
import type { LoginGeoCoords } from "@/lib/loginGeo";

export type { LoginGeoCoords } from "@/lib/loginGeo";
export { fetchApproxLocationFromIp, fetchBrowserLoginGeo, resolveLoginGeoForClient } from "@/lib/loginGeo";

/** The Firebase user fields the legacy `users/{uid}` document carries. */
export interface LastLoginUser {
    uid: string;
    email: string | null;
    displayName: string | null;
}

/**
 * Writes `users/{uid}.lastLogin` and optional sign-in coordinates as top-level
 * `lastLoginLat` / `lastLoginLng` (plus optional `lastLoginGeoSource`, accuracy).
 * Older docs may still have nested `lastLoginLocation`; readers should accept both.
 *
 * Since T18 the sign-in happens at Go, which records `users.last_login_*`; this legacy document is
 * still written for bridged sessions (the user is signed in to Firebase with its own uid) because the
 * active-users list of the Security Center overview reads it until P6 (Appendix E §E.5 row 29).
 */
export async function updateUserLastLogin(user: LastLoginUser, geo?: LoginGeoCoords | null): Promise<void> {
    const iso = new Date().toISOString();
    try {
        const payload: Record<string, unknown> = {
            uid: user.uid,
            email: user.email ?? "",
            displayName: user.displayName ?? "",
            lastLogin: iso,
        };
        if (
            geo &&
            Number.isFinite(geo.lat) &&
            Number.isFinite(geo.lng) &&
            Math.abs(geo.lat) <= 90 &&
            Math.abs(geo.lng) <= 180
        ) {
            payload.lastLoginLat = geo.lat;
            payload.lastLoginLng = geo.lng;
            if (geo.source) payload.lastLoginGeoSource = geo.source;
            if (geo.accuracyM != null && Number.isFinite(geo.accuracyM)) {
                payload.lastLoginLocationAccuracyM = geo.accuracyM;
            }
        }
        await setDoc(doc(db, COLLECTIONS.USERS, user.uid), payload, { merge: true });
    } catch (e) {
        console.warn("[updateUserLastLogin]", e);
    }
}
