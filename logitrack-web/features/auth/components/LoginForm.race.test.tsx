import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { act, cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const navigation = vi.hoisted(() => ({ replace: vi.fn() }));
vi.mock("next/navigation", () => ({
    useRouter: () => navigation,
    useSearchParams: () => new URLSearchParams("next=%2Fapp%2Fdashboard"),
}));
vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));

import LoginForm from "./LoginForm";
import { meQueryOptions } from "../api/me";
import { queryKeys } from "@/lib/queryKeys";
import { configureSessionEnd, resetSessionEndForTests } from "@/lib/sessionEnd";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";

function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>((done) => { resolve = done; });
    return { promise, resolve };
}

const loginReply = () => json(200, { data: { tenants: [], defaultTenantId: "t-own", expiresIn: 900 } });
let ended: string[];

beforeEach(() => {
    window.localStorage.clear();
    window.history.replaceState(null, "", "/login?next=%2Fapp%2Fdashboard");
    resetSessionEndForTests();
    ended = [];
    configureSessionEnd({ navigate: (url) => ended.push(url) });
    navigation.replace.mockImplementation((url: string) => window.history.replaceState(null, "", url));
    navigation.replace.mockClear();
});

afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
});

async function submit() {
    const user = userEvent.setup();
    await user.type(await screen.findByLabelText(/^Email/), "admin@own.test");
    await user.type(screen.getByLabelText(/^Password/), "seed-password");
    await user.click(screen.getByTestId("sign-in-submit"));
}

describe("sign-in while the public-page auth bootstrap is in flight", () => {
    it.each(["unauthenticated", "token_expired", "session_revoked", "invalid_token"])(
        "ignores a bootstrap %s after sign-in and navigation to /app",
        async (code) => {
            const bootstrap = deferred<Response>();
            const web = fakeWeb();
            let reads = 0;
            web.on("GET", "/api/go/v1/me", () => ++reads === 1 ? bootstrap.promise : json(200, { data: makeMe() }));
            web.on("POST", "/api/auth/login", loginReply);
            web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
            web.on("POST", "/api/auth/logout", () => new Response(null, { status: 204 }));
            const { client } = renderWithProviders(<LoginForm />);

            await submit();
            await waitFor(() => expect(window.location.pathname).toBe("/app/dashboard"));
            await act(async () => {
                bootstrap.resolve(goErr(401, code));
                await bootstrap.promise;
            });
            await waitFor(() => expect(client.isFetching()).toBe(0));

            expect(client.getQueryData(queryKeys.me())).toMatchObject({ id: "u-1" });
            expect(client.getQueryState(queryKeys.me())).toMatchObject({ status: "success", error: null });
            expect(ended).toEqual([]);
            expect(web.count("POST", "/api/auth/logout")).toBe(0);
            expect(web.count("POST", "/api/auth/refresh")).toBe(0);
        }
    );

    it("does not publish a late successful bootstrap for the previous principal", async () => {
        const bootstrap = deferred<Response>();
        const web = fakeWeb();
        let reads = 0;
        web.on("GET", "/api/go/v1/me", () => ++reads === 1 ? bootstrap.promise : json(200, { data: makeMe() }));
        web.on("POST", "/api/auth/login", loginReply);
        const { client } = renderWithProviders(<LoginForm />);
        await submit();
        await waitFor(() => expect(window.location.pathname).toBe("/app/dashboard"));
        await act(async () => {
            bootstrap.resolve(json(200, { data: makeMe({ id: "previous-user" }) }));
            await bootstrap.promise;
        });
        expect(client.getQueryData(queryKeys.me())).toMatchObject({ id: "u-1" });
    });

    it("finishes an old cookie-clearing refresh before sending sign-in", async () => {
        const refresh = deferred<Response>();
        const web = fakeWeb();
        let signedIn = false;
        web.on("GET", "/api/go/v1/me", () => signedIn ? json(200, { data: makeMe() }) : goErr(401, "unauthenticated"));
        web.on("POST", "/api/auth/refresh", async () => {
            const response = await refresh.promise;
            signedIn = false; // The BFF's refused refresh expires both cookies.
            return response;
        });
        web.on("POST", "/api/auth/login", () => {
            signedIn = true;
            return loginReply();
        });
        const { client } = renderWithProviders(<LoginForm />);
        await waitFor(() => expect(web.count("POST", "/api/auth/refresh")).toBe(1));
        await submit();
        const signInsBeforeRefresh = web.count("POST", "/api/auth/login");
        await act(async () => {
            refresh.resolve(goErr(401, "unauthenticated"));
            await refresh.promise;
        });
        await waitFor(() => expect(window.location.pathname).toBe("/app/dashboard"));
        expect(signInsBeforeRefresh).toBe(0);
        expect(signedIn).toBe(true);
        expect(client.getQueryData(queryKeys.me())).toMatchObject({ id: "u-1" });
        expect(ended).toEqual([]);
    });

    it.each(["session_revoked", "invalid_token", "unauthenticated", "token_expired"])(
        "still ends the session for a new %s after sign-in",
        async (code) => {
            const web = fakeWeb();
            let signedIn = false;
            let expired = false;
            web.on("GET", "/api/go/v1/me", () => expired ? goErr(401, code) : signedIn ? json(200, { data: makeMe() }) : goErr(401, "unauthenticated"));
            web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
            web.on("POST", "/api/auth/logout", () => new Response(null, { status: 204 }));
            web.on("POST", "/api/auth/login", () => { signedIn = true; return loginReply(); });
            const { client } = renderWithProviders(<LoginForm />);
            await waitFor(() => expect(client.isFetching()).toBe(0));
            await submit();
            await waitFor(() => expect(window.location.pathname).toBe("/app/dashboard"));
            expired = true;
            await act(async () => { await client.fetchQuery({ ...meQueryOptions, staleTime: 0 }); });
            await waitFor(() => expect(ended).toHaveLength(1));
            expect(ended[0]).toContain("/login?next=%2Fapp%2Fdashboard");
            expect(client.getQueryData(queryKeys.me())).toBeNull();
            await waitFor(() => expect(web.count("POST", "/api/auth/logout")).toBe(1));
        }
    );

    it.each(["invalid_credentials", "unavailable", "bad_response"])(
        "shows %s and preserves the typed email when sign-in fails",
        async (code) => {
            const web = fakeWeb();
            let signedIn = false;
            web.on("GET", "/api/go/v1/me", () => signedIn ? json(200, { data: { id: "missing-capabilities" } }) : goErr(401, "unauthenticated"));
            web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
            web.on("POST", "/api/auth/login", () => {
                if (code === "bad_response") { signedIn = true; return loginReply(); }
                return goErr(code === "unavailable" ? 503 : 401, code);
            });
            renderWithProviders(<LoginForm />);
            await submit();
            expect(await screen.findByRole("alert")).not.toBeEmptyDOMElement();
            expect(screen.getByLabelText(/^Email/)).toHaveValue("admin@own.test");
            expect(screen.getByTestId("sign-in-submit")).toBeEnabled();
            expect(navigation.replace).not.toHaveBeenCalled();
        }
    );
});
