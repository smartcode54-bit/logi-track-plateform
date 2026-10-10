// T18 (Appendix C §C.4.9, R29): `/reset-password#token=` reads the token once and removes it from the
// address bar. Under React StrictMode (`next dev`) the mount effect runs twice; the second run must not
// turn a valid link into "invalid".
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import React, { StrictMode } from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";

import ResetPasswordForm from "./ResetPasswordForm";
import { LanguageProvider } from "@/context/language";
import { fakeWeb } from "@/test-utils/fakeWeb";

function renderForm(strict: boolean) {
    try {
        window.localStorage.removeItem("language");
    } catch {
        // English is the default anyway
    }
    const tree = (
        <LanguageProvider>
            <ResetPasswordForm />
        </LanguageProvider>
    );
    return render(strict ? <StrictMode>{tree}</StrictMode> : tree);
}

const passwordInputs = () => document.querySelectorAll('input[type="password"]');

beforeEach(() => {
    window.history.replaceState(null, "", "/reset-password");
});
afterEach(() => {
    vi.unstubAllGlobals();
});

describe("ResetPasswordForm", () => {
    it.each([false, true])("keeps a valid link usable (StrictMode %s), removes the token from the URL and posts it", async (strict) => {
        window.history.replaceState(null, "", "/reset-password#token=abc123");
        const web = fakeWeb();
        web.on("POST", "/api/go/v1/auth/password/reset", () => new Response(null, { status: 204 }));
        renderForm(strict);
        await waitFor(() => expect(passwordInputs()).toHaveLength(2));
        expect(screen.queryByRole("alert")).toBeNull();
        expect(window.location.hash).toBe("");

        const u = userEvent.setup();
        await u.type(screen.getByLabelText("New password"), "N3w-passw0rd!");
        await u.type(screen.getByLabelText("Confirm new password"), "N3w-passw0rd!");
        await u.click(screen.getByRole("button", { name: "Save password" }));
        expect(await screen.findByRole("status")).toHaveTextContent("Your password has been set.");
        expect(web.calls.find((c) => c.url === "/api/go/v1/auth/password/reset")?.body).toEqual({ token: "abc123", newPassword: "N3w-passw0rd!" });
    });

    it.each([false, true])("shows the invalid-link text without a token (StrictMode %s)", async (strict) => {
        renderForm(strict);
        expect(await screen.findByRole("alert")).toHaveTextContent("This link is not valid or has expired.");
        expect(passwordInputs()).toHaveLength(0);
    });
});
