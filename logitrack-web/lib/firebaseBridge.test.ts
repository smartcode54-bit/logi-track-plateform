// T18 (R8, R40, R80; Appendix C §C.6.3): the web side of the Firebase bridge. The Firebase SDK and the
// BFF mint are fakes, so the tests count mints and sign-ins exactly.
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { Auth, IdTokenResult, User, UserCredential } from "firebase/auth";

import { ApiError } from "./apiError";
import {
    BRIDGE_DENIED_KEY,
    BRIDGE_RETRY_DELAYS_MS,
    bridgeSettledFor,
    configureFirebaseBridge,
    getBridgeState,
    signOutFirebaseBridge,
    syncFirebaseBridge,
    type BridgeDeps,
} from "./firebaseBridge";

interface FakeUser {
    uid: string;
    claims: Record<string, unknown>;
}

function fakeSdk(opts: { current?: FakeUser | null; mint?: () => Promise<{ customToken: string }> } = {}) {
    const state = {
        current: opts.current ?? null,
        mints: 0,
        signIns: [] as string[],
        signOuts: 0,
        sleeps: [] as number[],
    };
    const toUser = (u: FakeUser) => ({ uid: u.uid, emailVerified: true, metadata: { creationTime: "2026-01-01", lastSignInTime: "2026-10-10" } }) as unknown as User;
    const auth = {
        authStateReady: async () => undefined,
        get currentUser() {
            return state.current ? toUser(state.current) : null;
        },
    } as unknown as Auth;
    const deps: BridgeDeps = {
        auth,
        signInWithCustomToken: async (_a, token) => {
            state.signIns.push(token);
            // The custom token carries "uid|role" in these tests.
            const [uid, role] = token.split("|");
            state.current = { uid, claims: { admin: role === "admin", role } };
            return { user: toUser(state.current) } as unknown as UserCredential;
        },
        signOut: async () => {
            state.signOuts += 1;
            state.current = null;
        },
        getIdTokenResult: async (user) => {
            const claims = state.current && state.current.uid === user.uid ? state.current.claims : {};
            return { claims } as unknown as IdTokenResult;
        },
        mint:
            opts.mint ??
            (async () => {
                state.mints += 1;
                return { customToken: "fb-1|admin" };
            }),
        sleep: async (ms) => {
            state.sleeps.push(ms);
        },
    };
    return { state, deps };
}

beforeEach(() => {
    window.sessionStorage.clear();
});
afterEach(() => {
    configureFirebaseBridge(undefined);
});

describe("syncFirebaseBridge", () => {
    it("mints once per forced sign-in and exposes the legacy claims of the Firebase ID token", async () => {
        const { state, deps } = fakeSdk();
        configureFirebaseBridge(async () => deps);
        await syncFirebaseBridge({ id: "u1", legacyAuthUid: "fb-1" }, { force: true });
        expect(state.mints).toBe(1);
        expect(state.signIns).toEqual(["fb-1|admin"]);
        const s = getBridgeState();
        expect(s).toMatchObject({ status: "ready", userId: "u1", firebaseUid: "fb-1", claims: { admin: true, role: "admin" } });
        expect(s.profile).toEqual({ emailVerified: true, creationTime: "2026-01-01", lastSignInTime: "2026-10-10" });
        expect(bridgeSettledFor(s, "u1")).toBe(true);

        // An unforced sync for the same user (the page load after the sign-in) mints nothing.
        await syncFirebaseBridge({ id: "u1", legacyAuthUid: "fb-1" });
        expect(state.mints).toBe(1);
    });

    it("keeps a restored Firebase session of the same uid without minting (no per-load setAdminClaims)", async () => {
        const { state, deps } = fakeSdk({ current: { uid: "fb-1", claims: { role: "manager", admin: false } } });
        configureFirebaseBridge(async () => deps);
        await syncFirebaseBridge({ id: "u1", legacyAuthUid: "fb-1" });
        expect(state.mints).toBe(0);
        expect(getBridgeState()).toMatchObject({ status: "ready", claims: { role: "manager" } });
    });

    it("mints when the Firebase uid differs from ['me'].legacyAuthUid (another user's session)", async () => {
        const { state, deps } = fakeSdk({ current: { uid: "someone-else", claims: {} } });
        configureFirebaseBridge(async () => deps);
        await syncFirebaseBridge({ id: "u1", legacyAuthUid: "fb-1" });
        expect(state.mints).toBe(1);
        expect(getBridgeState().firebaseUid).toBe("fb-1");
    });

    it("a refused principal (403) holds no Firebase session, and the tab does not ask again", async () => {
        let calls = 0;
        const { state, deps } = fakeSdk({
            current: { uid: "stale", claims: {} },
            mint: async () => {
                calls += 1;
                throw new ApiError({ status: 403, code: "permission_denied", message: "" });
            },
        });
        configureFirebaseBridge(async () => deps);
        await syncFirebaseBridge({ id: "dispatcher" });
        expect(getBridgeState()).toMatchObject({ status: "none", userId: "dispatcher" });
        expect(state.signOuts).toBe(1);
        expect(window.sessionStorage.getItem(BRIDGE_DENIED_KEY)).toBe("dispatcher");

        configureFirebaseBridge(async () => deps); // a reload of the tab
        await syncFirebaseBridge({ id: "dispatcher" });
        expect(calls).toBe(1);
        expect(getBridgeState().status).toBe("none");
    });

    it("404 (bridge mode not web) is handled like a refusal", async () => {
        const { deps } = fakeSdk({ mint: async () => Promise.reject(new ApiError({ status: 404, code: "not_found", message: "" })) });
        configureFirebaseBridge(async () => deps);
        await syncFirebaseBridge({ id: "u1" });
        expect(getBridgeState().status).toBe("none");
    });

    it("retries a mint that failed on the network or a 5xx, then settles as error", async () => {
        let calls = 0;
        const { state, deps } = fakeSdk({
            mint: async () => {
                calls += 1;
                throw new ApiError({ status: 503, code: "unavailable", message: "" });
            },
        });
        configureFirebaseBridge(async () => deps);
        await syncFirebaseBridge({ id: "u1" });
        expect(calls).toBe(BRIDGE_RETRY_DELAYS_MS.length + 1);
        expect(state.sleeps).toEqual(BRIDGE_RETRY_DELAYS_MS);
        expect(getBridgeState()).toMatchObject({ status: "error", userId: "u1" });
        expect(bridgeSettledFor(getBridgeState(), "u1")).toBe(true);
    });

    it("signs out of Firebase on logout and forgets the refusal", async () => {
        const { state, deps } = fakeSdk();
        configureFirebaseBridge(async () => deps);
        await syncFirebaseBridge({ id: "u1", legacyAuthUid: "fb-1" }, { force: true });
        window.sessionStorage.setItem(BRIDGE_DENIED_KEY, "x");
        await signOutFirebaseBridge();
        expect(state.signOuts).toBe(1);
        expect(state.current).toBeNull();
        expect(getBridgeState()).toEqual({ status: "idle" });
        expect(window.sessionStorage.getItem(BRIDGE_DENIED_KEY)).toBeNull();
    });

    it("serialises concurrent calls: a forced mint after claims_changed runs after the pending one", async () => {
        let n = 0;
        const { state, deps } = fakeSdk({
            mint: async () => {
                n += 1;
                state.mints += 1;
                return { customToken: `fb-1|${n === 1 ? "manager" : "admin"}` };
            },
        });
        configureFirebaseBridge(async () => deps);
        const first = syncFirebaseBridge({ id: "u1", legacyAuthUid: "fb-1" });
        const second = syncFirebaseBridge({ id: "u1", legacyAuthUid: "fb-1" }, { force: true });
        await Promise.all([first, second]);
        expect(state.mints).toBe(2);
        expect(getBridgeState().claims).toEqual({ admin: true, role: "admin" });
    });
});
