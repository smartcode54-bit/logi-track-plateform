// T17 (developer-spec.md §10.6, §10.7; Appendix E §E.8.6; R35, R41): runtime web domain flags.
// GET /v1/config/web-flags is answered by a fake api whose "env" the tests change, as an edit of
// WEB_FLAG_OVERRIDES on the api container would.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { renderHook, waitFor } from "@testing-library/react";
import { focusManager, QueryClient, QueryClientProvider, QueryObserver } from "@tanstack/react-query";
import { goQueryFn } from "@/lib/goQuery";
import {
    domainQueryFn,
    parseWebFlags,
    resolveDomainSource,
    WEB_DOMAINS,
    WEB_FLAGS_KEY,
    WEB_FLAGS_REFRESH_MS,
    watchWebFlagFlips,
    webFlagsQueryOptions,
    type DomainSource,
    type WebDomain,
} from "./webFlags";
import { useDomainSource, useWebFlags, useWebFlagsSync } from "./useWebFlags";

let apiFlags: Partial<Record<WebDomain, DomainSource>> = {};
let flagsDown = false;
const urls: string[] = [];

function allFirebase(): Record<WebDomain, DomainSource> {
    return Object.fromEntries(WEB_DOMAINS.map((d) => [d, "firebase"])) as Record<WebDomain, DomainSource>;
}

beforeEach(() => {
    apiFlags = {};
    flagsDown = false;
    urls.length = 0;
    vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL) => {
            const url = String(input);
            urls.push(url);
            if (url === "/api/go/v1/config/web-flags") {
                if (flagsDown) return new Response("<html>bad gateway</html>", { status: 502 });
                return new Response(JSON.stringify({ data: { domains: { ...allFirebase(), ...apiFlags } } }), { status: 200 });
            }
            if (url === "/api/go/v1/hubs") return new Response(JSON.stringify({ data: "from-go" }), { status: 200 });
            return new Response(JSON.stringify({ error: { code: "not_found", message: "", details: {}, requestId: "" } }), { status: 404 });
        })
    );
});

afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    focusManager.setFocused(undefined);
});

describe("parseWebFlags", () => {
    it("keeps the nine domains, and anything but go stays on Firestore", () => {
        const flags = parseWebFlags({ domains: { auth: "go", masterdata: "GO", billing: "firebase", extra: "go" } });
        expect(Object.keys(flags.domains)).toEqual([...WEB_DOMAINS]);
        expect(flags.domains.auth).toBe("go");
        expect(flags.domains.masterdata).toBe("firebase");
        expect(flags.domains.dashboard).toBe("firebase");
        expect(() => parseWebFlags({})).toThrowError(expect.objectContaining({ code: "bad_response" }));
    });

    it("is the ['webFlags'] query over GET /v1/config/web-flags with a 60 s stale time and poll", () => {
        expect(webFlagsQueryOptions.queryKey).toEqual(["webFlags"]);
        expect(WEB_FLAGS_KEY).toEqual(["webFlags"]);
        expect(webFlagsQueryOptions.staleTime).toBe(60_000);
        expect(webFlagsQueryOptions.refetchInterval).toBe(60_000);
        expect(WEB_FLAGS_REFRESH_MS).toBe(60_000);
    });
});

function hubsQuery(firestore = vi.fn(async () => "from-firestore")) {
    return {
        firestore,
        options: {
            queryKey: ["hubs"],
            queryFn: domainQueryFn<string>("masterdata", { firestore, go: goQueryFn<string>("/v1/hubs") }),
            staleTime: Infinity,
        },
    };
}

describe("domainQueryFn", () => {
    it("reads Firestore or Go as the flags say when the fetch starts", async () => {
        const client = new QueryClient();
        const { options, firestore } = hubsQuery();
        await expect(client.fetchQuery(options)).resolves.toBe("from-firestore");
        expect(firestore).toHaveBeenCalledTimes(1);

        apiFlags = { masterdata: "go" };
        await client.invalidateQueries({ queryKey: WEB_FLAGS_KEY });
        await client.fetchQuery(webFlagsQueryOptions);
        await expect(client.fetchQuery({ ...options, staleTime: 0 })).resolves.toBe("from-go");
        expect(urls).toContain("/api/go/v1/hubs");
    });

    it("falls back to Firestore only when no flags were ever loaded, else keeps the last ones", async () => {
        flagsDown = true;
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        await expect(resolveDomainSource(client, "masterdata")).resolves.toBe("firebase");

        flagsDown = false;
        apiFlags = { masterdata: "go" };
        await client.fetchQuery(webFlagsQueryOptions);
        flagsDown = true;
        await client.invalidateQueries({ queryKey: WEB_FLAGS_KEY });
        await expect(resolveDomainSource(client, "masterdata")).resolves.toBe("go");
    });
});

describe("changing WEB_FLAG_OVERRIDES on the api flips a domain within 60 s, without a rebuild", () => {
    it("polls the flags and refetches the flipped domain's queries", async () => {
        vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout", "setInterval", "clearInterval", "Date"] });
        focusManager.setFocused(true);
        const client = new QueryClient();
        const stopWatch = watchWebFlagFlips(client);
        const stopFlags = new QueryObserver(client, webFlagsQueryOptions).subscribe(() => undefined); // useWebFlagsSync
        const { options, firestore } = hubsQuery();
        const hubs = new QueryObserver(client, options);
        const stopHubs = hubs.subscribe(() => undefined);
        await vi.advanceTimersByTimeAsync(10);
        expect(hubs.getCurrentResult().data).toBe("from-firestore");

        apiFlags = { masterdata: "go" }; // the api restarts with WEB_FLAG_OVERRIDES=masterdata=go
        await vi.advanceTimersByTimeAsync(WEB_FLAGS_REFRESH_MS - 1_000);
        expect(hubs.getCurrentResult().data).toBe("from-firestore");
        await vi.advanceTimersByTimeAsync(1_000);
        expect(client.getQueryData(WEB_FLAGS_KEY)).toMatchObject({ domains: { masterdata: "go" } });
        expect(hubs.getCurrentResult().data).toBe("from-go");

        apiFlags = {}; // rollback: remove the override
        await vi.advanceTimersByTimeAsync(WEB_FLAGS_REFRESH_MS);
        expect(hubs.getCurrentResult().data).toBe("from-firestore");
        expect(firestore).toHaveBeenCalledTimes(2);

        stopHubs();
        stopFlags();
        stopWatch();
        client.clear();
    });

    it("refetches a query that fell back to Firestore once the flags load", async () => {
        flagsDown = true;
        const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
        const stopWatch = watchWebFlagFlips(client);
        const { options } = hubsQuery();
        const hubs = new QueryObserver(client, options);
        const stop = hubs.subscribe(() => undefined);
        await vi.waitFor(() => expect(hubs.getCurrentResult().data).toBe("from-firestore"));
        flagsDown = false;
        apiFlags = { masterdata: "go" };
        await client.refetchQueries({ queryKey: WEB_FLAGS_KEY });
        await vi.waitFor(() => expect(hubs.getCurrentResult().data).toBe("from-go"));
        stop();
        stopWatch();
    });
});

describe("hooks", () => {
    it("useWebFlagsSync observes the flags; useWebFlags and useDomainSource read them", async () => {
        apiFlags = { auth: "go" };
        const client = new QueryClient();
        const wrapper = ({ children }: { children: React.ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
        const { result, unmount } = renderHook(
            () => {
                useWebFlagsSync();
                return { flags: useWebFlags().data, auth: useDomainSource("auth"), billing: useDomainSource("billing") };
            },
            { wrapper }
        );
        await waitFor(() => expect(result.current.auth).toBe("go"));
        expect(result.current.billing).toBe("firebase");
        expect(result.current.flags?.domains.auth).toBe("go");
        expect(urls.filter((u) => u.endsWith("/web-flags"))).toHaveLength(1);
        unmount();
        client.clear();
    });
});
