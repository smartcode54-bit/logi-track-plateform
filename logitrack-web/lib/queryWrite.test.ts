// T50 (developer-spec.md §10.8; Appendix E §E.5.1, §E.6): a cache write made while its key is fetching is
// applied again on the fetched value once that fetch succeeds, since the fetch's server read may predate it.
// The fetches here land by hand.
import { describe, expect, it } from "vitest";
import { QueryClient, QueryObserver } from "@tanstack/react-query";

import { writeThroughFetch } from "./queryWrite";

const flush = () => new Promise((r) => setTimeout(r, 0));
const KEY = ["things"];
const add = (item: string) => (cached: unknown) => (Array.isArray(cached) ? (cached.includes(item) ? cached : [...cached, item]) : cached);

function setup() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
    const server: string[] = ["a"];
    const pending: Array<{ resolve: () => void; reject: (e: Error) => void }> = [];
    const observer = new QueryObserver<string[]>(client, {
        queryKey: KEY,
        queryFn: () =>
            new Promise<string[]>((resolve, reject) => {
                const read = [...server];
                pending.push({ resolve: () => resolve(read), reject });
            }),
    });
    const off = observer.subscribe(() => undefined);
    return {
        client,
        server,
        data: () => client.getQueryData<string[]>(KEY),
        async land() {
            pending.splice(0).forEach((p) => p.resolve());
            await flush();
        },
        async fail() {
            pending.splice(0).forEach((p) => p.reject(new Error("503")));
            await flush();
        },
        off,
    };
}

describe("writeThroughFetch", () => {
    it("writes at once; with no fetch running there is nothing to apply again", async () => {
        const h = setup();
        await h.land();
        writeThroughFetch(h.client, KEY, add("b"));
        expect(h.data()).toEqual(["a", "b"]);
        h.off();
    });

    it("every write made while a refetch runs is applied again, in order, on what it fetched", async () => {
        const h = setup();
        await h.land();
        void h.client.refetchQueries({ queryKey: KEY }); // reads ["a"]
        await flush();
        const seen: unknown[] = [];
        writeThroughFetch(h.client, KEY, add("b"), { beforeReapply: (fetched) => seen.push(fetched) });
        writeThroughFetch(h.client, KEY, add("c"));
        expect(h.data()).toEqual(["a", "b", "c"]);
        await h.land();
        expect(h.data()).toEqual(["a", "b", "c"]);
        expect(seen).toEqual([["a"]]);
        // Applied once: the next refetch (started after the writes) is the server's word.
        void h.client.refetchQueries({ queryKey: KEY });
        await flush();
        await h.land();
        expect(h.data()).toEqual(["a"]);
        h.off();
    });

    it("a write during the first load (no cache yet) lands with it", async () => {
        const h = setup();
        writeThroughFetch(h.client, KEY, add("b"));
        expect(h.data()).toBeUndefined();
        await h.land();
        expect(h.data()).toEqual(["a", "b"]);
        h.off();
    });

    it("a failed or cancelled refetch keeps the written data, and nothing is applied later", async () => {
        const h = setup();
        await h.land();
        void h.client.refetchQueries({ queryKey: KEY }).catch(() => undefined);
        await flush();
        writeThroughFetch(h.client, KEY, add("b"));
        await h.fail();
        expect(h.data()).toEqual(["a", "b"]);
        void h.client.refetchQueries({ queryKey: KEY });
        await flush();
        writeThroughFetch(h.client, KEY, add("c"));
        await h.client.cancelQueries({ queryKey: KEY }); // reverts to the state the write left
        expect(h.data()).toEqual(["a", "b", "c"]);
        h.server.push("z");
        void h.client.refetchQueries({ queryKey: KEY });
        await flush();
        await h.land();
        expect(h.data()).toEqual(["a", "z"]);
        h.off();
    });

    it("keeps dataUpdatedAt when asked (a partial write)", async () => {
        const h = setup();
        await h.land();
        const at = h.client.getQueryState(KEY)!.dataUpdatedAt;
        await new Promise((r) => setTimeout(r, 5));
        void h.client.refetchQueries({ queryKey: KEY });
        await flush();
        writeThroughFetch(h.client, KEY, add("b"), { keepUpdatedAt: true });
        expect(h.client.getQueryState(KEY)!.dataUpdatedAt).toBe(at);
        await h.land();
        const landed = h.client.getQueryState(KEY)!.dataUpdatedAt;
        expect(landed).toBeGreaterThan(at);
        expect(h.data()).toEqual(["a", "b"]);
        expect(h.client.getQueryState(KEY)!.dataUpdatedAt).toBe(landed);
        h.off();
    });
});
