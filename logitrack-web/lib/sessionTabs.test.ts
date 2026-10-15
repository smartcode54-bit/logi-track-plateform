import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { waitFor } from "@testing-library/react";
import { fakeWeb, goErr, json } from "@/test-utils/fakeWeb";

function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => { resolve = done; });
    return { promise, resolve };
}

/** Separate module graphs, shared origin storage and the same exclusive Web Locks queue. */
async function loadTab() {
    vi.resetModules();
    const [auth, me, session, refresh] = await Promise.all([
        import("./authClient"), import("@/features/auth/api/me"), import("./sessionEnd"), import("./sharedRefresh"),
    ]);
    const navigate = vi.fn();
    session.configureSessionEnd({ navigate });
    return { auth, me, refresh, navigate };
}

beforeEach(() => {
    window.localStorage.clear();
    window.history.replaceState(null, "", "/app/dashboard");
    let tail: Promise<unknown> = Promise.resolve();
    Object.defineProperty(navigator, "locks", { configurable: true, value: {
        request(_name: string, _options: unknown, callback: () => Promise<unknown>) {
            const run = tail.then(callback);
            tail = run.catch(() => undefined);
            return run;
        },
    } });
});

afterEach(() => {
    vi.unstubAllGlobals();
    Reflect.deleteProperty(navigator, "locks");
});

it("does not let tab B's old logout queued behind tab A's sign-in revoke the fresh cookies", async () => {
    const tabA = await loadTab();
    const tabB = await loadTab();
    expect(tabA.auth).not.toBe(tabB.auth);
    const login = deferred<Response>();
    const bootstrap = deferred<Response>();
    const web = fakeWeb();
    let cookies = "old";
    let reads = 0;
    web.on("GET", "/api/go/v1/me", () => ++reads === 1 ? bootstrap.promise : goErr(401, "invalid_token"));
    web.on("POST", "/api/auth/login", async () => {
        const result = await login.promise;
        cookies = "fresh";
        return result;
    });
    web.on("POST", "/api/auth/logout", () => {
        cookies = "";
        return new Response(null, { status: 204 });
    });
    const oldRead = tabB.me.readMe().catch((error: unknown) => error);
    const signingIn = tabA.auth.signInWithPassword("admin@own.test", "pw");
    await waitFor(() => expect(web.count("POST", "/api/auth/login")).toBe(1));
    bootstrap.resolve(goErr(401, "session_revoked"));
    expect(await oldRead).toMatchObject({ code: "session_revoked" });
    login.resolve(json(200, { data: { tenants: [], defaultTenantId: null, expiresIn: 900 } }));
    await signingIn;
    await tabB.refresh.withSessionLock(async () => undefined);
    expect(cookies).toBe("fresh");
    expect(web.count("POST", "/api/auth/logout")).toBe(0);
    expect(tabB.navigate).not.toHaveBeenCalled();

    // A later failure belongs to the new session and must still end it in tab B.
    await expect(tabB.me.readMe()).rejects.toMatchObject({ code: "invalid_token" });
    await tabB.refresh.withSessionLock(async () => undefined);
    expect(web.count("POST", "/api/auth/logout")).toBe(1);
    expect(tabB.navigate).toHaveBeenCalledTimes(1);
});

it.each(["unauthenticated", "token_expired", "session_revoked", "invalid_token"])(
    "discards tab B's %s response received after tab A signed in",
    async (code) => {
        const tabA = await loadTab();
        const tabB = await loadTab();
        const bootstrap = deferred<Response>();
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => bootstrap.promise);
        web.on("POST", "/api/auth/login", () => json(200, { data: { tenants: [], defaultTenantId: null, expiresIn: 900 } }));
        const result = tabB.me.readMe().catch((error: unknown) => error);
        await tabA.auth.signInWithPassword("admin@own.test", "pw");
        bootstrap.resolve(goErr(401, code));
        expect(await result).toMatchObject({ name: "AbortError" });
        await tabB.refresh.withSessionLock(async () => undefined);
        expect(web.count("POST", "/api/auth/logout")).toBe(0);
        expect(web.count("POST", "/api/auth/refresh")).toBe(0);
        expect(tabB.navigate).not.toHaveBeenCalled();
    }
);

it("keeps the in-tab generation when storage reads succeed but writes are refused", async () => {
    const tab = await loadTab();
    const bootstrap = deferred<Response>();
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => bootstrap.promise);
    web.on("POST", "/api/auth/login", () => json(200, { data: { tenants: [], defaultTenantId: null, expiresIn: 900 } }));
    await tab.auth.signInWithPassword("admin@own.test", "pw");
    const result = tab.me.readMe().catch((error: unknown) => error);
    const storage = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new DOMException("Full", "QuotaExceededError"); });
    try {
        await tab.auth.signInWithPassword("admin@own.test", "pw");
        bootstrap.resolve(goErr(401, "session_revoked"));
        const error = await result;
        await tab.refresh.withSessionLock(async () => undefined);
        expect(error).toMatchObject({ name: "AbortError" });
        expect(web.count("POST", "/api/auth/logout")).toBe(0);
        expect(tab.navigate).not.toHaveBeenCalled();
    } finally {
        storage.mockRestore();
    }
});
