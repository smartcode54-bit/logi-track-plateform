/**
 * A fake web origin for the React tests of T18: `fetch` answers the BFF paths (`/api/auth/*`,
 * `/api/go/v1/*`) from a small programmable Go state. No network; since TW7 no Firebase at all.
 */
import { vi } from "vitest";

import type { MeDTO } from "@/features/auth/api/me";

export interface FakeCall {
    method: string;
    url: string;
    body: unknown;
    /** Request headers, names lower-cased (`x-act-on-tenant`, `idempotency-key`, ...). */
    headers: Record<string, string>;
}

export type Handler = (call: FakeCall) => Response | undefined | Promise<Response | undefined>;

export function json(status: number, body: unknown, headers: Record<string, string> = {}): Response {
    return new Response(body === undefined ? null : JSON.stringify(body), { status, headers: { "Content-Type": "application/json", ...headers } });
}

export function goErr(status: number, code: string, details: Record<string, unknown> = {}): Response {
    return json(status, { error: { code, message: code, details, requestId: "rid" } });
}

export function makeMe(over: Partial<MeDTO> = {}): MeDTO {
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
        const headers: Record<string, string> = {};
        new Headers(init.headers).forEach((value, name) => {
            headers[name] = value;
        });
        const call = { method, url, body, headers };
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

/** A customer as `GET /v1/customers?fields=minimal` answers it. */
export interface FakeCustomer {
    id: string;
    billingPartyId: string | null;
    code: string;
    name: string;
}

/**
 * `GET /v1/customers?fields=minimal` as Go answers it (UI8): with `ids` the customers those customer or billing
 * party ids name, by code, no page (more than 100 distinct ids is 422 `ids too_many`); else the code/name search
 * `q` in keyset pages of `limit` (the cursor is the offset).
 */
export function customersReply(customers: readonly FakeCustomer[]): (call: FakeCall) => Response {
    return (call) => {
        const p = new URL(call.url, "http://web.test").searchParams;
        if (p.has("ids")) {
            const ids = new Set(p.getAll("ids").flatMap((v) => v.split(",")).filter(Boolean));
            if (ids.size > 100) return goErr(422, "invalid_argument", { fields: [{ field: "ids", reason: "too_many", params: { max: 100 } }] });
            const hits = customers.filter((c) => ids.has(c.id) || (c.billingPartyId !== null && ids.has(c.billingPartyId)));
            return json(200, { data: [...hits].sort((a, b) => a.code.localeCompare(b.code)) });
        }
        const q = (p.get("q") ?? "").toLowerCase();
        const limit = Number(p.get("limit") ?? "50");
        const from = Number(p.get("cursor") ?? "0");
        const hits = customers.filter((c) => !q || `${c.code} ${c.name}`.toLowerCase().includes(q));
        const next = from + limit < hits.length ? String(from + limit) : undefined;
        return json(200, { data: hits.slice(from, from + limit), ...(next ? { nextCursor: next } : {}) });
    };
}

/**
 * The `GET /v1/customers` calls that are neither a lookup (`ids`), a search (`q`) nor an open picker's first page
 * (`limit=50`, no cursor): the pages of a read of every customer.
 */
export function customerDirectoryReads(calls: readonly FakeCall[]): FakeCall[] {
    return calls.filter((c) => {
        if (c.method !== "GET" || c.url.split("?")[0] !== "/api/go/v1/customers") return false;
        const p = new URL(c.url, "http://web.test").searchParams;
        const pickerPage = p.get("limit") === "50" && !p.has("cursor");
        return !p.has("ids") && !(p.get("q") ?? "").trim() && !pickerPage;
    });
}

/** The `ids` of each `GET /v1/customers?ids=` lookup a fake web answered, each sorted. */
export function customerLookups(calls: readonly FakeCall[]): string[][] {
    return calls
        .filter((c) => c.method === "GET" && c.url.split("?")[0] === "/api/go/v1/customers")
        .map((c) => new URL(c.url, "http://web.test").searchParams)
        .filter((p) => p.has("ids"))
        .map((p) => p.getAll("ids").flatMap((v) => v.split(",")).filter(Boolean).sort());
}
