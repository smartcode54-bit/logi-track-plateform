// TW7 transition (issue #87; Appendix C §C.6.3 as built): the web holds no Firebase SDK, so the session a
// pre-TW7 build persisted in this browser is deleted from storage instead of signed out. IndexedDB is a
// fake (jsdom has none); localStorage and sessionStorage are jsdom's.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
    LEGACY_FIREBASE_CLEARED_KEY,
    LEGACY_FIREBASE_DATABASES,
    clearLegacyFirebaseSession,
} from "./legacyFirebaseCleanup";
import { fakeIndexedDB } from "@/test-utils/fakeIndexedDB";

const AUTH_USER_KEY = "firebase:authUser:sentinel-api-key:[DEFAULT]";

beforeEach(() => {
    window.localStorage.clear();
    window.sessionStorage.clear();
});
afterEach(() => {
    vi.unstubAllGlobals();
});

describe("clearLegacyFirebaseSession", () => {
    it("deletes the Firebase SDK's databases and only its firebase: keys, then remembers it ran", () => {
        const idb = fakeIndexedDB();
        vi.stubGlobal("indexedDB", idb);
        window.localStorage.setItem(AUTH_USER_KEY, JSON.stringify({ uid: "legacy-uid", stsTokenManager: { refreshToken: "x" } }));
        window.localStorage.setItem("firebase:persistence:sentinel-api-key:[DEFAULT]", "local");
        window.localStorage.setItem("lt:lastRefreshAt", "1");
        window.localStorage.setItem("language", "th");
        window.sessionStorage.setItem("firebase:redirectEvent:sentinel-api-key:[DEFAULT]", "{}");
        window.sessionStorage.setItem("lt:draft", "keep");

        clearLegacyFirebaseSession();

        expect(idb.deleted).toEqual([...LEGACY_FIREBASE_DATABASES]);
        expect(idb.deleted).toContain("firebaseLocalStorageDb");
        // A blocked deletion (an old-build tab still open) or an error is never left unhandled.
        for (const r of idb.requests) {
            expect(typeof r.onblocked).toBe("function");
            expect(typeof r.onerror).toBe("function");
        }
        expect(Object.keys(window.localStorage).sort()).toEqual(["language", "lt:lastRefreshAt", LEGACY_FIREBASE_CLEARED_KEY].sort());
        expect(Object.keys(window.sessionStorage)).toEqual(["lt:draft"]);
        expect(window.localStorage.getItem(LEGACY_FIREBASE_CLEARED_KEY)).toBe("1");
    });

    it("once: a browser already cleaned on an earlier load is not cleaned again; without once it always is", () => {
        const idb = fakeIndexedDB();
        vi.stubGlobal("indexedDB", idb);
        clearLegacyFirebaseSession({ once: true });
        expect(idb.deleted).toHaveLength(LEGACY_FIREBASE_DATABASES.length);

        // An old-build tab wrote the session back: the load-time pass leaves it, logout and session ends do not.
        window.localStorage.setItem(AUTH_USER_KEY, "{}");
        clearLegacyFirebaseSession({ once: true });
        expect(idb.deleted).toHaveLength(LEGACY_FIREBASE_DATABASES.length);
        expect(window.localStorage.getItem(AUTH_USER_KEY)).toBe("{}");

        clearLegacyFirebaseSession();
        expect(idb.deleted).toHaveLength(2 * LEGACY_FIREBASE_DATABASES.length);
        expect(window.localStorage.getItem(AUTH_USER_KEY)).toBeNull();
    });

    it("never throws: no IndexedDB, a refused deletion, blocked storage", () => {
        window.localStorage.setItem(AUTH_USER_KEY, "{}");
        // jsdom has no IndexedDB: the keys still go.
        expect(() => clearLegacyFirebaseSession()).not.toThrow();
        expect(window.localStorage.getItem(AUTH_USER_KEY)).toBeNull();

        // One database refused: the others are still deleted.
        const idb = fakeIndexedDB({ throwOn: "firebaseLocalStorageDb" });
        vi.stubGlobal("indexedDB", idb);
        expect(() => clearLegacyFirebaseSession()).not.toThrow();
        expect(idb.deleted).toEqual(LEGACY_FIREBASE_DATABASES.filter((n) => n !== "firebaseLocalStorageDb"));

        // Storage whose every call throws (blocked site data).
        const blocked = {
            get length(): number {
                throw new DOMException("blocked", "SecurityError");
            },
            key: () => {
                throw new DOMException("blocked", "SecurityError");
            },
            getItem: () => {
                throw new DOMException("blocked", "SecurityError");
            },
            setItem: () => {
                throw new DOMException("blocked", "SecurityError");
            },
            removeItem: () => {
                throw new DOMException("blocked", "SecurityError");
            },
        };
        vi.stubGlobal("localStorage", blocked);
        vi.stubGlobal("sessionStorage", blocked);
        expect(() => clearLegacyFirebaseSession({ once: true })).not.toThrow();
        expect(() => clearLegacyFirebaseSession()).not.toThrow();
    });
});
