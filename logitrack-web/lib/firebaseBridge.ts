/**
 * The web side of the Firebase bridge (developer-spec.md §4.9, §10.4; Appendix C §C.6.3; R8, R40,
 * R80), from the P0 login switch until TW7 at the end of P6. Sign-in happens at Go (BFF cookies);
 * pages that still read Firestore need a Firebase session too, so the browser asks the BFF for a
 * custom token (`POST /api/auth/firebase-token` -> internal `POST /v1/bridge/firebase-token`) and
 * signs in with `signInWithCustomToken`. The token's legacy claims (`admin`, `role`, `driverId`,
 * `customerScopeId`, `partnerScopeId`) are the ones `firestore.rules` and the unmigrated pages read.
 *
 * When a token is minted (never per page load; replaces `setAdminClaims`, `context/auth.tsx:52-53`):
 * - once per sign-in (`force`), after a `claims_changed` refresh (`onClaimsRefreshed`, `force`) and
 *   after a tenant switch (`force`): the legacy claims follow the new role, scope or tenant;
 * - on a page load whose Firebase SDK holds no user, or a user other than `['me'].legacyAuthUid`.
 *
 * Go mints only for own-fleet staff, platform_admin and users imported from a legacy partner or
 * customer claim. Anyone else gets `403 permission_denied`, and `404` means the bridge mode is not
 * `web`/`both`: the browser then holds no Firebase session at all (a stale one is signed out) and
 * remembers the refusal for this tab, so a reload does not ask again. Those users see only pages
 * served by Go (§10.13). Sign-out, and any session end (`onSessionEnd`), sign out of Firebase too.
 *
 * A Firebase session of anyone but the target (`auth.currentUser.uid !== ['me'].legacyAuthUid`, or any
 * session when the target has no legacy uid) is signed out before the mint, and again if the sync ends
 * without a session of its own. So a failed mint or `signInWithCustomToken` (5xx, network, 429, an
 * unreachable identitytoolkit) leaves no Firebase session at all, never the previous user's of a shared
 * browser: Firestore pages then fail closed instead of running under someone else's claims. A
 * restored session of the target itself is kept when a forced re-mint fails; the account mirror
 * (Appendix C §C.6.4) brings its claims in line.
 *
 * State lives in a small store read by `AuthProvider` (`useSyncExternalStore`): the auth context
 * stays loading until the bridge has first settled for the signed-in user, so a Firestore page never
 * queries before its Firebase session exists. A forced re-mint for a user already settled (a
 * `claims_changed` refresh, a tenant switch) keeps that state until the new outcome replaces it: the
 * existing Firebase session stays valid meanwhile, so the page under the auth gate is not torn down.
 */
import type { Auth, IdTokenResult, User, UserCredential } from "firebase/auth";
import { isApiError } from "./apiError";
import { firebaseCustomToken } from "./authClient";

export type BridgeStatus = "idle" | "pending" | "ready" | "none" | "error";

/** What the unmigrated pages still read from the Firebase user (`app/my-account/page.tsx`). */
export interface BridgeProfile {
    emailVerified: boolean;
    creationTime?: string;
    lastSignInTime?: string;
}

export interface BridgeState {
    status: BridgeStatus;
    /** The Go user id this state belongs to. */
    userId?: string;
    /** Claims of the Firebase ID token (status `ready`). */
    claims?: Record<string, unknown>;
    /** The signed-in Firebase uid (status `ready`). */
    firebaseUid?: string;
    /** The Firebase user's own fields (status `ready`). */
    profile?: BridgeProfile;
}

/** Firebase SDK calls the bridge needs; replaced in tests. */
export interface BridgeDeps {
    auth: Auth;
    signInWithCustomToken: (auth: Auth, token: string) => Promise<UserCredential>;
    signOut: (auth: Auth) => Promise<void>;
    getIdTokenResult: (user: User) => Promise<IdTokenResult>;
    /** `POST /api/auth/firebase-token`. */
    mint: () => Promise<{ customToken: string }>;
    /** Waits `ms` (tests make it instant). */
    sleep: (ms: number) => Promise<void>;
}

/** sessionStorage: the Go user id for whom Go refused a token in this tab (403 or 404). */
export const BRIDGE_DENIED_KEY = "lt:bridgeDenied";
/** Delays before the retries of a mint that failed on the network or a 5xx. */
export const BRIDGE_RETRY_DELAYS_MS = [1_000, 4_000];

let state: BridgeState = { status: "idle" };
const listeners = new Set<() => void>();
let chain: Promise<void> = Promise.resolve();
let depsOverride: (() => Promise<BridgeDeps>) | undefined;

function setState(next: BridgeState): void {
    state = next;
    for (const l of [...listeners]) l();
}

/** The current bridge state (stable reference until it changes). */
export function getBridgeState(): BridgeState {
    return state;
}

/** Subscribes to state changes; returns the unsubscribe function. */
export function subscribeBridge(listener: () => void): () => void {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
}

/** Tests: inject the Firebase SDK and reset the store. */
export function configureFirebaseBridge(deps: (() => Promise<BridgeDeps>) | undefined): void {
    depsOverride = deps;
    state = { status: "idle" };
    chain = Promise.resolve();
}

async function defaultDeps(): Promise<BridgeDeps> {
    const { auth, getIdTokenResult, signInWithCustomToken, signOut } = await import("./firebaseBridgeSdk");
    return {
        auth,
        signInWithCustomToken,
        signOut,
        getIdTokenResult: (user) => getIdTokenResult(user),
        mint: firebaseCustomToken,
        sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
    };
}

function loadDeps(): Promise<BridgeDeps> {
    return depsOverride ? depsOverride() : defaultDeps();
}

function deniedFor(userId: string): boolean {
    try {
        return window.sessionStorage.getItem(BRIDGE_DENIED_KEY) === userId;
    } catch {
        return false;
    }
}

function markDenied(userId: string | undefined): void {
    try {
        if (userId) window.sessionStorage.setItem(BRIDGE_DENIED_KEY, userId);
        else window.sessionStorage.removeItem(BRIDGE_DENIED_KEY);
    } catch {
        // Storage blocked: the next load asks Go again, which only costs one call.
    }
}

function retryable(error: unknown): boolean {
    return !isApiError(error) || error.status === 0 || error.status >= 500;
}

function profileOf(user: User): BridgeProfile {
    return {
        emailVerified: user.emailVerified,
        ...(user.metadata?.creationTime ? { creationTime: user.metadata.creationTime } : {}),
        ...(user.metadata?.lastSignInTime ? { lastSignInTime: user.metadata.lastSignInTime } : {}),
    };
}

function readyState(userId: string, user: User, result: IdTokenResult): BridgeState {
    return { status: "ready", userId, claims: { ...result.claims }, firebaseUid: user.uid, profile: profileOf(user) };
}

export interface SyncTarget {
    /** Go user id (`['me'].id`). */
    id: string;
    /** `['me'].legacyAuthUid`: the Firebase uid, when the user has one. */
    legacyAuthUid?: string;
}

/** Whether the Firebase user of `auth` is someone other than `target` (a target without legacy uid owns none). */
function foreignUser(auth: Auth, target: SyncTarget): boolean {
    const u = auth.currentUser;
    return Boolean(u && u.uid !== target.legacyAuthUid);
}

/** Signs out a Firebase session that does not belong to `target`; never rejects. */
async function dropForeignSession(deps: BridgeDeps, target: SyncTarget): Promise<void> {
    if (foreignUser(deps.auth, target)) await deps.signOut(deps.auth).catch(() => undefined);
}

/** Settles as `error`: first drops a session that is not the target's (see the module comment). */
async function settleError(deps: BridgeDeps, target: SyncTarget): Promise<void> {
    await dropForeignSession(deps, target);
    setState({ status: "error", userId: target.id });
}

async function runSync(target: SyncTarget, force: boolean): Promise<void> {
    if (!force && state.userId === target.id && (state.status === "ready" || state.status === "none")) return;
    // A forced re-mint for a user already settled keeps that state until the new outcome: the existing
    // Firebase session stays valid meanwhile, and `loading` must not flip back (the /app layout would
    // unmount the page). Sign-in and another user go through `pending` (`idle` or another `userId`).
    if (!bridgeSettledFor(state, target.id)) setState({ status: "pending", userId: target.id });
    let deps: BridgeDeps;
    try {
        deps = await loadDeps();
        await deps.auth.authStateReady();
    } catch (error) {
        console.warn("[bridge] Firebase SDK unavailable", error);
        setState({ status: "error", userId: target.id });
        return;
    }
    const { auth } = deps;
    const current = auth.currentUser;
    // A restored Firebase session of this very user is kept: its claims follow Go through the
    // account mirror (Appendix C §C.6.4) and the forced mints above.
    if (!force && current && target.legacyAuthUid && current.uid === target.legacyAuthUid) {
        try {
            const result = await deps.getIdTokenResult(current);
            setState(readyState(target.id, current, result));
            return;
        } catch {
            // The restored session cannot produce a token (revoked by the mirror): mint a new one.
        }
    }
    // Anyone else's session (a previous user of this browser) goes before the mint, so no outcome
    // below can leave Firestore running as that user.
    await dropForeignSession(deps, target);
    if (!force && !auth.currentUser && deniedFor(target.id)) {
        setState({ status: "none", userId: target.id });
        return;
    }

    let token: { customToken: string } | undefined;
    for (let attempt = 0; ; attempt++) {
        try {
            token = await deps.mint();
            break;
        } catch (error) {
            if (isApiError(error) && (error.status === 403 || error.status === 404)) {
                // Not entitled to a Firestore session (or bridge mode off): hold none at all.
                if (auth.currentUser) await deps.signOut(auth).catch(() => undefined);
                markDenied(target.id);
                setState({ status: "none", userId: target.id });
                return;
            }
            if (isApiError(error) && error.status === 401) {
                // The Go session ended meanwhile; lib/sessionEnd.ts has taken over.
                await dropForeignSession(deps, target);
                setState({ status: "none", userId: target.id });
                return;
            }
            if (!retryable(error) || attempt >= BRIDGE_RETRY_DELAYS_MS.length) {
                console.warn("[bridge] custom token unavailable", error);
                await settleError(deps, target);
                return;
            }
            await deps.sleep(BRIDGE_RETRY_DELAYS_MS[attempt]);
        }
    }
    try {
        markDenied(undefined);
        const credential = await deps.signInWithCustomToken(auth, token.customToken);
        const result = await deps.getIdTokenResult(credential.user);
        setState(readyState(target.id, credential.user, result));
    } catch (error) {
        // A failed sign-in leaves the SDK as it was: check the user again before settling.
        console.warn("[bridge] signInWithCustomToken failed", error);
        await settleError(deps, target);
    }
}

/**
 * Brings the Firebase session in line with the signed-in Go user (see the module comment). Calls are
 * serialised; a non-forced call for a user already settled is a no-op. Never rejects.
 */
export function syncFirebaseBridge(target: SyncTarget, options: { force?: boolean } = {}): Promise<void> {
    const force = options.force === true;
    const run = chain.then(() => runSync(target, force));
    chain = run.catch(() => undefined);
    return run;
}

/** Signs out of Firebase (logout, session end, a signed-out visitor). Never rejects. */
export function signOutFirebaseBridge(): Promise<void> {
    const run = chain.then(async () => {
        markDenied(undefined);
        try {
            const deps = await loadDeps();
            if (deps.auth.currentUser) await deps.signOut(deps.auth);
        } catch (error) {
            console.warn("[bridge] Firebase sign-out failed", error);
        }
        setState({ status: "idle" });
    });
    chain = run.catch(() => undefined);
    return run;
}

/** Whether the bridge has settled (any outcome) for Go user `userId`. */
export function bridgeSettledFor(s: BridgeState, userId: string): boolean {
    return s.userId === userId && (s.status === "ready" || s.status === "none" || s.status === "error");
}
