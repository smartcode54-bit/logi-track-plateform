// T18 (Appendix E §E.5 row 29; Appendix B §B.2.5 web contract): the revoke action of the Security
// Center overview. Its list is still the legacy users/{uid} documents, whose fields their owner can
// rewrite, so the Go user is the one bound to the document id (legacyAuthUid), shown before anything
// is revoked; a row naming someone else's email resolves to nobody.
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

const rows: { id: string; data: Record<string, unknown> }[] = [];
vi.mock("@/firebase/client", () => ({ db: {}, functions: {} }));
vi.mock("firebase/functions", () => ({ httpsCallable: vi.fn() }));
vi.mock("firebase/firestore", () => ({
    GeoPoint: class {},
    collection: () => ({}),
    limit: () => ({}),
    orderBy: () => ({}),
    query: () => ({}),
    onSnapshot: (_q: unknown, next: (snap: { forEach: (cb: (d: { id: string; data: () => Record<string, unknown> }) => void) => void }) => void) => {
        next({ forEach: (cb) => rows.forEach((r) => cb({ id: r.id, data: () => r.data })) });
        return () => undefined;
    },
}));
vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));

import { SessionManagementActiveUsers } from "./SessionManagementActiveUsers";
import { configureFirebaseBridge } from "@/lib/firebaseBridge";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";

beforeAll(() => {
    Element.prototype.hasPointerCapture ??= () => false;
    Element.prototype.releasePointerCapture ??= () => undefined;
    Element.prototype.scrollIntoView ??= () => undefined;
    globalThis.ResizeObserver ??= class {
        observe() {}
        unobserve() {}
        disconnect() {}
    } as unknown as typeof ResizeObserver;
});
beforeEach(() => {
    rows.length = 0;
    window.history.replaceState(null, "", "/app/security-center");
    configureFirebaseBridge(async () => {
        throw new Error("no Firebase SDK here");
    });
});
afterEach(() => {
    vi.unstubAllGlobals();
    configureFirebaseBridge(undefined);
});

function legacyRow(uid: string, email: string, displayName: string) {
    rows.push({ id: uid, data: { email, displayName, lastLogin: new Date().toISOString() } });
}

function setup(goUsers: unknown[]) {
    const web = fakeWeb();
    web.on("GET", "/api/go/v1/me", () => json(200, { data: makeMe({ id: "u-admin", legacyAuthUid: undefined }) }));
    web.on("POST", "/api/auth/firebase-token", () => goErr(403, "permission_denied"));
    web.on("GET", "/api/go/v1/users", () => json(200, { data: goUsers }));
    web.on("DELETE", /^\/api\/go\/v1\/users\/[^/]+\/sessions$/, () => new Response(null, { status: 204 }));
    return web;
}

async function openRevoke() {
    const u = userEvent.setup();
    const [, row] = await screen.findAllByRole("row");
    await u.click(within(row).getByRole("button"));
    await u.click(await screen.findByRole("menuitem", { name: "Force logout" }));
    return { u, dialog: await screen.findByRole("dialog") };
}

describe("SessionManagementActiveUsers revoke", () => {
    it("shows the Go account bound to the row and revokes exactly that one", async () => {
        legacyRow("fb-ann", "ann@own.test", "Ann");
        const web = setup([
            { id: "go-other", email: "ann@own.test", legacyAuthUid: "fb-other", displayName: "Other Ann" },
            { id: "go-ann", email: "ann@own.test", legacyAuthUid: "fb-ann", displayName: "Ann" },
        ]);
        renderWithProviders(<SessionManagementActiveUsers />);
        const { u, dialog } = await openRevoke();
        expect(await within(dialog).findByText("Ann (ann@own.test)")).toBeInTheDocument();
        await u.click(within(dialog).getByRole("button", { name: "Sign out everywhere" }));
        await waitFor(() => expect(web.calls.some((c) => c.method === "DELETE")).toBe(true));
        expect(web.calls.filter((c) => c.method === "DELETE").map((c) => c.url)).toEqual(["/api/go/v1/users/go-ann/sessions"]);
    });

    it("a row that names someone else's email resolves to nobody: nothing can be revoked", async () => {
        // User X rewrote its own users/{fb-x}.email to the boss's address.
        legacyRow("fb-x", "boss@company.test", "X");
        const web = setup([{ id: "go-boss", email: "boss@company.test", legacyAuthUid: "fb-boss", displayName: "Boss" }]);
        renderWithProviders(<SessionManagementActiveUsers />);
        const { dialog } = await openRevoke();
        expect(await within(dialog).findByText("This account is not in the user directory yet.")).toBeInTheDocument();
        expect(within(dialog).getByRole("button", { name: "Sign out everywhere" })).toBeDisabled();
        expect(web.calls.some((c) => c.method === "DELETE")).toBe(false);
    });
});
