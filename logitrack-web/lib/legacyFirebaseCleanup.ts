/**
 * Clears what the pre-TW7 web left of Firebase in this browser (TW7, issue #87; Appendix C §C.6.3 as
 * built; developer-spec.md §10.13).
 *
 * Until TW7 the web signed in to Firebase with a bridge custom token (`POST /api/auth/firebase-token`)
 * and the SDK kept that session in IndexedDB (`firebaseLocalStorageDb`): the legacy claims (`admin`,
 * `role`, `customerScopeId`, ...) and a refresh token that never expires on its own. The pre-TW7
 * AuthProvider signed it out on logout, on a session end and for a signed-out visitor. The web has no
 * SDK any more, so it cannot call `signOut()`; it deletes the stores instead:
 * - the IndexedDB databases the Firebase JS SDK keeps per origin (`LEGACY_FIREBASE_DATABASES`: the auth
 *   session, the App Check token, the heartbeat and installations records);
 * - every `localStorage` / `sessionStorage` key starting with `firebase:` (the auth SDK's fallback
 *   persistence and its redirect state).
 *
 * context/auth.tsx calls it on logout and on every session end, and once per browser on load (a signed-in
 * or signed-out visitor alike: no page reads a Firebase session any more). Deleting removes only the local
 * copy, as Firebase `signOut()` did: a refresh token already copied elsewhere stays valid until the account
 * mirror sets `validSince` (Appendix C §C.6.4), the operator step of §C.6.6 for a deployment that ran the
 * bridge's `web` mode. Transition code: it can go once no browser can hold a pre-TW7 session (at the latest
 * when the Firebase accounts go, P8).
 *
 * Never throws: storage may be missing or blocked (private mode, cleared or blocked site data).
 */

/** The IndexedDB databases the Firebase JS SDK (auth, App Check, heartbeat, installations) opens per origin. */
export const LEGACY_FIREBASE_DATABASES = [
    "firebaseLocalStorageDb",
    "firebase-app-check-database",
    "firebase-heartbeat-database",
    "firebase-installations-database",
] as const;

/** The prefix of the Firebase SDK's `localStorage` / `sessionStorage` keys (`firebase:authUser:*`, ...). */
export const LEGACY_FIREBASE_KEY_PREFIX = "firebase:";

/** Set once this browser was cleaned on load, so the load-time pass does not repeat on every page. */
export const LEGACY_FIREBASE_CLEARED_KEY = "lt:legacyFirebaseCleared";

function storageOf(name: "localStorage" | "sessionStorage"): Storage | null {
    try {
        return (globalThis as unknown as Record<string, Storage | undefined>)[name] ?? null;
    } catch {
        return null; // the accessor throws where site data is blocked
    }
}

function removeFirebaseKeys(storage: Storage | null): void {
    if (!storage) return;
    try {
        const keys: string[] = [];
        for (let i = 0; i < storage.length; i++) {
            const key = storage.key(i);
            if (key?.startsWith(LEGACY_FIREBASE_KEY_PREFIX)) keys.push(key);
        }
        for (const key of keys) storage.removeItem(key);
    } catch {
        // blocked storage: nothing was persisted there either
    }
}

function deleteDatabases(): void {
    let idb: IDBFactory | undefined;
    try {
        idb = globalThis.indexedDB;
    } catch {
        return;
    }
    if (!idb) return;
    for (const name of LEGACY_FIREBASE_DATABASES) {
        try {
            const request = idb.deleteDatabase(name);
            // A tab of the old build that still has the database open blocks the deletion; it completes on
            // its own once that tab closes, so neither outcome is waited for. Deleting a database that does
            // not exist succeeds.
            request.onblocked = () => undefined;
            request.onerror = () => undefined;
        } catch {
            // IndexedDB refused (private mode): the SDK could not have persisted there either
        }
    }
}

/**
 * Deletes the pre-TW7 Firebase session and SDK state of this origin. With `once`, it does nothing when
 * this browser was already cleaned on an earlier load; logout and session ends always clean.
 */
export function clearLegacyFirebaseSession(options: { once?: boolean } = {}): void {
    if (typeof window === "undefined") return;
    const local = storageOf("localStorage");
    if (options.once) {
        try {
            if (local?.getItem(LEGACY_FIREBASE_CLEARED_KEY) === "1") return;
        } catch {
            // unreadable flag: clean again, it is idempotent
        }
    }
    deleteDatabases();
    removeFirebaseKeys(local);
    removeFirebaseKeys(storageOf("sessionStorage"));
    try {
        local?.setItem(LEGACY_FIREBASE_CLEARED_KEY, "1");
    } catch {
        // blocked storage: the next load cleans again
    }
}
