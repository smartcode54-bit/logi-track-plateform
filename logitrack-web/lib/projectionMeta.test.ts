import { afterEach, describe, expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";

import { adaptProjection, fieldIsRedacted } from "./projectionMeta";
import { goFetch, goFetchEnvelope } from "./goFetch";
import { fetchAllPages } from "./goPages";
import { goInfiniteQueryFn, goNextPageParam, goQueryFn } from "./goQuery";
import { maskedMoneyText, maskedJsonText } from "./maskedValue";

const response = (body: unknown) => new Response(JSON.stringify(body), { headers: { "X-Request-Id": "projection-ref" } });
const page = (masked = true) => ({ data: [{ id: "row", name: masked ? "****" : "Visible", amount: masked ? null : 10, nested: { externalId: masked ? null : "external" } }], meta: {
    supportAccess: { masked, version: "7" },
    redacted: masked ? [{ path: "/data/0/name", kind: "person" }, { path: "/data/0/amount", kind: "money" }, { path: "/data/0/nested/externalId", kind: "person" }] : [],
} });
afterEach(() => vi.unstubAllGlobals());

describe("projection metadata adapter", () => {
    it("retains per-field paths through unwrapping, nesting, page flattening and JSON caches", async () => {
        vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(response({ ...page(), nextCursor: "next" })).mockResolvedValueOnce(response(page(false))));
        const rows = await fetchAllPages<{ amount: number | null; nested: { externalId: string | null } }>("/v1/platform/users");
        const cached = JSON.parse(JSON.stringify(rows));
        expect(cached).toHaveLength(2);
        expect(fieldIsRedacted(cached[0], "amount")).toBe(true);
        expect(fieldIsRedacted(cached[1], "amount")).toBe(false);
        expect(fieldIsRedacted(cached[0].nested, "externalId")).toBe(true);
        expect(maskedMoneyText(cached[0].amount, { source: cached[0], path: "amount" })).toBe("****");
        expect(maskedJsonText(cached[0])).toContain('"externalId":"****"');
        expect(maskedJsonText(cached[0])).not.toContain("__redactedFields");
    });

    it("retains metadata through query structural sharing and infinite-page flattening", async () => {
        vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(response(page()))));
        const client = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0, staleTime: 0 } } });
        const query = { queryKey: ["projected"] as const, queryFn: goQueryFn<{ amount: number | null }[]>("/v1/platform/users") };
        await client.fetchQuery(query);
        const rows = await client.fetchQuery(query);
        expect(fieldIsRedacted(rows[0], "amount")).toBe(true);
        const infinite = await client.fetchInfiniteQuery({ queryKey: ["pages"], queryFn: goInfiniteQueryFn<{ amount: number | null }>("/v1/platform/users"), initialPageParam: undefined as string | undefined, getNextPageParam: goNextPageParam });
        expect(fieldIsRedacted(infinite.pages.flatMap((p) => p.data)[0], "amount")).toBe(true);
        client.clear();
    });

    it("preserves redacted money and file elements through nested arrays", () => {
        const env = adaptProjection({ data: { amounts: [null, 12], nested: [[null, "visible"]] }, meta: { redacted: [
            { path: "/data/amounts/0", kind: "money" }, { path: "/data/nested/0/0", kind: "file" },
        ] } });
        expect(maskedMoneyText(env.data.amounts[0], { source: env.data.amounts, path: "0" })).toBe("****");
        expect(maskedJsonText(env.data.amounts)).toBe('["****",12]');
        expect(maskedJsonText(env.data.nested[0])).toBe('["****","visible"]');
        expect(maskedJsonText(JSON.parse(JSON.stringify(env.data)))).toBe('{"amounts":["****",12],"nested":[["****","visible"]]}');
    });

    it("uses a parent record's indexed redactions when exporting an array", () => {
        const source = { __redactedFields: [{ path: "/files/1", kind: "file" }], files: ["a", null] };
        expect(maskedJsonText(source)).toBe('{"files":["a","****"]}');
        expect(maskedJsonText(source.files, { source, path: "/files" })).toBe('["a","****"]');
    });

    it("preserves meta pointers and escaped property names", async () => {
        vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(response({ data: { "a/b": { "~key": null } }, meta: { label: "****", redacted: [{ path: "/data/a~1b/~0key", kind: "file" }, { path: "/meta/label", kind: "person" }] } }))));
        const env = await goFetchEnvelope<{ "a/b": { "~key": null } }>("/v1/platform/users/u");
        expect(fieldIsRedacted(env.data["a/b"], "/~0key")).toBe(true);
        expect(fieldIsRedacted(env.meta, "label")).toBe(true);
    });

    it.each([
        {}, { redacted: null }, { redacted: [], supportAccess: { masked: "yes", version: "7" } },
        { redacted: [{ path: "/data/name", kind: "unknown" }] },
        { redacted: [{ path: "/data/missing", kind: "money" }] },
        { redacted: [{ path: "/data/name~2", kind: "person" }] },
        { redacted: [{ path: "/data/name", kind: "person" }] },
    ])("fails closed before publishing malformed projection metadata: %j", async (meta) => {
        vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(response({ data: { name: "raw" }, meta }))));
        await expect(goFetch("/v1/platform/users/u")).rejects.toMatchObject({ code: "support_projection_unavailable", requestId: "projection-ref" });
    });

    it.each([200, 204, 304])("refuses empty or cache-only platform responses (%s)", async (status) => {
        vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(new Response(null, { status }))));
        await expect(goFetch("/v1/platform/users")).rejects.toMatchObject({ code: "support_projection_unavailable" });
    });

    it("requires metadata on explicitly admitted support reads and leaves ordinary primitives unchanged", async () => {
        vi.stubGlobal("fetch", vi.fn(() => Promise.resolve(response({ data: 0 }))));
        await expect(goFetch("/v1/dashboard/summary", { requireProjection: true })).rejects.toMatchObject({ code: "support_projection_unavailable" });
        await expect(goFetch("/v1/example")).resolves.toBe(0);
        expect(adaptProjection({ data: null }).data).toBeNull();
    });
});
