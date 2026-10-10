/**
 * A fake web origin for the React tests of T18: `fetch` answers the BFF paths (`/api/auth/*`,
 * `/api/go/v1/*`) from a small programmable Go state, and the Firebase SDK of the bridge is a fake
 * that counts mints and sign-ins. No network, no Firebase.
 */
import { vi } from "vitest";
import type { Auth, IdTokenResult, User, UserCredential } from "firebase/auth";

import type { Me } from "@/features/auth/api/me";
import { firebaseCustomToken } from "@/lib/authClient";
import { configureFirebaseBridge, type BridgeDeps } from "@/lib/firebaseBridge";

export interface FakeCall {
    method: string;
    url: string;
    body: unknown;
}

export type Handler = (call: FakeCall) => Response | undefined | Promise<Response | undefined>;

export function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
    return new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });
}

export function goErr(status: number, code: string, details: Record<string, unknown> = {}): Response {
    return json(status, { error: { code, message: code, details, requestId: "rid" } });
}

export function makeMe(over: Partial<Me> = {}): Me {
    return {
        id: "u-1",
        email: "admin@own.test",
        displayName: "Admin",
        photoUrl: null,
        tenant: { id: "t-own", nameTh: "กองรถ", nameEn: "Own fleet", kind: "own_fleet", role: "tenant_admin" },
        tenants: [{ id: "t-own", nameTh: "กองรถ", nameEn: "Own fleet", kind: "own_fleet", role: "tenant_admin" }],
        platformRoles: [],
        dispatcher: false,
        steward: true,
        driver: null,
        customerScopes: [],
        capabilities: ["users:view", "users:manage", "users:assign_role", "users:revoke_sessions"],
        mustChangePassword: false,
        legacyAuthUid: "fb-u-1",
        ...over,
    };
}

/** The fake origin: `calls` records every browser request; `handlers` answer in order, first match wins. */
export function fakeWeb() {
    const calls: FakeCall[] = [];
    const handlers: Handler[] = [];
    const fetchMock = vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
        const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
        const method = (init.method ?? "GET").toUpperCase();
        let body: unknown = undefined;
        if (typeof init.body === "string" && init.body !== "") {
            try {
                body = JSON.parse(init.body);
            } catch {
                body = init.body;
            }
        }
        const call = { method, url, body };
        calls.push(call);
        for (const h of handlers) {
            const res = await h(call);
            if (res) return res;
        }
        return goErr(404, "not_found");
    });
    vi.stubGlobal("fetch", fetchMock);
    return {
        calls,
        handlers,
        on(method: string, path: string | RegExp, reply: (call: FakeCall) => Response | Promise<Response>) {
            handlers.push((call) => {
                const p = call.url.split("?")[0];
                const match = typeof path === "string" ? p === path : path.test(p);
                return call.method === method && match ? reply(call) : undefined;
            });
            return this;
        },
        count(method: string, path: string) {
            return calls.filter((c) => c.method === method && c.url.split("?")[0] === path).length;
        },
    };
}

/**
 * A fake Firebase SDK for the bridge; the custom token names the uid ("uid|role"). The mint is the real
 * `POST /api/auth/firebase-token` call (answered by `fakeWeb`) unless replaced.
 */
export function fakeFirebase(mint: () => Promise<{ customToken: string }> = firebaseCustomToken) {
    const state = { current: null as { uid: string; role: string } | null, signIns: 0, signOuts: 0 };
    const toUser = (uid: string) => ({ uid, emailVerified: true, metadata: {} }) as unknown as User;
    const deps: BridgeDeps = {
        auth: {
            authStateReady: async () => undefined,
            get currentUser() {
                return state.current ? toUser(state.current.uid) : null;
            },
        } as unknown as Auth,
        signInWithCustomToken: async (_a, token) => {
            const [uid, role] = token.split("|");
            state.current = { uid, role };
            state.signIns += 1;
            return { user: toUser(uid) } as unknown as UserCredential;
        },
        signOut: async () => {
            state.current = null;
            state.signOuts += 1;
        },
        getIdTokenResult: async () => ({ claims: state.current ? { role: state.current.role, admin: state.current.role === "admin" } : {} }) as unknown as IdTokenResult,
        mint,
        sleep: async () => undefined,
    };
    configureFirebaseBridge(async () => deps);
    return state;
}
