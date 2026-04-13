import { doc, setDoc } from "firebase/firestore";
import type { User } from "firebase/auth";
import { db } from "@/firebase/client";
import { COLLECTIONS } from "@/lib/collections";

export type LoginGeoCoords = {
    lat: number;
    lng: number;
    accuracyM?: number;
    source?: "gps" | "ip";
};

/**
 * Best-effort browser geolocation (user may deny or block without HTTPS).
 */
export async function fetchBrowserLoginGeo(): Promise<LoginGeoCoords | null> {
    if (typeof window === "undefined" || !navigator?.geolocation) return null;
    return new Promise((resolve) => {
        navigator.geolocation.getCurrentPosition(
            (pos) => {
                resolve({
                    lat: pos.coords.latitude,
                    lng: pos.coords.longitude,
                    accuracyM: pos.coords.accuracy,
                    source: "gps",
                });
            },
            () => resolve(null),
            { enableHighAccuracy: false, maximumAge: 300_000, timeout: 15_000 },
        );
    });
}

/**
 * Approximate lat/lng from public IP (no browser permission). City-level; used when GPS is unavailable.
 */
export async function fetchApproxLocationFromIp(): Promise<LoginGeoCoords | null> {
    if (typeof window === "undefined") return null;
    const ctrl = new AbortController();
    const timer = window.setTimeout(() => ctrl.abort(), 6000);
    try {
        const r = await fetch("https://ipwho.is/json/", { signal: ctrl.signal });
        if (!r.ok) return null;
        const j = (await r.json()) as { success?: boolean; latitude?: unknown; longitude?: unknown };
        if (j.success === false) return null;
        const lat = Number(j.latitude);
        const lng = Number(j.longitude);
        if (!Number.isFinite(lat) || !Number.isFinite(lng)) return null;
        if (Math.abs(lat) > 90 || Math.abs(lng) > 180) return null;
        return { lat, lng, source: "ip" };
    } catch {
        return null;
    } finally {
        window.clearTimeout(timer);
    }
}

/** Prefer GPS, then IP-based approximation. */
export async function resolveLoginGeoForClient(): Promise<LoginGeoCoords | null> {
    const gps = await fetchBrowserLoginGeo();
    if (gps) return gps;
    return fetchApproxLocationFromIp();
}

/**
 * Writes `users/{uid}.lastLogin` and optional sign-in coordinates as top-level
 * `lastLoginLat` / `lastLoginLng` (plus optional `lastLoginGeoSource`, accuracy).
 * Older docs may still have nested `lastLoginLocation`; readers should accept both.
 */
export async function updateUserLastLogin(user: User, geo?: LoginGeoCoords | null): Promise<void> {
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
