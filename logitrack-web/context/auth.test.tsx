// T18 (developer-spec.md §10.4, §10.6; R36-R38, R40, R50, R78, R80): the AuthProvider over ['me'] and the
// Firebase bridge, with the session stream of RealtimeProvider. The browser talks to a fake BFF (fetch),
// the Firebase SDK and EventSource are fakes.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { act, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";

vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));

import { AuthProvider, useAuth } from "./auth";
import { RealtimeProvider } from "./realtime";
import { ME_KEY, type Me } from "@/features/auth/api/me";
import { configureFirebaseBridge, getBridgeState } from "@/lib/firebaseBridge";
import { makeQueryClient } from "@/lib/queryClient";
import { configureSessionEnd } from "@/lib/sessionEnd";
import { LAST_FORCED_REFRESH_KEY, LAST_REFRESH_KEY, sharedRefresh } from "@/lib/sharedRefresh";
import { fakeFirebase, fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";

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

function renderApp() {
    const client = makeQueryClient();
    const view = render(
        <QueryClientProvider client={client}>
            <AuthProvider>
                <RealtimeProvider>
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

const state = () => screen.getByTestId("state").textContent;

beforeEach(() => {
    navigated = [];
    configureSessionEnd({ navigate: (u) => navigated.push(u) });
    window.localStorage.removeItem(LAST_REFRESH_KEY);
    window.localStorage.removeItem(LAST_FORCED_REFRESH_KEY);
    window.sessionStorage.clear();
    window.history.replaceState(null, "", "/");
    FakeEventSource.all = [];
    vi.stubGlobal("EventSource", FakeEventSource);
});
afterEach(() => {
    vi.unstubAllGlobals();
    configureFirebaseBridge(undefined);
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

        role = "tenant_admin";
        await act(async () => {
            await sharedRefresh({ force: true, since: Date.now() - 1 });
        });
        await waitFor(() => expect(screen.getByTestId("role").textContent).toBe("tenant_admin"));
        expect(web.count("POST", "/api/auth/firebase-token")).toBe(mintsBefore + 1);
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
        // The stream is reopened with the new claims.
        await waitFor(() => expect(FakeEventSource.all.length).toBeGreaterThan(1));
    });

    it("logout revokes at the BFF, signs out of Firebase and leaves no principal", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe() }));
        web.on("POST", "/api/auth/firebase-token", () => json(200, { data: { customToken: "fb-u-1|admin", expiresIn: 3600 } }));
        web.on("POST", "/api/auth/logout", () => new Response(null, { status: 204 }));
        const fb = fakeFirebase();
        const { client } = renderApp();
        await waitFor(() => expect(state()).toBe("in"));
        await act(async () => {
            await authApi!.logout();
        });
        // TanStack notifies observers on a macrotask (notifyManager): wait for the render.
        await waitFor(() => expect(state()).toBe("out"));
        expect(web.count("POST", "/api/auth/logout")).toBe(1);
        expect(fb.current).toBeNull();
        expect(client.getQueryData<Me | null>(ME_KEY)).toBeNull();
        expect(FakeEventSource.all.every((s) => s.closed)).toBe(true);
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
