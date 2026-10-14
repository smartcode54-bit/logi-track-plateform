// T18 over TW4, TW7 (developer-spec.md §10.4, §10.6; Appendix E §E.3.3, §E.3.8, §E.4 last row; R36-R38,
// R50, R78, R80): the AuthProvider over ['me'], with the session stream of RealtimeProvider and the cache
// bound to the session as app/providers.tsx binds it (bindQueryClientToSession). useAuth() is an adapter
// whose value is memoised and whose claims come from Go only; usePermission is a selector with no read
// of its own. Since TW7 there is no Firebase bridge: no flow asks for `POST /api/auth/firebase-token`.
// The browser talks to a fake BFF (fetch); EventSource is a fake.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React, { useEffect, useState } from "react";
import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";

vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));

import { AuthProvider, useAuth } from "./auth";
import { RealtimeProvider } from "@/lib/realtime/RealtimeProvider";
import { type MeDTO } from "@/features/auth/api/me";
import { usePermission } from "@/hooks/usePermission";
import { CAPABILITIES } from "@/lib/capabilities";
import { bindQueryClientToSession, createQueryClient } from "@/lib/queryClient";
import { queryKeys } from "@/lib/queryKeys";
import { configureSessionEnd, resetSessionEndForTests } from "@/lib/sessionEnd";
import { LAST_FORCED_REFRESH_KEY, LAST_REFRESH_KEY, sharedRefresh } from "@/lib/sharedRefresh";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { fakeIndexedDB, type FakeIndexedDB } from "@/test-utils/fakeIndexedDB";
import { LEGACY_FIREBASE_CLEARED_KEY } from "@/lib/legacyFirebaseCleanup";

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
});

describe("AuthProvider", () => {
    it("a signed-out visitor on a public page is just signed out: no session end, no bridge, no stream", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => goErr(401, "unauthenticated"));
        web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
        renderApp();
        await waitFor(() => expect(state()).toBe("out"));
        expect(navigated).toEqual([]);
        expect(web.count("POST", "/api/auth/firebase-token")).toBe(0);
        expect(web.count("POST", "/api/auth/logout")).toBe(0);
        expect(FakeEventSource.all).toHaveLength(0);
        // No Cloud Function is ever called (setAdminClaims per load is gone).
        expect(web.calls.every((c) => c.url.startsWith("/api/"))).toBe(true);
    });

    it("login: cookies via /api/auth/login, ['me'] from Go, then the session stream; no Firebase bridge (TW7)", async () => {
        const web = fakeWeb();
        let signedIn = false;
        web.on("GET", "/api/go/v1/me", () => (signedIn ? json(200, { data: makeMe() }) : goErr(401, "unauthenticated")));
        web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
        web.on("POST", "/api/auth/login", () => {
            signedIn = true;
            return json(200, { data: { tenants: [], defaultTenantId: "t-own", expiresIn: 900 } });
        });
        web.on("PATCH", "/api/go/v1/me", () => json(200, { data: makeMe() }));
        renderApp();
        await waitFor(() => expect(state()).toBe("out"));

        await act(async () => {
            await authApi!.login("admin@own.test", "pw");
        });
        await waitFor(() => expect(state()).toBe("in"));
        expect(screen.getByTestId("uid").textContent).toBe("u-1");
        expect(screen.getByTestId("role").textContent).toBe("admin");
        expect(web.count("POST", "/api/auth/firebase-token")).toBe(0);
        expect(web.calls.find((c) => c.url === "/api/auth/login")?.body).toEqual({ email: "admin@own.test", password: "pw" });
        await waitFor(() => expect(FakeEventSource.all.map((s) => s.url)).toEqual(["/api/go/v1/events"]));
    });

    it("a dispatcher created in Go works with synthesised claims", async () => {
        const web = fakeWeb();
        const me = makeMe({ id: "u-d", dispatcher: true, tenant: { id: "t-c", nameTh: "ค", nameEn: null, kind: "carrier", role: "user" } });
        web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
        renderApp();
        await waitFor(() => expect(state()).toBe("in"));
        expect(screen.getByTestId("uid").textContent).toBe("u-d");
        expect(screen.getByTestId("role").textContent).toBe("user");
    });

    it("claims_changed: a forced refresh refetches ['me']; the user stays signed in", async () => {
        const web = fakeWeb();
        let role = "manager";
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ tenant: { id: "t-own", nameTh: "ก", nameEn: null, kind: "own_fleet", role } }) }));
        web.on("POST", "/api/auth/refresh", () => new Response(null, { status: 204 }));
        renderApp();
        await waitFor(() => expect(screen.getByTestId("role").textContent).toBe("manager"));

        role = "operation_staff";
        await act(async () => {
            await sharedRefresh({ force: true, since: Date.now() - 1 });
        });
        // The role line comes from Go (['me']).
        await waitFor(() => expect(screen.getByTestId("role").textContent).toBe("operation_staff"));
        expect(web.count("POST", "/api/auth/firebase-token")).toBe(0);
        // One GET /v1/me for the claims change: the AuthProvider joins the runtime's refetch.
        expect(web.count("GET", "/api/go/v1/me")).toBe(2);
        expect(state()).toBe("in");
        expect(navigated).toEqual([]);
        expect(web.count("POST", "/api/auth/logout")).toBe(0);
    });

    it("SSE session.revoked admin_revoke on a protected page: GET /v1/me 401 ends the session, /login", async () => {
        window.history.replaceState(null, "", "/app/dashboard");
        const web = fakeWeb();
        let revoked = false;
        web.on("GET", "/api/go/v1/me", () => (revoked ? goErr(401, "session_revoked") : json(200, { data: makeMe() })));
        web.on("POST", "/api/auth/logout", () => new Response(null, { status: 204 }));
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
        expect(web.count("POST", "/api/auth/logout")).toBe(1);
        expect(client.getQueryData(ME_KEY)).toBeNull();
        expect(FakeEventSource.all[0].closed).toBe(true);
    });

    it("SSE session.revoked claims_changed keeps the tab signed in (forced refresh, new ['me'])", async () => {
        window.history.replaceState(null, "", "/app/dashboard");
        const web = fakeWeb();
        let role = "operator";
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ tenant: { id: "t-own", nameTh: "ก", nameEn: null, kind: "own_fleet", role } }) }));
        web.on("POST", "/api/auth/refresh", () => new Response(null, { status: 204 }));
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

    it("logout revokes at the BFF and leaves no principal and no cached data", async () => {
        window.history.replaceState(null, "", "/app/dashboard");
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }));
        web.on("POST", "/api/auth/logout", () => new Response(null, { status: 204 }));
        const { client } = renderApp();
        await waitFor(() => expect(state()).toBe("in"));
        client.setQueryData(["hubs"], [{ id: "h1" }]);
        await act(async () => {
            await authApi!.logout();
        });
        // TanStack notifies observers on a macrotask (notifyManager): wait for the render.
        await waitFor(() => expect(state()).toBe("out"));
        expect(web.count("POST", "/api/auth/logout")).toBe(1);
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
            who = email === "ann@own.test" ? makeMe({ id: "u-ann" }) : makeMe({ id: "u-bob" });
            return json(200, { data: { tenants: [], defaultTenantId: "t-own", expiresIn: 900 } });
        });
        web.on("PATCH", "/api/go/v1/me", () => json(200, { data: who }));
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

    it("switchTenant: POST /api/auth/tenant, then ['me'] and every tenant query are reset", async () => {
        const web = fakeWeb();
        let tenant = "t-own";
        web.on("GET", "/api/go/v1/me", () =>
            json(200, { data: makeMe({ tenant: { id: tenant, nameTh: tenant, nameEn: null, kind: tenant === "t-own" ? "own_fleet" : "carrier", role: "tenant_admin" } }) })
        );
        web.on("POST", "/api/auth/tenant", (c) => {
            tenant = (c.body as { tenantId: string }).tenantId;
            return new Response(null, { status: 204 });
        });
        const { client } = renderApp();
        await waitFor(() => expect(screen.getByTestId("tenant").textContent).toBe("t-own"));
        client.setQueryData(["trucks", { page: 1 }], ["old-tenant-row"]);
        await act(async () => {
            await authApi!.switchTenant("t-carrier");
        });
        await waitFor(() => expect(screen.getByTestId("tenant").textContent).toBe("t-carrier"));
        expect(web.calls.find((c) => c.url === "/api/auth/tenant")?.body).toEqual({ tenantId: "t-carrier" });
        expect(web.count("POST", "/api/auth/firebase-token")).toBe(0);
        expect(client.getQueryData(["trucks", { page: 1 }])).toBeUndefined();
    });

    it("claims_changed, refreshClaims and switchTenant keep the page under the auth gate mounted (no loading flash)", async () => {
        window.history.replaceState(null, "", "/app/dashboard");
        const web = fakeWeb();
        let role = "operator";
        let tenant = "t-own";
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ tenant: { id: tenant, nameTh: tenant, nameEn: null, kind: "own_fleet", role } }) }));
        web.on("POST", "/api/auth/refresh", () => new Response(null, { status: 204 }));
        web.on("POST", "/api/auth/tenant", (c) => {
            tenant = (c.body as { tenantId: string }).tenantId;
            return new Response(null, { status: 204 });
        });
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

        expect(web.count("POST", "/api/auth/firebase-token")).toBe(0);
        expect(mounts.n).toBe(1);
        expect(states.slice(states.indexOf("in"))).toEqual(["in"]);
        expect(navigated).toEqual([]);
    });

    it("an unreachable api is an error with a retry, never a sign-out", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => new Response("<html>bad gateway</html>", { status: 502 }));
        renderApp();
        await waitFor(() => expect(state()).toBe("error"), { timeout: 10_000 });
        expect(navigated).toEqual([]);
        expect(web.count("POST", "/api/auth/logout")).toBe(0);
    }, 15_000);
});

describe("useAuth() adapter over ['me'] (TW4)", () => {
    it("synthesises customClaims from GET /v1/me alone: no legacy Firestore id since TW7", async () => {
        const web = fakeWeb();
        const me = makeMe({ tenant: null, tenants: [], customerScopes: [{ billingPartyId: "bp1", name: "CJ" }], capabilities: ["operations:view_first_mile"] });
        web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
        const client = sessionBoundClient();
        const { result } = renderHook(() => useAuth(), { wrapper: hookWrapper(client) });
        expect(result.current?.loading).toBe(true);
        await waitFor(() => expect(result.current?.loading).toBe(false));
        expect(result.current?.customClaims).toEqual({ role: "customer", admin: false, capabilities: ["operations:view_first_mile"], dispatcher: false });
        expect(result.current?.me?.id).toBe("u-1");
        expect(result.current?.currentUser).toEqual({ uid: "u-1", id: "u-1", email: "admin@own.test", displayName: "Admin", photoURL: null });
        expect(web.count("POST", "/api/auth/firebase-token")).toBe(0);
    });

    it("a principal's permissions come from Go's capabilities, not from the role defaults (§10.6, R5, R27)", async () => {
        const web = fakeWeb();
        // Go: an own-fleet manager whose overrides removed fleet:view_trucks.
        const me = makeMe({ tenant: { id: "t-own", nameTh: "ก", nameEn: null, kind: "own_fleet", role: "manager" }, capabilities: ["chat:view"] });
        web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
        const client = sessionBoundClient();
        const { result } = renderHook(() => useAuth(), { wrapper: hookWrapper(client) });
        await waitFor(() => expect(result.current?.loading).toBe(false));
        const { can, getRole, isAdmin } = await import("@/lib/permissions");
        const claims = result.current!.customClaims;
        expect(getRole(claims)).toBe("manager");
        expect(isAdmin(claims)).toBe(false);
        expect(can(claims, CAPABILITIES.chat_view)).toBe(true);
        // The role defaults would grant a manager fleet:view_trucks.
        expect(can(claims, CAPABILITIES.fleet_view_trucks)).toBe(false);
        expect(can(claims, CAPABILITIES.security_manage_roles)).toBe(false);
    });

    it("a signed-out visitor has no currentUser, no me and no claims", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => goErr(401, "unauthenticated"));
        web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
        const client = sessionBoundClient();
        const { result } = renderHook(() => useAuth(), { wrapper: hookWrapper(client) });
        await waitFor(() => expect(result.current?.loading).toBe(false));
        expect(result.current).toMatchObject({ currentUser: null, me: null, customClaims: null, error: null });
    });

    it("keeps one context value and stable functions across provider re-renders (context/auth.tsx:171)", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }));
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
            json(200, { data: makeMe({ capabilities: ["security:view_audit", "users:manage", "accounting:edit_fuel"] }) })
        );
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
        expect(web.calls.map((c) => c.url)).toEqual(["/api/go/v1/me"]);
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
        // TW7 removed the Firestore matrix page that edited it; F87 removed lib/collections.ts, which named it.
        expect(users.sort()).toEqual([]);
    });

    it("the auth context no longer calls setAdminClaims, listens to forceLogoutAt or to the Firebase auth state (T18), nor bridges (TW7)", async () => {
        const { readFileSync } = await import("fs");
        const path = await import("path");
        const source = readFileSync(path.resolve(__dirname, "auth.tsx"), "utf8").replace(/\/\*[\s\S]*?\*\//g, "");
        expect(source).not.toMatch(/setAdminClaims|forceLogoutAt|onAuthStateChanged|firebase\/auth|firebase\/functions|firebaseBridge|firebase-token|signInWithCustomToken|updateUserLastLogin/);
    });
});

// TW7 transition (Appendix C §C.6.3 as built): until TW7 the provider signed the bridge's Firebase session
// out on logout, on a session end and for a signed-out visitor. The SDK is gone, so the session a pre-TW7
// build persisted in this browser (IndexedDB `firebaseLocalStorageDb`, `firebase:*` keys) is deleted
// instead, on the same three occasions.
describe("the Firebase session a pre-TW7 build left in this browser", () => {
    const AUTH_USER_KEY = "firebase:authUser:sentinel-api-key:[DEFAULT]";
    let idb: FakeIndexedDB;
    const firebaseState = () => ({
        authDb: idb.deleted.filter((n) => n === "firebaseLocalStorageDb").length,
        key: window.localStorage.getItem(AUTH_USER_KEY),
    });

    beforeEach(() => {
        idb = fakeIndexedDB();
        vi.stubGlobal("indexedDB", idb);
        window.localStorage.removeItem(LEGACY_FIREBASE_CLEARED_KEY);
        window.localStorage.setItem(AUTH_USER_KEY, JSON.stringify({ uid: "legacy-uid", stsTokenManager: { refreshToken: "x" } }));
    });

    it("a signed-out visitor's load deletes it, once per browser", async () => {
        window.sessionStorage.setItem("firebase:redirectEvent:sentinel-api-key:[DEFAULT]", "{}");
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => goErr(401, "unauthenticated"));
        web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
        const first = renderApp();
        await waitFor(() => expect(state()).toBe("out"));
        expect(firebaseState()).toEqual({ authDb: 1, key: null });
        expect(window.sessionStorage.getItem("firebase:redirectEvent:sentinel-api-key:[DEFAULT]")).toBeNull();
        first.view.unmount();

        // The next page load finds this browser cleaned: no second pass on every page.
        renderApp();
        await waitFor(() => expect(state()).toBe("out"));
        expect(firebaseState().authDb).toBe(1);
    });

    it("logout deletes it, even in a browser cleaned on an earlier load", async () => {
        window.localStorage.setItem(LEGACY_FIREBASE_CLEARED_KEY, "1");
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }));
        web.on("POST", "/api/auth/logout", () => new Response(null, { status: 204 }));
        renderApp();
        await waitFor(() => expect(state()).toBe("in"));
        expect(firebaseState()).toEqual({ authDb: 0, key: expect.any(String) });

        await act(async () => {
            await authApi!.logout();
        });
        expect(web.count("POST", "/api/auth/logout")).toBe(1);
        expect(firebaseState()).toEqual({ authDb: 1, key: null });
    });

    it("a session end (admin revoke) deletes it, even in a browser cleaned on an earlier load", async () => {
        window.localStorage.setItem(LEGACY_FIREBASE_CLEARED_KEY, "1");
        window.history.replaceState(null, "", "/app/dashboard");
        const web = fakeWeb();
        let revoked = false;
        web.on("GET", "/api/go/v1/me", () => (revoked ? goErr(401, "session_revoked") : json(200, { data: makeMe() })));
        web.on("POST", "/api/auth/logout", () => new Response(null, { status: 204 }));
        renderApp();
        await waitFor(() => expect(state()).toBe("in"));
        await waitFor(() => expect(FakeEventSource.all).toHaveLength(1));
        expect(firebaseState().authDb).toBe(0);

        revoked = true;
        act(() => {
            FakeEventSource.all[0].emit("session.revoked", { type: "session.revoked", topic: "user:u-1", eventId: "e", data: { userId: "u-1", sessionIds: ["s-1"], reason: "admin_revoke" } });
        });
        await waitFor(() => expect(navigated).toEqual(["/login?next=%2Fapp%2Fdashboard&reason=revoked"]));
        expect(firebaseState()).toEqual({ authDb: 1, key: null });
    });
});
