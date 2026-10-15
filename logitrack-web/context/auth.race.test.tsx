import { afterEach, beforeEach, expect, it, vi } from "vitest";
import React from "react";
import { act, cleanup, screen, waitFor } from "@testing-library/react";
import { completeSignIn, useAuth } from "./auth";
import { meQueryOptions } from "@/features/auth/api/me";
import { signInWithPassword } from "@/lib/authClient";
import { createQueryClient } from "@/lib/queryClient";
import { queryKeys } from "@/lib/queryKeys";
import { resetSessionEndForTests } from "@/lib/sessionEnd";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";

vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));

function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => { resolve = done; });
    return { promise, resolve };
}

const loginReply = () => json(200, { data: { tenants: [], defaultTenantId: null, expiresIn: 900 } });

beforeEach(() => {
    window.localStorage.clear();
    window.history.replaceState(null, "", "/login");
    resetSessionEndForTests();
});
afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
});

it("stops loading when the post-login principal read fails with a bootstrap still in flight", async () => {
    const bootstrap = deferred<Response>();
    const web = fakeWeb();
    let reads = 0;
    web.on("GET", "/api/go/v1/me", () => ++reads === 1 ? bootstrap.promise : goErr(503, "unavailable"));
    web.on("POST", "/api/auth/login", loginReply);
    let auth: ReturnType<typeof useAuth>;
    function Probe() {
        const current = useAuth();
        React.useEffect(() => { auth = current; }, [current]);
        return <p>{current?.loading ? "loading" : "ready"}</p>;
    }
    const { client } = renderWithProviders(<Probe />);
    await waitFor(() => expect(reads).toBe(1));
    await act(async () => {
        await expect(auth!.login("admin@own.test", "pw")).rejects.toMatchObject({ code: "unavailable" });
    });
    await waitFor(() => expect(screen.getByText("ready")).toBeInTheDocument());
    expect(client.getQueryState(queryKeys.me())).toMatchObject({ status: "error", fetchStatus: "idle" });
    await act(async () => { bootstrap.resolve(goErr(401, "session_revoked")); });
});

it("cancels a me refetch started while the post-login read is in flight before publishing the principal", async () => {
    const postLogin = deferred<Response>();
    const background = deferred<Response>();
    const web = fakeWeb();
    let reads = 0;
    web.on("GET", "/api/go/v1/me", () => ++reads === 1 ? postLogin.promise : background.promise);
    web.on("POST", "/api/auth/login", loginReply);
    const client = createQueryClient();
    try {
        await signInWithPassword("admin@own.test", "pw");
        const signingIn = completeSignIn(client);
        await waitFor(() => expect(reads).toBe(1));
        const refetching = client.fetchQuery(meQueryOptions).catch(() => undefined);
        await waitFor(() => expect(reads).toBe(2));
        postLogin.resolve(json(200, { data: makeMe() }));
        await signingIn;
        background.resolve(goErr(503, "unavailable"));
        await refetching;
        expect(client.getQueryState(queryKeys.me())).toMatchObject({ status: "success", error: null, data: { id: "u-1" } });
    } finally {
        client.clear();
    }
});

it("does not publish a principal superseded while cache cancellation is settling", async () => {
    const cancelling = deferred<void>();
    const release = deferred<void>();
    const web = fakeWeb();
    let reads = 0;
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ id: ++reads === 1 ? "old-user" : "new-user" }) }));
    web.on("POST", "/api/auth/login", loginReply);
    const client = createQueryClient();
    const cancelQueries = client.cancelQueries.bind(client);
    let cancellations = 0;
    // Hold the awaited cache boundary, while still performing the real cancellation.
    const cancel = vi.spyOn(client, "cancelQueries").mockImplementation(async (...args) => {
        await cancelQueries(...args);
        if (++cancellations === 2) {
            cancelling.resolve();
            await release.promise;
        }
    });
    try {
        await signInWithPassword("old@own.test", "pw");
        const oldSignIn = completeSignIn(client).catch((error: unknown) => error);
        await cancelling.promise;
        await signInWithPassword("new@own.test", "pw");
        await completeSignIn(client);
        release.resolve();
        expect(await oldSignIn).toMatchObject({ name: "AbortError" });
        expect(client.getQueryState(queryKeys.me())).toMatchObject({ status: "success", error: null, data: { id: "new-user" } });
    } finally {
        release.resolve();
        cancel.mockRestore();
        client.clear();
    }
});
