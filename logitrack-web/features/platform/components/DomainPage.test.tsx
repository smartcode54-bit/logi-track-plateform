// T54S: a page whose Go read model has no Firestore equivalent switches as a whole on its web flag
// (developer-spec.md §10.6, §10.13; R35, R41): Go while the flag is `go`, the Firestore page otherwise,
// Firestore when the flags cannot be read, and a flip re-renders the other source without a reload.
import { afterEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { act, render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";

import { createQueryClient } from "@/lib/queryClient";
import { fakeWeb, goErr, json } from "@/test-utils/fakeWeb";
import { WEB_FLAGS_KEY, type WebFlags } from "../api/webFlags";
import { DomainPage } from "./DomainPage";

afterEach(() => vi.unstubAllGlobals());

const GoPage = () => <p>go page</p>;
const FirestorePage = () => <p>firestore page</p>;

function flags(security: "go" | "firebase") {
    return {
        domains: { auth: "go", masterdata: "firebase", operations: "firebase", billing: "firebase", hr: "firebase", comms: "firebase", security, mobile_release: "firebase", dashboard: "firebase" },
    };
}

function renderPage(client = createQueryClient()) {
    render(
        <QueryClientProvider client={client}>
            <DomainPage domain="security" go={GoPage} firestore={FirestorePage} />
        </QueryClientProvider>
    );
    return client;
}

describe("DomainPage", () => {
    it("renders the Go page while the domain's flag is go, and nothing of it before the flags load", async () => {
        const web = fakeWeb();
        let release: () => void = () => undefined;
        web.on("GET", "/api/go/v1/config/web-flags", () => new Promise((r) => (release = () => r(json(200, { data: flags("go") })))));
        renderPage();
        expect(screen.getByRole("status")).toBeInTheDocument();
        expect(screen.queryByText("go page")).toBeNull();
        expect(screen.queryByText("firestore page")).toBeNull();
        await act(async () => release());
        expect(await screen.findByText("go page")).toBeInTheDocument();
    });

    it("renders the Firestore page while the flag is off", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/config/web-flags", () => json(200, { data: flags("firebase") }));
        renderPage();
        expect(await screen.findByText("firestore page")).toBeInTheDocument();
        expect(screen.queryByText("go page")).toBeNull();
    });

    // One render per test (RTL cleans up after each), so a page left over from another render cannot
    // satisfy the assertions.
    it("renders the Firestore page when the flags cannot be read", async () => {
        const down = fakeWeb();
        down.on("GET", "/api/go/v1/config/web-flags", () => goErr(503, "unavailable"));
        renderPage();
        expect(await screen.findByText("firestore page")).toBeInTheDocument();
        expect(screen.queryByText("go page")).toBeNull();
        expect(screen.queryByRole("status")).toBeNull();
    });

    it("stays on the Go page when a later flags refetch fails (loaded flags win over the error)", async () => {
        const web = fakeWeb();
        let up = true;
        web.on("GET", "/api/go/v1/config/web-flags", () => (up ? json(200, { data: flags("go") }) : goErr(503, "unavailable")));
        const client = renderPage();
        expect(await screen.findByText("go page")).toBeInTheDocument();
        up = false;
        await act(async () => {
            await client.refetchQueries({ queryKey: WEB_FLAGS_KEY });
        });
        expect(client.getQueryState(WEB_FLAGS_KEY)?.status).toBe("error");
        expect(screen.getByText("go page")).toBeInTheDocument();
        expect(screen.queryByText("firestore page")).toBeNull();
    });

    it("switches the page when the flag flips (an env change on the api)", async () => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/config/web-flags", () => json(200, { data: flags("firebase") }));
        const client = renderPage();
        expect(await screen.findByText("firestore page")).toBeInTheDocument();
        act(() => client.setQueryData<WebFlags>(WEB_FLAGS_KEY, flags("go") as WebFlags));
        expect(await screen.findByText("go page")).toBeInTheDocument();
        expect(screen.queryByText("firestore page")).toBeNull();
    });
});
