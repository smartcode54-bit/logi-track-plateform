// TW4 (developer-spec.md §10.6; Appendix E §E.3.3, §E.3.8, §E.4 last row): useAuth() is an adapter
// over ['me'], its value is memoised, and usePermission is a selector with no read of its own.
import React, { useEffect, useState } from "react";
import { act, render, renderHook, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

type FakeUser = { uid: string; email: string };
let authListener: ((user: FakeUser | null) => void) | undefined;
let tokenClaims: Record<string, unknown> = {};

type Snapshot = { data: () => Record<string, unknown> | undefined };
const firestore = vi.hoisted(() => {
    // The `users/{uid}` listeners the provider opened (the forceLogoutAt listener).
    const snapshots: Array<(snapshot: Snapshot) => void> = [];
    return {
        snapshots,
        getDoc: vi.fn(),
        getDocs: vi.fn(),
        onSnapshot: vi.fn((_ref: unknown, next: (snapshot: Snapshot) => void) => {
            snapshots.push(next);
            return () => undefined;
        }),
        doc: vi.fn(() => ({})),
    };
});
const signOut = vi.hoisted(() => vi.fn(async () => undefined));
const signInWithEmailAndPassword = vi.hoisted(() => vi.fn(async () => ({ user: null })));

vi.mock("@/firebase/client", () => ({ auth: {}, db: {}, functions: {}, storage: {} }));
vi.mock("@/lib/updateUserLastLogin", () => ({ resolveLoginGeoForClient: vi.fn(), updateUserLastLogin: vi.fn() }));
vi.mock("firebase/functions", () => ({
    getFunctions: () => ({}),
    httpsCallable: () => async () => ({ data: { admin: false } }),
}));
vi.mock("firebase/auth", () => ({
    onAuthStateChanged: (_auth: unknown, cb: (user: FakeUser | null) => void) => {
        authListener = cb;
        return () => {
            authListener = undefined;
        };
    },
    getIdTokenResult: async () => ({ claims: tokenClaims }),
    getIdToken: async () => "token",
    signOut,
    signInWithEmailAndPassword,
}));
vi.mock("firebase/firestore", () => firestore);

const { AuthProvider, useAuth } = await import("./auth");
const { usePermission } = await import("@/hooks/usePermission");
const { useCustomerScope } = await import("@/hooks/useCustomerScope");
const { createQueryClient } = await import("@/lib/queryClient");
const { CAPABILITIES } = await import("@/lib/capabilities");

let meBody: Record<string, unknown>;
const calls: string[] = [];

function principal(over: Record<string, unknown> = {}) {
    return {
        id: "u1",
        email: "ann@example.test",
        displayName: "Ann",
        photoUrl: null,
        tenant: { id: "t1", nameTh: "ท", nameEn: null, kind: "own_fleet", role: "manager" },
        tenants: [],
        platformRoles: [],
        dispatcher: false,
        steward: true,
        driver: null,
        customerScopes: [],
        capabilities: ["security:view_audit", "users:manage", "accounting:edit_fuel"],
        mustChangePassword: false,
        ...over,
    };
}

beforeEach(() => {
    calls.length = 0;
    meBody = principal();
    tokenClaims = {};
    firestore.getDoc.mockClear();
    firestore.getDocs.mockClear();
    signOut.mockClear();
    // Firebase reports its own sign-out to the auth-state listener, as the SDK does.
    signOut.mockImplementation(async () => {
        authListener?.(null);
    });
    firestore.snapshots.length = 0;
    vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
            const url = String(input);
            calls.push(`${init?.method ?? "GET"} ${url}`);
            if (url === "/api/go/v1/me") return new Response(JSON.stringify({ data: meBody }), { status: 200 });
            if (url === "/api/auth/logout") return new Response(null, { status: 204 });
            throw new Error(`unexpected ${url}`);
        })
    );
});

afterEach(() => {
    vi.unstubAllGlobals();
    window.history.replaceState({}, "", "/");
});

function setup() {
    const client = createQueryClient();
    const wrapper = ({ children }: { children: React.ReactNode }) => (
        <QueryClientProvider client={client}>
            <AuthProvider>{children}</AuthProvider>
        </QueryClientProvider>
    );
    return { client, wrapper };
}

async function firebaseSignedIn(user: FakeUser | null) {
    await act(async () => {
        authListener?.(user);
    });
}

describe("AuthProvider over ['me']", () => {
    it("synthesises customClaims from GET /v1/me and takes only legacy ids from the bridge token", async () => {
        meBody = principal({ tenant: null, customerScopes: [{ billingPartyId: "bp1", name: "CJ" }], capabilities: ["operations:view_first_mile"] });
        tokenClaims = { role: "admin", admin: true, customerScopeId: "cust-doc-1" };
        const { wrapper } = setup();
        const { result } = renderHook(() => ({ auth: useAuth(), scope: useCustomerScope() }), { wrapper });
        expect(result.current.auth?.loading).toBe(true);
        await firebaseSignedIn({ uid: "fb-1", email: "ann@example.test" });
        await waitFor(() => expect(result.current.auth?.loading).toBe(false));
        expect(result.current.auth?.customClaims).toMatchObject({ role: "customer", admin: false, customerScopeId: "cust-doc-1" });
        expect(result.current.auth?.me?.id).toBe("u1");
        expect(result.current.auth?.currentUser).toMatchObject({ uid: "fb-1" });
        expect(result.current.scope).toEqual({ isCustomer: true, customerScopeId: "cust-doc-1" });
    });

    it("keeps one context value across provider re-renders, so consumers do not re-render (context/auth.tsx:171)", async () => {
        const { client } = setup();
        let renders = 0;
        let loading: boolean | undefined;
        function Consumer() {
            const auth = useAuth();
            // Every commit of this component (an effect without dependencies runs after each one).
            useEffect(() => {
                loading = auth?.loading;
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
        render(<Parent><Consumer /></Parent>);
        await firebaseSignedIn(null);
        await waitFor(() => expect(loading).toBe(false));
        await screen.findByText("consumer");
        const settled = renders;
        await act(async () => bump());
        await act(async () => bump());
        expect(renders).toBe(settled);
    });

    it("usePermission is a selector: any number of instances cost one GET /v1/me and no permissions_config read", async () => {
        const { wrapper } = setup();
        const { result } = renderHook(
            () => [
                usePermission(CAPABILITIES.security_view_audit),
                usePermission(CAPABILITIES.security_manage_users),
                usePermission(CAPABILITIES.security_view_mobile_clients),
                usePermission(CAPABILITIES.accounting_edit_fuel),
                usePermission(CAPABILITIES.accounting_view_fuel),
            ],
            { wrapper }
        );
        expect(result.current.every((p) => p.loading)).toBe(true);
        await waitFor(() => expect(result.current.every((p) => !p.loading)).toBe(true));
        expect(result.current.map((p) => p.hasPermission)).toEqual([true, true, false, true, false]);
        expect(calls.filter((c) => c === "GET /api/go/v1/me")).toHaveLength(1);
        expect(firestore.getDoc).not.toHaveBeenCalled();
        expect(firestore.getDocs).not.toHaveBeenCalled();
    });

    it("logout ends the Go session, empties the cache and signs out of Firebase", async () => {
        const { client, wrapper } = setup();
        client.setQueryData(["hubs"], [{ id: "h1" }]);
        const { result } = renderHook(() => useAuth(), { wrapper });
        await firebaseSignedIn({ uid: "fb-1", email: "ann@example.test" });
        await waitFor(() => expect(result.current?.me?.id).toBe("u1"));
        await act(async () => {
            await result.current!.logout();
        });
        expect(calls).toContain("POST /api/auth/logout");
        expect(client.getQueryData(["hubs"])).toBeUndefined();
        // TanStack notifies observers on its next tick.
        await waitFor(() => expect(result.current?.me).toBeNull());
        expect(result.current?.customClaims).toBeNull();
        expect(signOut).toHaveBeenCalledTimes(1);
    });
});

// Until T18/TW5 replace them with SSE `session.revoked`, the Firebase signals end the whole session:
// the Go session, the cache and the /app page, not only the Firebase sign-in (the /app layout and
// /login decide "signed in" from ['me']). Each test is a fresh tab: a session end that leaves a
// protected page latches for the rest of the page load.
describe("the bridge-period session ends together (forceLogoutAt, sign-out in another tab)", () => {
    async function freshTab({ bridgeUser = true }: { bridgeUser?: boolean } = {}) {
        vi.resetModules();
        const [authModule, queryClientModule, session, reactQuery] = await Promise.all([
            import("./auth"),
            import("@/lib/queryClient"),
            import("@/lib/sessionEnd"),
            import("@tanstack/react-query"),
        ]);
        const client = queryClientModule.createQueryClient();
        const unbind = queryClientModule.bindQueryClientToSession(client);
        const navigate = vi.fn();
        session.configureSessionEnd({ navigate });
        const Provider = reactQuery.QueryClientProvider;
        const AuthProviderFresh = authModule.AuthProvider;
        const wrapper = ({ children }: { children: React.ReactNode }) => (
            <Provider client={client}>
                <AuthProviderFresh>{children}</AuthProviderFresh>
            </Provider>
        );
        const view = renderHook(() => authModule.useAuth(), { wrapper });
        await firebaseSignedIn(bridgeUser ? { uid: "fb-1", email: "ann@example.test" } : null);
        await waitFor(() => expect(view.result.current?.me?.id).toBe("u1"));
        client.setQueryData(["customers"], [{ id: "c1" }]);
        return { client, unbind, navigate, result: view.result };
    }

    it("a changed forceLogoutAt (admin disable or role change) ends the Go session, empties the cache and leaves /app", async () => {
        window.history.replaceState({}, "", "/app/driver-monitor");
        const { client, unbind, navigate, result } = await freshTab();
        await waitFor(() => expect(firestore.snapshots).toHaveLength(1));
        const emit = (ms: number) =>
            act(async () => {
                firestore.snapshots[0]({ data: () => ({ forceLogoutAt: { toMillis: () => ms } }) });
            });
        await emit(1); // the baseline seen on subscribing never logs out
        expect(calls).not.toContain("POST /api/auth/logout");
        await emit(2);
        await waitFor(() => expect(signOut).toHaveBeenCalledTimes(1));
        expect(calls.filter((c) => c === "POST /api/auth/logout")).toHaveLength(1);
        expect(client.getQueryData(["customers"])).toBeUndefined();
        await waitFor(() => expect(result.current?.me).toBeNull());
        expect(navigate).toHaveBeenCalledTimes(1);
        expect(navigate).toHaveBeenCalledWith("/login?next=%2Fapp%2Fdriver-monitor&reason=revoked");
        unbind();
    });

    it("a Firebase sign-out in another tab ends this tab's session too", async () => {
        window.history.replaceState({}, "", "/app/driver-monitor");
        const { client, unbind, navigate, result } = await freshTab();
        await firebaseSignedIn(null);
        expect(calls).toContain("POST /api/auth/logout");
        expect(client.getQueryData(["customers"])).toBeUndefined();
        await waitFor(() => expect(result.current?.me).toBeNull());
        expect(navigate).toHaveBeenCalledWith("/login?next=%2Fapp%2Fdriver-monitor");
        unbind();
    });

    it("the user's own logout ends the session once, without a second session end", async () => {
        window.history.replaceState({}, "", "/app/driver-monitor");
        const { unbind, navigate, result } = await freshTab();
        await act(async () => {
            await result.current!.logout();
        });
        expect(signOut).toHaveBeenCalledTimes(1);
        expect(calls.filter((c) => c === "POST /api/auth/logout")).toHaveLength(1);
        expect(navigate).not.toHaveBeenCalled();
        unbind();
    });

    it("a principal without a bridge session (R80) is left alone when Firebase reports no user", async () => {
        window.history.replaceState({}, "", "/app/driver-monitor");
        const { unbind, navigate, result } = await freshTab({ bridgeUser: false });
        await firebaseSignedIn(null);
        expect(calls).not.toContain("POST /api/auth/logout");
        expect(navigate).not.toHaveBeenCalled();
        expect(result.current?.me?.id).toBe("u1");
        unbind();
    });
});

describe("login", () => {
    it("starts the new principal from an empty cache, then refetches ['me']", async () => {
        const { client, wrapper } = setup();
        const { result } = renderHook(() => useAuth(), { wrapper });
        await waitFor(() => expect(result.current?.me?.id).toBe("u1"));
        client.setQueryData(["customers"], [{ id: "cached before the sign-in" }]);
        const before = calls.filter((c) => c === "GET /api/go/v1/me").length;
        await act(async () => {
            await result.current!.login("bob@example.test", "pw");
        });
        expect(signInWithEmailAndPassword).toHaveBeenCalledTimes(1);
        expect(client.getQueryData(["customers"])).toBeUndefined();
        expect(calls.filter((c) => c === "GET /api/go/v1/me")).toHaveLength(before + 1);
    });
});

describe("source guard: permissions_config", () => {
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
});
