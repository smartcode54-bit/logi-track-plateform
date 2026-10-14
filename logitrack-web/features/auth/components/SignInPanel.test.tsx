// T18 (R79; Appendix C §C.4.8): the sign-in panel with the must-change-password step. The BFF and Go
// are a fake fetch; since TW7 there is no Firebase bridge to fake.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React from "react";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

vi.mock("@/lib/loginGeo", () => ({ resolveLoginGeoForClient: async () => null }));

import { toast } from "sonner";

import { useLanguage } from "@/context/language";
import { SignInPanel } from "./SignInPanel";
import { tokenFromHash } from "./ResetPasswordForm";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";

beforeEach(() => {
    window.sessionStorage.clear();
    window.history.replaceState(null, "", "/login");
});
afterEach(() => {
    vi.unstubAllGlobals();
});

function signedOutWeb() {
    const web = fakeWeb();
    let signedIn = false;
    web.on("GET", "/api/go/v1/me", () => (signedIn ? json(200, { data: makeMe({}) }) : goErr(401, "unauthenticated")));
    web.on("POST", "/api/auth/refresh", () => goErr(401, "unauthenticated"));
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
        const onSignedIn = vi.fn();
        renderWithProviders(<SignInPanel onSignedIn={onSignedIn} />);
        const user = userEvent.setup();
        await user.type(await screen.findByLabelText(/^Email/), "admin@own.test");
        await user.type(screen.getByLabelText(/^Password/), "pw");
        await user.click(screen.getByRole("button", { name: "Sign in" }));
        await waitFor(() => expect(onSignedIn).toHaveBeenCalledTimes(1));
        expect(web.calls.find((c) => c.url === "/api/auth/login")?.body).toEqual({ email: "admin@own.test", password: "pw" });
    });

    it("shows the catalogue text of a wrong password", async () => {
        const { web } = signedOutWeb();
        web.on("POST", "/api/auth/login", () => goErr(401, "invalid_credentials"));
        renderWithProviders(<SignInPanel />);
        const user = userEvent.setup();
        await user.type(await screen.findByLabelText(/^Email/), "a@b.test");
        await user.type(screen.getByLabelText(/^Password/), "nope");
        await user.click(screen.getByRole("button", { name: "Sign in" }));
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
        const onSignedIn = vi.fn();
        renderWithProviders(<SignInPanel onSignedIn={onSignedIn} />);
        const user = userEvent.setup();
        await user.type(await screen.findByLabelText(/^Email/), "driver@own.test");
        await user.type(screen.getByLabelText(/^Password/), "Temp-Pass-1");
        await user.click(screen.getByRole("button", { name: "Sign in" }));

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
        renderWithProviders(<SignInPanel />);
        const user = userEvent.setup();
        await user.type(await screen.findByLabelText(/^Email/), "d@own.test");
        await user.type(screen.getByLabelText(/^Password/), "x");
        await user.click(screen.getByRole("button", { name: "Sign in" }));
        await user.type(await screen.findByLabelText("New password"), "A-Much-Better-Secret");
        await user.type(screen.getByLabelText("Confirm new password"), "A-Much-Better-Secret");
        await user.click(screen.getByRole("button", { name: "Set password and sign in" }));
        expect(await screen.findByRole("status")).toHaveTextContent("This step has expired. Sign in again to continue.");
        expect(screen.getByLabelText(/^Password/)).toBeInTheDocument();
    });
});

/** A button that switches the interface to Thai, as the navigation's language switch does. */
function ToThai() {
    const { setLanguage } = useLanguage();
    return (
        <button type="button" onClick={() => setLanguage("th")}>
            to-thai
        </button>
    );
}

describe("SignInPanel field errors (UI3E, DESIGN.md form style)", () => {
    it("marks both fields required with the Signal Red star, which is not part of their names", async () => {
        signedOutWeb();
        renderWithProviders(<SignInPanel />);
        const email = await screen.findByLabelText(/^Email/);
        const password = screen.getByLabelText(/^Password/);
        for (const [input, name] of [
            [email, "Email"],
            [password, "Password"],
        ] as const) {
            expect(input).toHaveAttribute("aria-required", "true");
            expect(input).toHaveAccessibleName(name);
            const star = document.querySelector(`label[for="${input.id}"] [aria-hidden="true"]`);
            expect(star).toHaveTextContent("*");
            expect(star?.className).toContain("text-destructive");
            // A 44 px target on a phone.
            expect(input.className).toContain("max-md:h-11");
        }
    });

    it("ties each error to its field, focuses the first through the shared onInvalid handler and posts nothing", async () => {
        const { web } = signedOutWeb();
        const summary = vi.spyOn(toast, "error");
        renderWithProviders(<SignInPanel />);
        const user = userEvent.setup();
        const email = await screen.findByLabelText(/^Email/);
        const password = screen.getByLabelText(/^Password/);
        await user.click(screen.getByRole("button", { name: "Sign in" }));

        expect(email).toHaveAttribute("aria-invalid", "true");
        expect(email).toHaveAccessibleDescription("Enter your email address.");
        expect(password).toHaveAttribute("aria-invalid", "true");
        expect(password).toHaveAccessibleDescription("Enter your password.");
        await waitFor(() => expect(email).toHaveFocus());
        expect(summary).toHaveBeenCalledWith("Cannot sign in yet: 2 field(s) need attention. First problem: Enter your email address.");
        expect(web.count("POST", "/api/auth/login")).toBe(0);

        // A malformed address says what is expected; a fixed field loses its error as the user types.
        await user.type(email, "not-an-address");
        expect(email).toHaveAccessibleDescription("Enter an email address such as name@company.co.th.");
        await user.clear(email);
        await user.type(email, "somchai@own.test");
        expect(email).toHaveAttribute("aria-invalid", "false");
        expect(email).not.toHaveAccessibleDescription();
        expect(password).toHaveAttribute("aria-invalid", "true");
        summary.mockRestore();
    });

    it("says a shown error again in Thai when the language changes", async () => {
        signedOutWeb();
        renderWithProviders(
            <>
                <ToThai />
                <SignInPanel />
            </>
        );
        const user = userEvent.setup();
        await user.click(await screen.findByRole("button", { name: "Sign in" }));
        expect(screen.getByLabelText(/^Email/)).toHaveAccessibleDescription("Enter your email address.");
        await user.click(screen.getByRole("button", { name: "to-thai" }));
        await waitFor(() => expect(screen.getByLabelText(/^อีเมล/)).toHaveAccessibleDescription("กรอกอีเมลของคุณ"));
        expect(screen.getByLabelText(/^รหัสผ่าน/)).toHaveAccessibleDescription("กรอกรหัสผ่านของคุณ");
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
