import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";
import { readMe } from "@/features/auth/api/me";
import { signInWithGoogle, signInWithPassword, switchTenant } from "./authClient";
import { configureSessionEnd, endSession, onSessionEnd, resetSessionEndForTests } from "./sessionEnd";
import { onClaimsRefreshed, sharedRefresh, withSessionLock } from "./sharedRefresh";
import { ApiError } from "./apiError";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";

function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => { resolve = done; });
    return { promise, resolve };
}

const loginReply = () => json(200, { data: { tenants: [], defaultTenantId: "t-own", expiresIn: 900 } });
let navigate: ReturnType<typeof vi.fn>;

beforeEach(() => {
    window.localStorage.clear();
    window.history.replaceState(null, "", "/app/dashboard");
    resetSessionEndForTests();
    navigate = vi.fn();
    configureSessionEnd({ navigate });
});
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

describe("auth work belongs to the session in which it started", () => {
    it.each(["unauthenticated", "token_expired", "session_revoked", "invalid_token"])(
        "discards a direct me read's stale %s even without query cancellation",
        async (code) => {
            const response = deferred<Response>();
            const web = fakeWeb();
            web.on("GET", "/api/go/v1/me", () => response.promise);
            web.on("POST", "/api/auth/login", loginReply);
            web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
            const result = readMe().catch((error: unknown) => error);
            await signInWithPassword("admin@own.test", "pw");
            response.resolve(goErr(401, code));
            expect(await result).toMatchObject({ name: "AbortError" });
            expect(web.count("POST", "/api/auth/refresh")).toBe(0);
            expect(web.count("POST", "/api/auth/logout")).toBe(0);
            expect(navigate).not.toHaveBeenCalled();
        }
    );

    it("discards a stale retry's 401 after a successful refresh and a Google sign-in", async () => {
        const retry = deferred<Response>();
        const web = fakeWeb();
        let reads = 0;
        web.on("GET", "/api/go/v1/me", () => ++reads === 1 ? goErr(401, "token_expired") : retry.promise);
        web.on("POST", "/api/auth/refresh", () => new Response(null, { status: 204 }));
        web.on("POST", "/api/auth/google", loginReply);
        const result = readMe().catch((error: unknown) => error);
        await waitFor(() => expect(reads).toBe(2));
        await signInWithGoogle("id-token", "nonce");
        retry.resolve(goErr(401, "session_revoked"));
        expect(await result).toMatchObject({ name: "AbortError" });
        expect(web.count("POST", "/api/auth/logout")).toBe(0);
        expect(navigate).not.toHaveBeenCalled();
    });

    it.each([false, true])("discards a queued refresh (forced=%s) after sign-in, without notifying claims listeners", async (force) => {
        const signIn = deferred<Response>();
        const web = fakeWeb();
        web.on("POST", "/api/auth/login", () => signIn.promise);
        const signingIn = signInWithPassword("admin@own.test", "pw");
        await waitFor(() => expect(web.count("POST", "/api/auth/login")).toBe(1));
        const claims = vi.fn();
        const off = onClaimsRefreshed(claims);
        try {
            const result = sharedRefresh({ force, since: Date.now() }).catch((error: unknown) => error);
            signIn.resolve(loginReply());
            await signingIn;
            expect(await result).toMatchObject({ name: "AbortError" });
            expect(web.count("POST", "/api/auth/refresh")).toBe(0);
            expect(claims).not.toHaveBeenCalled();
        } finally {
            off();
        }
    });

    it("waits for a public-page logout to expire old cookies before sign-in sets new ones", async () => {
        window.history.replaceState(null, "", "/login");
        const logout = deferred<Response>();
        const web = fakeWeb();
        let cookie = "old";
        web.on("POST", "/api/auth/logout", async () => { const res = await logout.promise; cookie = ""; return res; });
        web.on("POST", "/api/auth/login", () => { cookie = "new"; return loginReply(); });
        endSession(new ApiError({ status: 401, code: "invalid_token", message: "invalid" }));
        await waitFor(() => expect(web.count("POST", "/api/auth/logout")).toBe(1));
        const signingIn = signInWithPassword("admin@own.test", "pw");
        await Promise.resolve();
        expect(web.count("POST", "/api/auth/login")).toBe(0);
        logout.resolve(new Response(null, { status: 204 }));
        await signingIn;
        expect(cookie).toBe("new");
        expect(navigate).not.toHaveBeenCalled();
    });

    it("does not send an old session-end logout queued behind sign-in", async () => {
        window.history.replaceState(null, "", "/login");
        const signIn = deferred<Response>();
        const web = fakeWeb();
        web.on("POST", "/api/auth/login", () => signIn.promise);
        const signingIn = signInWithPassword("admin@own.test", "pw");
        await waitFor(() => expect(web.count("POST", "/api/auth/login")).toBe(1));
        endSession(new ApiError({ status: 401, code: "session_revoked", message: "old session" }));
        signIn.resolve(loginReply());
        await signingIn;
        await Promise.resolve();
        expect(web.count("POST", "/api/auth/logout")).toBe(0);
        expect(navigate).not.toHaveBeenCalled();
    });

    it("ignores a stale tenant-route 401 instead of logging out the new session", async () => {
        const tenant = deferred<Response>();
        const web = fakeWeb();
        web.on("POST", "/api/auth/tenant", () => tenant.promise);
        web.on("POST", "/api/auth/login", loginReply);
        const result = switchTenant("t-own").catch((error: unknown) => error);
        await signInWithPassword("admin@own.test", "pw");
        tenant.resolve(goErr(401, "invalid_token"));
        expect(await result).toMatchObject({ name: "AbortError" });
        expect(web.count("POST", "/api/auth/logout")).toBe(0);
    });

    it("still ends a current session when the single retry returns 401", async () => {
        const web = fakeWeb();
        let reads = 0;
        web.on("GET", "/api/go/v1/me", () => ++reads === 1 ? goErr(401, "token_expired") : goErr(401, "invalid_token"));
        web.on("POST", "/api/auth/refresh", () => new Response(null, { status: 204 }));
        web.on("POST", "/api/auth/logout", () => new Response(null, { status: 204 }));
        await expect(readMe()).rejects.toMatchObject({ code: "invalid_token" });
        expect(navigate).toHaveBeenCalledTimes(1);
        await waitFor(() => expect(web.count("POST", "/api/auth/logout")).toBe(1));
    });

    it("still returns the current principal after the single successful retry", async () => {
        const web = fakeWeb();
        let reads = 0;
        web.on("GET", "/api/go/v1/me", () => ++reads === 1 ? goErr(401, "token_expired") : json(200, { data: makeMe() }));
        web.on("POST", "/api/auth/refresh", () => new Response(null, { status: 204 }));
        await expect(readMe()).resolves.toMatchObject({ id: "u-1" });
        expect(navigate).not.toHaveBeenCalled();
    });

    it.each(["session_revoked", "invalid_token"])("ends the current session for %s received after query cancellation", async (code) => {
        const response = deferred<Response>();
        const signal = new AbortController();
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => response.promise);
        web.on("POST", "/api/auth/logout", () => new Response(null, { status: 204 }));
        const ended = vi.fn();
        const off = onSessionEnd(ended);
        try {
            const result = readMe(signal.signal).catch((error: unknown) => error);
            signal.abort();
            response.resolve(goErr(401, code));
            expect(await result).toMatchObject({ code });
            expect(ended).toHaveBeenCalledTimes(1);
            await waitFor(() => expect(navigate).toHaveBeenCalledTimes(1));
            expect(web.count("POST", "/api/auth/logout")).toBe(1);
        } finally {
            off();
        }
    });

    it.each([false, true])("handles query cancellation during a refresh (accepted=%s)", async (accepted) => {
        const refresh = deferred<Response>();
        const signal = new AbortController();
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => goErr(401, "unauthenticated"));
        web.on("POST", "/api/auth/refresh", () => refresh.promise);
        web.on("POST", "/api/auth/logout", () => new Response(null, { status: 204 }));
        const result = readMe(signal.signal).catch((error: unknown) => error);
        await waitFor(() => expect(web.count("POST", "/api/auth/refresh")).toBe(1));
        signal.abort();
        refresh.resolve(accepted ? new Response(null, { status: 204 }) : goErr(401, "unauthenticated"));
        expect(await result).toMatchObject(accepted ? { name: "AbortError" } : { code: "unauthenticated" });
        await withSessionLock(async () => undefined);
        expect(web.count("GET", "/api/go/v1/me")).toBe(1);
        expect(web.count("POST", "/api/auth/logout")).toBe(accepted ? 0 : 1);
        expect(navigate).toHaveBeenCalledTimes(accepted ? 0 : 1);
    });

    it("redirects within 1.5 seconds when another tab never releases the session lock", async () => {
        vi.useFakeTimers();
        const release = deferred<void>();
        const busy = withSessionLock(() => release.promise);
        const web = fakeWeb();
        web.on("POST", "/api/auth/logout", () => new Response(null, { status: 204 }));
        try {
            endSession(new ApiError({ status: 401, code: "invalid_token", message: "invalid" }));
            await vi.advanceTimersByTimeAsync(1499);
            expect(navigate).not.toHaveBeenCalled();
            await vi.advanceTimersByTimeAsync(1);
            expect(navigate).toHaveBeenCalledTimes(1);
            expect(web.count("POST", "/api/auth/logout")).toBe(0);
        } finally {
            release.resolve();
            await busy;
            await withSessionLock(async () => undefined);
        }
        expect(web.count("POST", "/api/auth/logout")).toBe(1);
        expect(navigate).toHaveBeenCalledTimes(1);
    });

    it("dispatches a protected-page logout before navigating, even when the lock was busy", async () => {
        const release = deferred<void>();
        const response = deferred<Response>();
        const busy = withSessionLock(() => release.promise);
        const web = fakeWeb();
        web.on("POST", "/api/auth/logout", () => response.promise);
        configureSessionEnd({ navigate: () => {
            expect(web.count("POST", "/api/auth/logout")).toBe(1);
            navigate();
        } });
        endSession(new ApiError({ status: 401, code: "invalid_token", message: "invalid" }));
        expect(navigate).not.toHaveBeenCalled();
        release.resolve();
        await busy;
        await waitFor(() => expect(navigate).toHaveBeenCalledTimes(1));
        // Navigation does not wait for the network response; keepalive owns it from here.
        response.resolve(new Response(null, { status: 204 }));
        await response.promise;
    });
});
