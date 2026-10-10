// T18 over TW4 (developer-spec.md §10.4, §10.6; Appendix E §E.3.3, §E.3.8, §E.4 last row; R36-R38,
// R40, R50, R78, R80): the AuthProvider over ['me'] and the Firebase bridge, with the session stream of
// RealtimeProvider and the cache bound to the session as app/providers.tsx binds it
// (bindQueryClientToSession). useAuth() is an adapter whose value is memoised and whose claims come
// from Go; usePermission is a selector with no read of its own. The browser talks to a fake BFF
// (fetch), the Firebase SDK and EventSource are fakes.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React, { useEffect, useState } from "react";
import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";

vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));
// Spies on the Firestore reads a permission check used to make (permissions_config): none may happen.
const firestore = vi.hoisted(() => ({ getDoc: vi.fn(), getDocs: vi.fn() }));
vi.mock("firebase/firestore", async (importOriginal) => ({
    ...(await importOriginal<typeof import("firebase/firestore")>()),
    getDoc: firestore.getDoc,
    getDocs: firestore.getDocs,
}));

import { AuthProvider, useAuth } from "./auth";
import { RealtimeProvider } from "./realtime";
import { type MeDTO } from "@/features/auth/api/me";
import { usePermission } from "@/hooks/usePermission";
import { useCustomerScope } from "@/hooks/useCustomerScope";
import { CAPABILITIES } from "@/lib/capabilities";
import { configureFirebaseBridge, getBridgeState } from "@/lib/firebaseBridge";
import { bindQueryClientToSession, createQueryClient } from "@/lib/queryClient";
import { queryKeys } from "@/lib/queryKeys";
import { configureSessionEnd, resetSessionEndForTests } from "@/lib/sessionEnd";
import { LAST_FORCED_REFRESH_KEY, LAST_REFRESH_KEY, sharedRefresh } from "@/lib/sharedRefresh";
import { fakeFirebase, fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";

const ME_KEY = queryKeys.me();

class FakeEventSource {
    static all: FakeEventSource[] = [];
    readyState = 1;
    onopen: (() => void) | null = null;
    onerror: (() => void) | null = null;
    closed = false;
    listeners = new Map<string, ((ev: MessageEvent) => void)[]>();
    constructor(readonly url: string) {
        FakeEventSource.all.push(this);
    }
    addEventListener(type: string, l: (ev: MessageEvent) => void) {
        this.listeners.set(type, [...(this.listeners.get(type) ?? []), l]);
    }
    close() {
        this.closed = true;
        this.readyState = 2;
    }
    emit(type: string, data: unknown) {
        for (const l of this.listeners.get(type) ?? []) l({ data: JSON.stringify(data), lastEventId: "1" } as MessageEvent);
    }
}

function Probe() {
    const auth = useAuth();
    if (!auth) return <p>no provider</p>;
    return (
        <div>
            <p data-testid="state">{auth.loading ? "loading" : auth.me ? "in" : auth.error ? "error" : "out"}</p>
            <p data-testid="uid">{auth.currentUser?.uid ?? ""}</p>
            <p data-testid="role">{String(auth.customClaims?.role ?? "")}</p>
            <p data-testid="tenant">{auth.me?.tenant?.id ?? ""}</p>
        </div>
    );
}

let navigated: string[] = [];
let authApi: ReturnType<typeof useAuth> = null;
function Grab({ onAuth }: { onAuth: (a: ReturnType<typeof useAuth>) => void }) {
    const auth = useAuth();
    React.useEffect(() => onAuth(auth));
    return null;
}

/** A page under the same gate as app/app/layout.tsx: a spinner while `loading`, else the page. */
function GatedPage({ onMount }: { onMount: () => void }) {
    const auth = useAuth();
    return auth?.loading ? <p>spinner</p> : <CountedPage onMount={onMount} />;
}
function CountedPage({ onMount }: { onMount: () => void }) {
    React.useEffect(() => onMount(), [onMount]);
    return <p>page</p>;
}
/** Reports each change of the auth state ("loading", "in", "out", "error"). */
function StateLog({ onState }: { onState: (s: string) => void }) {
    const auth = useAuth();
    const s = auth?.loading ? "loading" : auth?.me ? "in" : auth?.error ? "error" : "out";
    React.useEffect(() => onState(s), [s, onState]);
    return null;
}

// The runtime of app/providers.tsx that owns the cache's session effects (cleared on a session end and
// on a change of principal, invalidated after claims_changed); unbound after each test.
const unbinds: Array<() => void> = [];
function sessionBoundClient(): QueryClient {
    const client = createQueryClient();
    unbinds.push(bindQueryClientToSession(client));
    return client;
}

function renderApp(extra: React.ReactNode = null) {
    const client = sessionBoundClient();
    const view = render(
        <QueryClientProvider client={client}>
            <AuthProvider>
                <RealtimeProvider>
                    {extra}
                    <Probe />
                    <Grab
                        onAuth={(a) => {
                            authApi = a;
                        }}
                    />
                </RealtimeProvider>
            </AuthProvider>
        </QueryClientProvider>
    );
    return { client, view };
}

function hookWrapper(client: QueryClient) {
    function Wrapper({ children }: { children: React.ReactNode }) {
        return (
            <QueryClientProvider client={client}>
                <AuthProvider>{children}</AuthProvider>
            </QueryClientProvider>
        );
    }
    return Wrapper;
}

const state = () => screen.getByTestId("state").textContent;

beforeEach(() => {
    navigated = [];
    // The admin_revoke test leaves lib/sessionEnd.ts "leaving": without the reset every later test would
    // run with endSession as a silent no-op and its "never signs out" checks could not fail.
    resetSessionEndForTests();
    configureSessionEnd({ navigate: (u) => navigated.push(u) });
    window.localStorage.removeItem(LAST_REFRESH_KEY);
    window.localStorage.removeItem(LAST_FORCED_REFRESH_KEY);
    window.sessionStorage.clear();
    window.history.replaceState(null, "", "/");
    FakeEventSource.all = [];
    vi.stubGlobal("EventSource", FakeEventSource);
});
afterEach(() => {
    while (unbinds.length) unbinds.pop()!();
    vi.unstubAllGlobals();
    configureFirebaseBridge(undefined);
    firestore.getDoc.mockClear();
    firestore.getDocs.mockClear();
});

describe("AuthProvider", () => {
    it("a signed-out visitor on a public page is just signed out: no session end, no mint, no stream", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => goErr(401, "unauthenticated"));
        web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
        fakeFirebase();
        renderApp();
        await waitFor(() => expect(state()).toBe("out"));
        expect(navigated).toEqual([]);
        expect(web.count("POST", "/api/auth/firebase-token")).toBe(0);
        expect(web.count("POST", "/api/auth/logout")).toBe(0);
        expect(FakeEventSource.all).toHaveLength(0);
        // No Cloud Function is ever called (setAdminClaims per load is gone).
        expect(web.calls.every((c) => c.url.startsWith("/api/"))).toBe(true);
    });

    it("login: cookies via /api/auth/login, ['me'] from Go, one forced bridge mint, then the session stream", async () => {
        const web = fakeWeb();
        let signedIn = false;
        web.on("GET", "/api/go/v1/me", () => (signedIn ? json(200, { data: makeMe() }) : goErr(401, "unauthenticated")));
        web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
        web.on("POST", "/api/auth/login", () => {
            signedIn = true;
            return json(200, { data: { tenants: [], defaultTenantId: "t-own", expiresIn: 900 } });
        });
        web.on("POST", "/api/auth/firebase-token", () => json(200, { data: { customToken: "fb-u-1|admin", expiresIn: 3600 } }));
        web.on("PATCH", "/api/go/v1/me", () => json(200, { data: makeMe() }));
        const fb = fakeFirebase();
        renderApp();
        await waitFor(() => expect(state()).toBe("out"));

        await act(async () => {
            await authApi!.login("admin@own.test", "pw");
        });
        await waitFor(() => expect(state()).toBe("in"));
        expect(screen.getByTestId("uid").textContent).toBe("fb-u-1");
        expect(screen.getByTestId("role").textContent).toBe("admin");
        expect(web.count("POST", "/api/auth/firebase-token")).toBe(1);
        expect(fb.signIns).toBe(1);
        expect(web.calls.find((c) => c.url === "/api/auth/login")?.body).toEqual({ email: "admin@own.test", password: "pw" });
        await waitFor(() => expect(FakeEventSource.all.map((s) => s.url)).toEqual(["/api/go/v1/events"]));
    });

    it("a principal Go refuses a Firestore session for (a dispatcher created in Go) works with synthesised claims", async () => {
        const web = fakeWeb();
        const me = makeMe({ id: "u-d", legacyAuthUid: undefined, dispatcher: true, tenant: { id: "t-c", nameTh: "ค", nameEn: null, kind: "carrier", role: "user" } });
        web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
        web.on("POST", "/api/auth/firebase-token", () => goErr(403, "permission_denied"));
        const fb = fakeFirebase();
        renderApp();
        await waitFor(() => expect(state()).toBe("in"));
        expect(getBridgeState().status).toBe("none");
        expect(fb.signIns).toBe(0);
        expect(screen.getByTestId("uid").textContent).toBe("u-d");
        expect(screen.getByTestId("role").textContent).toBe("user");
    });

    it("claims_changed: a forced refresh refetches ['me'] and mints the bridge again; the user stays signed in", async () => {
        const web = fakeWeb();
        let role = "manager";
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ tenant: { id: "t-own", nameTh: "ก", nameEn: null, kind: "own_fleet", role } }) }));
        web.on("POST", "/api/auth/firebase-token", () => json(200, { data: { customToken: `fb-u-1|${role}`, expiresIn: 3600 } }));
        web.on("POST", "/api/auth/refresh", () => new Response(null, { status: 204 }));
        fakeFirebase();
        renderApp();
        await waitFor(() => expect(screen.getByTestId("role").textContent).toBe("manager"));
        const mintsBefore = web.count("POST", "/api/auth/firebase-token");

        role = "operation_staff";
        await act(async () => {
            await sharedRefresh({ force: true, since: Date.now() - 1 });
        });
        // The role line comes from Go (['me']); the re-minted bridge token carries the new legacy role too.
        await waitFor(() => expect(screen.getByTestId("role").textContent).toBe("operation_staff"));
        await waitFor(() => expect(getBridgeState().claims?.role).toBe("operation_staff"));
        expect(web.count("POST", "/api/auth/firebase-token")).toBe(mintsBefore + 1);
        // One GET /v1/me for the claims change: the AuthProvider joins the runtime's refetch.
        expect(web.count("GET", "/api/go/v1/me")).toBe(2);
        expect(state()).toBe("in");
        expect(navigated).toEqual([]);
        expect(web.count("POST", "/api/auth/logout")).toBe(0);
    });

    it("SSE session.revoked admin_revoke on a protected page: GET /v1/me 401 ends the session, Firebase signs out, /login", async () => {
        window.history.replaceState(null, "", "/app/dashboard");
        const web = fakeWeb();
        let revoked = false;
        web.on("GET", "/api/go/v1/me", () => (revoked ? goErr(401, "session_revoked") : json(200, { data: makeMe() })));
        web.on("POST", "/api/auth/firebase-token", () => json(200, { data: { customToken: "fb-u-1|admin", expiresIn: 3600 } }));
        web.on("POST", "/api/auth/logout", () => new Response(null, { status: 204 }));
        const fb = fakeFirebase();
        const { client } = renderApp();
        await waitFor(() => expect(state()).toBe("in"));
        await waitFor(() => expect(FakeEventSource.all).toHaveLength(1));

        revoked = true;
        const at = Date.now();
        act(() => {
            FakeEventSource.all[0].emit("session.revoked", { type: "session.revoked", topic: "user:u-1", eventId: "e", data: { userId: "u-1", sessionIds: ["s-1"], reason: "admin_revoke" } });
        });
        await waitFor(() => expect(navigated).toEqual(["/login?next=%2Fapp%2Fdashboard&reason=revoked"]));
        expect(Date.now() - at).toBeLessThan(5_000);
        await waitFor(() => expect(fb.signOuts).toBeGreaterThan(0));
        expect(web.count("POST", "/api/auth/logout")).toBe(1);
        expect(client.getQueryData(ME_KEY)).toBeNull();
        expect(FakeEventSource.all[0].closed).toBe(true);
    });

    it("SSE session.revoked claims_changed keeps the tab signed in (forced refresh, new ['me'])", async () => {
        window.history.replaceState(null, "", "/app/dashboard");
        const web = fakeWeb();
        let role = "operator";
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ tenant: { id: "t-own", nameTh: "ก", nameEn: null, kind: "own_fleet", role } }) }));
        web.on("POST", "/api/auth/firebase-token", () => json(200, { data: { customToken: `fb-u-1|${role}`, expiresIn: 3600 } }));
        web.on("POST", "/api/auth/refresh", () => new Response(null, { status: 204 }));
        fakeFirebase();
        renderApp();
        await waitFor(() => expect(FakeEventSource.all).toHaveLength(1));
        role = "manager";
        act(() => {
            FakeEventSource.all[0].emit("session.revoked", { type: "session.revoked", topic: "user:u-1", eventId: "e", data: { userId: "u-1", sessionIds: [], reason: "claims_changed" } });
        });
        await waitFor(() => expect(screen.getByTestId("role").textContent).toBe("manager"));
        const forced = web.calls.filter((c) => c.url === "/api/auth/refresh");
        expect(forced).toHaveLength(1);
        expect(forced[0].body).toEqual({ force: true });
        expect(navigated).toEqual([]);
        expect(web.count("POST", "/api/auth/logout")).toBe(0);
        // The stream is reopened with the new claims.
        await waitFor(() => expect(FakeEventSource.all.length).toBeGreaterThan(1));
    });

    it("logout revokes at the BFF, signs out of Firebase and leaves no principal and no cached data", async () => {
        window.history.replaceState(null, "", "/app/dashboard");
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }));
        web.on("POST", "/api/auth/firebase-token", () => json(200, { data: { customToken: "fb-u-1|admin", expiresIn: 3600 } }));
        web.on("POST", "/api/auth/logout", () => new Response(null, { status: 204 }));
        const fb = fakeFirebase();
        const { client } = renderApp();
        await waitFor(() => expect(state()).toBe("in"));
        client.setQueryData(["hubs"], [{ id: "h1" }]);
        await act(async () => {
            await authApi!.logout();
        });
        // TanStack notifies observers on a macrotask (notifyManager): wait for the render.
        await waitFor(() => expect(state()).toBe("out"));
        expect(web.count("POST", "/api/auth/logout")).toBe(1);
        expect(fb.current).toBeNull();
        expect(client.getQueryData<MeDTO | null>(ME_KEY)).toBeNull();
        expect(client.getQueryData(["hubs"])).toBeUndefined();
        expect(authApi?.customClaims).toBeNull();
        expect(FakeEventSource.all.every((s) => s.closed)).toBe(true);
        // The user's own logout is not also a session end: no second logout, no navigation from here
        // (the shell navigates to /login itself).
        expect(navigated).toEqual([]);
    });

    it("a sign-in drops what a signed-out tab cached; signing in as someone else resets the previous principal's queries", async () => {
        const web = fakeWeb();
        let who: MeDTO | null = null;
        web.on("GET", "/api/go/v1/me", () => (who ? json(200, { data: who }) : goErr(401, "unauthenticated")));
        web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
        web.on("POST", "/api/auth/login", (c) => {
            const email = (c.body as { email: string }).email;
            who = email === "ann@own.test" ? makeMe({ id: "u-ann", legacyAuthUid: "fb-u-ann" }) : makeMe({ id: "u-bob", legacyAuthUid: "fb-u-bob" });
            return json(200, { data: { tenants: [], defaultTenantId: "t-own", expiresIn: 900 } });
        });
        web.on("POST", "/api/auth/firebase-token", () => json(200, { data: { customToken: `fb-${who?.id}|admin`, expiresIn: 3600 } }));
        web.on("PATCH", "/api/go/v1/me", () => json(200, { data: who }));
        fakeFirebase();
        const { client } = renderApp();
        await waitFor(() => expect(state()).toBe("out"));

        client.setQueryData(["customers"], [{ id: "cached while signed out" }]);
        const reads = web.count("GET", "/api/go/v1/me");
        await act(async () => {
            await authApi!.login("ann@own.test", "pw");
        });
        await waitFor(() => expect(authApi?.me?.id).toBe("u-ann"));
        expect(client.getQueryData(["customers"])).toBeUndefined();
        // completeSignIn sets ['me'] from its own GET /v1/me; nothing reads it a second time.
        expect(web.count("GET", "/api/go/v1/me")).toBe(reads + 1);

        client.setQueryData(["customers"], [{ id: "ann's customers" }]);
        await act(async () => {
            await authApi!.login("bob@own.test", "pw");
        });
        await waitFor(() => expect(authApi?.me?.id).toBe("u-bob"));
        // Another principal in ['me']: lib/queryClient.ts watchPrincipal reset the previous one's data.
        expect(client.getQueryData(["customers"])).toBeUndefined();
    });

    it("switchTenant: POST /api/auth/tenant, then ['me'], a forced mint and every tenant query are reset", async () => {
        const web = fakeWeb();
        let tenant = "t-own";
        web.on("GET", "/api/go/v1/me", () =>
            json(200, { data: makeMe({ tenant: { id: tenant, nameTh: tenant, nameEn: null, kind: tenant === "t-own" ? "own_fleet" : "carrier", role: "tenant_admin" } }) })
        );
        web.on("POST", "/api/auth/firebase-token", () => json(200, { data: { customToken: "fb-u-1|admin", expiresIn: 3600 } }));
        web.on("POST", "/api/auth/tenant", (c) => {
            tenant = (c.body as { tenantId: string }).tenantId;
            return new Response(null, { status: 204 });
        });
        fakeFirebase();
        const { client } = renderApp();
        await waitFor(() => expect(screen.getByTestId("tenant").textContent).toBe("t-own"));
        client.setQueryData(["trucks", { page: 1 }], ["old-tenant-row"]);
        const mints = web.count("POST", "/api/auth/firebase-token");
        await act(async () => {
            await authApi!.switchTenant("t-carrier");
        });
        await waitFor(() => expect(screen.getByTestId("tenant").textContent).toBe("t-carrier"));
        expect(web.calls.find((c) => c.url === "/api/auth/tenant")?.body).toEqual({ tenantId: "t-carrier" });
        expect(web.count("POST", "/api/auth/firebase-token")).toBe(mints + 1);
        expect(client.getQueryData(["trucks", { page: 1 }])).toBeUndefined();
    });

    it("claims_changed, refreshClaims and switchTenant keep the page under the auth gate mounted (no loading flash)", async () => {
        window.history.replaceState(null, "", "/app/dashboard");
        const web = fakeWeb();
        let role = "operator";
        let tenant = "t-own";
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ tenant: { id: tenant, nameTh: tenant, nameEn: null, kind: "own_fleet", role } }) }));
        web.on("POST", "/api/auth/firebase-token", () => json(200, { data: { customToken: `fb-u-1|${role}`, expiresIn: 3600 } }));
        web.on("POST", "/api/auth/refresh", () => new Response(null, { status: 204 }));
        web.on("POST", "/api/auth/tenant", (c) => {
            tenant = (c.body as { tenantId: string }).tenantId;
            return new Response(null, { status: 204 });
        });
        fakeFirebase();
        const mounts = { n: 0 };
        const states: string[] = [];
        const onMount = () => {
            mounts.n += 1;
        };
        const onState = (st: string) => {
            if (states.at(-1) !== st) states.push(st);
        };
        renderApp(
            <>
                <GatedPage onMount={onMount} />
                <StateLog onState={onState} />
            </>
        );
        await waitFor(() => expect(screen.getByTestId("role").textContent).toBe("operator"));
        await waitFor(() => expect(FakeEventSource.all).toHaveLength(1));
        const mints = web.count("POST", "/api/auth/firebase-token");
        expect(mounts.n).toBe(1);

        role = "manager";
        act(() => {
            FakeEventSource.all[0].emit("session.revoked", { type: "session.revoked", topic: "user:u-1", eventId: "e", data: { userId: "u-1", sessionIds: [], reason: "claims_changed" } });
        });
        await waitFor(() => expect(screen.getByTestId("role").textContent).toBe("manager"));

        role = "operation_staff";
        await act(async () => {
            await authApi!.refreshClaims();
        });
        await waitFor(() => expect(screen.getByTestId("role").textContent).toBe("operation_staff"));

        await act(async () => {
            await authApi!.switchTenant("t-two");
        });
        await waitFor(() => expect(screen.getByTestId("tenant").textContent).toBe("t-two"));

        expect(web.count("POST", "/api/auth/firebase-token")).toBe(mints + 3);
        expect(mounts.n).toBe(1);
        expect(states.slice(states.indexOf("in"))).toEqual(["in"]);
        expect(navigated).toEqual([]);
    });

    it("a signed-out visitor's leftover Firebase session (a previous user of this browser) is signed out on a public page", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => goErr(401, "unauthenticated"));
        web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
        const fb = fakeFirebase();
        fb.current = { uid: "fb-ADMIN-A", role: "admin" };
        renderApp();
        await waitFor(() => expect(state()).toBe("out"));
        await waitFor(() => expect(fb.signOuts).toBe(1));
        expect(fb.current).toBeNull();
        expect(web.count("POST", "/api/auth/firebase-token")).toBe(0);
        expect(web.count("POST", "/api/auth/logout")).toBe(0);
    });

    it("a sign-in whose mint keeps failing leaves no Firebase session of the previous user", async () => {
        const web = fakeWeb();
        let signedIn = false;
        const me = makeMe({ id: "go-B", legacyAuthUid: "fb-B", tenant: { id: "t-own", nameTh: "ก", nameEn: null, kind: "own_fleet", role: "operator" } });
        web.on("GET", "/api/go/v1/me", () => (signedIn ? json(200, { data: me }) : goErr(401, "unauthenticated")));
        web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
        web.on("POST", "/api/auth/login", () => {
            signedIn = true;
            return json(200, { data: { tenants: [], defaultTenantId: "t-own", expiresIn: 900 } });
        });
        web.on("POST", "/api/auth/firebase-token", () => goErr(503, "unavailable"));
        web.on("PATCH", "/api/go/v1/me", () => json(200, { data: me }));
        const fb = fakeFirebase();
        renderApp();
        await waitFor(() => expect(state()).toBe("out"));
        // The previous user's session is restored from IndexedDB only now (after the visitor cleanup).
        fb.current = { uid: "fb-ADMIN-A", role: "admin" };

        await act(async () => {
            await authApi!.login("operator@own.test", "pw");
        });
        await waitFor(() => expect(state()).toBe("in"));
        expect(getBridgeState()).toMatchObject({ status: "error", userId: "go-B" });
        expect(fb.current).toBeNull();
        expect(screen.getByTestId("uid").textContent).toBe("go-B");
        expect(screen.getByTestId("role").textContent).toBe("operator");
    });

    it("an unreachable api is an error with a retry, never a sign-out", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => new Response("<html>bad gateway</html>", { status: 502 }));
        fakeFirebase();
        renderApp();
        await waitFor(() => expect(state()).toBe("error"), { timeout: 10_000 });
        expect(navigated).toEqual([]);
        expect(web.count("POST", "/api/auth/logout")).toBe(0);
    }, 15_000);
});

describe("useAuth() adapter over ['me'] (TW4)", () => {
    it("synthesises customClaims from GET /v1/me and takes only the legacy ids from the bridge token", async () => {
        const web = fakeWeb();
        const me = makeMe({ tenant: null, tenants: [], customerScopes: [{ billingPartyId: "bp1", name: "CJ" }], capabilities: ["operations:view_first_mile"] });
        web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
        // The bridge token claims admin; only its customerScopeId may reach customClaims.
        web.on("POST", "/api/auth/firebase-token", () => json(200, { data: { customToken: "fb-u-1|admin", expiresIn: 3600 } }));
        const fb = fakeFirebase();
        fb.extraClaims = { customerScopeId: "cust-doc-1" };
        const client = sessionBoundClient();
        const { result } = renderHook(() => ({ auth: useAuth(), scope: useCustomerScope() }), { wrapper: hookWrapper(client) });
        expect(result.current.auth?.loading).toBe(true);
        await waitFor(() => expect(result.current.auth?.loading).toBe(false));
        expect(getBridgeState().status).toBe("ready");
        expect(result.current.auth?.customClaims).toMatchObject({
            role: "customer",
            admin: false,
            capabilities: ["operations:view_first_mile"],
            customerScopeId: "cust-doc-1",
        });
        expect(result.current.auth?.me?.id).toBe("u-1");
        expect(result.current.auth?.currentUser).toMatchObject({ uid: "fb-u-1", id: "u-1" });
        expect(result.current.scope).toEqual({ isCustomer: true, customerScopeId: "cust-doc-1" });
    });

    it("a bridged principal's permissions come from Go, never from the bridge token's role (§10.6, R5, R27)", async () => {
        const web = fakeWeb();
        // Go: an own-fleet manager whose overrides removed fleet:view_trucks. The bridge token says admin.
        const me = makeMe({ tenant: { id: "t-own", nameTh: "ก", nameEn: null, kind: "own_fleet", role: "manager" }, capabilities: ["chat:view"] });
        web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
        web.on("POST", "/api/auth/firebase-token", () => json(200, { data: { customToken: "fb-u-1|admin", expiresIn: 3600 } }));
        fakeFirebase();
        const client = sessionBoundClient();
        const { result } = renderHook(() => useAuth(), { wrapper: hookWrapper(client) });
        await waitFor(() => expect(result.current?.loading).toBe(false));
        expect(getBridgeState()).toMatchObject({ status: "ready", claims: { role: "admin", admin: true } });
        const { can, getRole, isAdmin } = await import("@/lib/permissions");
        const claims = result.current!.customClaims;
        expect(getRole(claims)).toBe("manager");
        expect(isAdmin(claims)).toBe(false);
        expect(can(claims, CAPABILITIES.chat_view)).toBe(true);
        // The role defaults would grant a manager fleet:view_trucks, and the bridge's admin everything.
        expect(can(claims, CAPABILITIES.fleet_view_trucks)).toBe(false);
        expect(can(claims, CAPABILITIES.security_manage_roles)).toBe(false);
    });

    it("a signed-out visitor has no currentUser, no me and no claims", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => goErr(401, "unauthenticated"));
        web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
        fakeFirebase();
        const client = sessionBoundClient();
        const { result } = renderHook(() => useAuth(), { wrapper: hookWrapper(client) });
        await waitFor(() => expect(result.current?.loading).toBe(false));
        expect(result.current).toMatchObject({ currentUser: null, me: null, customClaims: null, error: null });
    });

    it("keeps one context value and stable functions across provider re-renders (context/auth.tsx:171)", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }));
        web.on("POST", "/api/auth/firebase-token", () => json(200, { data: { customToken: "fb-u-1|admin", expiresIn: 3600 } }));
        fakeFirebase();
        const client = sessionBoundClient();
        let renders = 0;
        let latest: ReturnType<typeof useAuth> = null;
        function Consumer() {
            const auth = useAuth();
            // Every commit of this component (an effect without dependencies runs after each one).
            useEffect(() => {
                latest = auth;
                renders++;
            });
            return <span>consumer</span>;
        }
        let bump: () => void = () => undefined;
        function Parent({ children }: { children: React.ReactNode }) {
            const [n, setN] = useState(0);
            useEffect(() => {
                bump = () => setN((x) => x + 1);
            }, []);
            return (
                <QueryClientProvider client={client}>
                    <AuthProvider>
                        {children}
                        <span>{n}</span>
                    </AuthProvider>
                </QueryClientProvider>
            );
        }
        render(
            <Parent>
                <Consumer />
            </Parent>
        );
        await waitFor(() => expect(latest?.loading).toBe(false));
        await screen.findByText("consumer");
        const settled = renders;
        const before = latest!;
        await act(async () => bump());
        await act(async () => bump());
        expect(renders).toBe(settled);
        expect(latest).toBe(before);
        for (const fn of ["login", "loginWithGoogle", "logout", "refreshClaims", "switchTenant", "retry"] as const) {
            expect(typeof before[fn]).toBe("function");
        }
    });

    it("usePermission is a selector: any number of instances cost one GET /v1/me and no permissions_config read", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () =>
            json(200, { data: makeMe({ legacyAuthUid: undefined, capabilities: ["security:view_audit", "users:manage", "accounting:edit_fuel"] }) })
        );
        web.on("POST", "/api/auth/firebase-token", () => goErr(404, "not_found"));
        fakeFirebase();
        const client = sessionBoundClient();
        const { result } = renderHook(
            () => [
                usePermission(CAPABILITIES.security_view_audit),
                usePermission(CAPABILITIES.security_manage_users),
                usePermission(CAPABILITIES.security_view_mobile_clients),
                usePermission(CAPABILITIES.accounting_edit_fuel),
                usePermission(CAPABILITIES.accounting_view_fuel),
            ],
            { wrapper: hookWrapper(client) }
        );
        expect(result.current.every((p) => p.loading)).toBe(true);
        await waitFor(() => expect(result.current.every((p) => !p.loading)).toBe(true));
        expect(result.current.map((p) => p.hasPermission)).toEqual([true, true, false, true, false]);
        expect(web.count("GET", "/api/go/v1/me")).toBe(1);
        expect(firestore.getDoc).not.toHaveBeenCalled();
        expect(firestore.getDocs).not.toHaveBeenCalled();
    });
});

describe("source guards", () => {
    it("only the role-matrix editor touches permissions_config; every permission check reads ['me']", async () => {
        const { readdirSync, readFileSync, statSync } = await import("fs");
        const path = await import("path");
        const root = path.resolve(__dirname, "..");
        const walk = (dir: string, out: string[] = []): string[] => {
            for (const name of readdirSync(dir)) {
                const p = path.join(dir, name);
                if (statSync(p).isDirectory()) {
                    if (name !== "node_modules" && name !== "__tests__") walk(p, out);
                } else if (/\.(ts|tsx)$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(p);
            }
            return out;
        };
        const users = ["app", "components", "context", "features", "hooks", "lib"]
            .flatMap((d) => walk(path.join(root, d)))
            .filter((f) => /PERMISSIONS_CONFIG|["']permissions_config["']/.test(readFileSync(f, "utf8")))
            .map((f) => path.relative(root, f));
        // lib/collections.ts names the collection; the matrix page edits it until T51/T54 (`/v1/roles/matrix`).
        expect(users.sort()).toEqual(["app/app/security-center/roles/page.tsx", "lib/collections.ts"]);
    });

    it("the auth context no longer calls setAdminClaims, listens to forceLogoutAt or to the Firebase auth state (T18)", async () => {
        const { readFileSync } = await import("fs");
        const path = await import("path");
        const source = readFileSync(path.resolve(__dirname, "auth.tsx"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
        expect(source).not.toMatch(/setAdminClaims|forceLogoutAt|onAuthStateChanged|firebase\/auth|firebase\/functions/);
    });
});

