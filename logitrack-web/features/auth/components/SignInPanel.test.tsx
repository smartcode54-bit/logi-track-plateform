// T18 (R79; Appendix C §C.4.8): the sign-in panel with the must-change-password step. The BFF and Go
// are a fake fetch; the Firebase bridge is a fake SDK.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));

import { SignInPanel } from "./SignInPanel";
import { tokenFromHash } from "./ResetPasswordForm";
import { configureFirebaseBridge } from "@/lib/firebaseBridge";
import { fakeFirebase, fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";

beforeEach(() => {
    window.sessionStorage.clear();
    window.history.replaceState(null, "", "/login");
});
afterEach(() => {
    vi.unstubAllGlobals();
    configureFirebaseBridge(undefined);
});

function signedOutWeb() {
    const web = fakeWeb();
    let signedIn = false;
    web.on("GET", "/api/go/v1/me", () => (signedIn ? json(200, { data: makeMe({ legacyAuthUid: undefined }) }) : goErr(401, "unauthenticated")));
    web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
    web.on("POST", "/api/auth/firebase-token", () => goErr(403, "permission_denied"));
    web.on("PATCH", "/api/go/v1/me", () => json(200, { data: makeMe() }));
    return {
        web,
        signIn: () => {
            signedIn = true;
        },
    };
}

describe("SignInPanel", () => {
    it("signs in with email and password through /api/auth/login", async () => {
        const { web, signIn } = signedOutWeb();
        web.on("POST", "/api/auth/login", () => {
            signIn();
            return json(200, { data: { tenants: [], defaultTenantId: null, expiresIn: 900 } });
        });
        fakeFirebase();
        const onSignedIn = vi.fn();
        renderWithProviders(<SignInPanel onSignedIn={onSignedIn} />);
        const user = userEvent.setup();
        await user.type(await screen.findByLabelText("Email"), "admin@own.test");
        await user.type(screen.getByLabelText("Password"), "pw");
        await user.click(screen.getByRole("button", { name: "Login" }));
        await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1));
        expect(web.calls.find((c) => c.url === "/api/auth/login")?.body).toEqual({ email: "admin@own.test", password: "pw" });
    });

    it("shows the catalogue text of a wrong password", async () => {
        const { web } = signedOutWeb();
        web.on("POST", "/api/auth/login", () => goErr(401, "invalid_credentials"));
        fakeFirebase();
        renderWithProviders(<SignInPanel />);
        const user = userEvent.setup();
        await user.type(await screen.findByLabelText("Email"), "a@b.test");
        await user.type(screen.getByLabelText("Password"), "nope");
        await user.click(screen.getByRole("button", { name: "Login" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Incorrect email or password.");
    });

    it("must change password: the ticket form, a policy error keeps the ticket, then a normal sign-in with the new password", async () => {
        const { web, signIn } = signedOutWeb();
        web.on("POST", "/api/auth/login", (c) => {
            const body = c.body as { password: string };
            if (body.password === "Temp-Pass-1") return goErr(403, "password_change_required", { passwordChangeTicket: "tkt-9", expiresIn: 600 });
            if (body.password === "A-Much-Better-Secret") {
                signIn();
                return json(200, { data: { tenants: [], defaultTenantId: null, expiresIn: 900 } });
            }
            return goErr(401, "invalid_credentials");
        });
        web.on("POST", "/api/go/v1/auth/password/change", (c) => {
            const body = c.body as { passwordChangeTicket: string; newPassword: string };
            if (body.newPassword.length < 10) return goErr(422, "invalid_argument", { fields: [{ field: "newPassword", reason: "too_short", params: { min: 10, max: 128 } }] });
            return body.passwordChangeTicket === "tkt-9" ? new Response(null, { status: 204 }) : goErr(422, "invalid_argument", { fields: [{ field: "passwordChangeTicket", reason: "invalid_or_expired" }] });
        });
        fakeFirebase();
        const onSignedIn = vi.fn();
        renderWithProviders(<SignInPanel onSignedIn={onSignedIn} />);
        const user = userEvent.setup();
        await user.type(await screen.findByLabelText("Email"), "driver@own.test");
        await user.type(screen.getByLabelText("Password"), "Temp-Pass-1");
        await user.click(screen.getByRole("button", { name: "Login" }));

        expect(await screen.findByText("Set a new password")).toBeInTheDocument();
        expect(onSignedIn).not.toHaveBeenCalled();

        await user.type(screen.getByLabelText("New password"), "short");
        await user.type(screen.getByLabelText("Confirm new password"), "short");
        await user.click(screen.getByRole("button", { name: "Set password and sign in" }));
        expect(await screen.findByRole("alert")).toHaveTextContent("Use at least 10 characters.");

        await user.clear(screen.getByLabelText("New password"));
        await user.clear(screen.getByLabelText("Confirm new password"));
        await user.type(screen.getByLabelText("New password"), "A-Much-Better-Secret");
        await user.type(screen.getByLabelText("Confirm new password"), "A-Much-Better-Secret");
        await user.click(screen.getByRole("button", { name: "Set password and sign in" }));

        await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1));
        const changes = web.calls.filter((c) => c.url === "/api/go/v1/auth/password/change");
        expect(changes.at(-1)?.body).toEqual({ passwordChangeTicket: "tkt-9", newPassword: "A-Much-Better-Secret" });
        const logins = web.calls.filter((c) => c.url === "/api/auth/login").map((c) => (c.body as { password: string }).password);
        expect(logins).toEqual(["Temp-Pass-1", "A-Much-Better-Secret"]);
        // The change form never refreshed or logged out anything.
        expect(web.calls.filter((c) => c.url === "/api/auth/logout")).toHaveLength(0);
    });

    it("an expired ticket returns to the sign-in form with a message", async () => {
        const { web } = signedOutWeb();
        web.on("POST", "/api/auth/login", () => goErr(403, "password_change_required", { passwordChangeTicket: "old" }));
        web.on("POST", "/api/go/v1/auth/password/change", () => goErr(422, "invalid_argument", { fields: [{ field: "passwordChangeTicket", reason: "invalid_or_expired" }] }));
        fakeFirebase();
        renderWithProviders(<SignInPanel />);
        const user = userEvent.setup();
        await user.type(await screen.findByLabelText("Email"), "d@own.test");
        await user.type(screen.getByLabelText("Password"), "x");
        await user.click(screen.getByRole("button", { name: "Login" }));
        await user.type(await screen.findByLabelText("New password"), "A-Much-Better-Secret");
        await user.type(screen.getByLabelText("Confirm new password"), "A-Much-Better-Secret");
        await user.click(screen.getByRole("button", { name: "Set password and sign in" }));
        expect(await screen.findByRole("status")).toHaveTextContent("This step has expired. Sign in again to continue.");
        expect(screen.getByLabelText("Password")).toBeInTheDocument();
    });
});

describe("tokenFromHash", () => {
    it("reads the token of the reset and invite links", () => {
        expect(tokenFromHash("#token=abc_DEF-123")).toBe("abc_DEF-123");
        expect(tokenFromHash("token=x")).toBe("x");
        expect(tokenFromHash("#other=1")).toBe("");
        expect(tokenFromHash("")).toBe("");
    });
});
