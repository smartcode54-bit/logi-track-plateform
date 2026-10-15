import React from "react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { LanguageSwitcher } from "@/components/language-switcher";
import { LanguageProvider, useLanguage } from "@/context/language";
import { fakeWeb } from "@/test-utils/fakeWeb";
import { useForgotPassword } from "./useForgotPassword";

function ResetRequest() {
    const { language } = useLanguage();
    const { email, setEmail, handleSubmit, sent } = useForgotPassword();
    return (
        <>
            <LanguageSwitcher />
            <p data-testid="language">{language}</p>
            <form onSubmit={handleSubmit}>
                <input aria-label="Email" value={email} onChange={(event) => setEmail(event.target.value)} />
                <button type="submit">Send reset</button>
            </form>
            {sent ? <p role="status">Sent</p> : null}
        </>
    );
}

beforeEach(() => window.localStorage.removeItem("language"));
afterEach(() => vi.unstubAllGlobals());

describe("password reset email language", () => {
    it.each(["th", "en"])("sends locale %s from the active interface language", async (locale) => {
        const web = fakeWeb();
        web.on("POST", "/api/go/v1/auth/password/forgot", () => new Response(null, { status: 202 }));
        render(<LanguageProvider><ResetRequest /></LanguageProvider>);
        const email = await screen.findByRole("textbox", { name: "Email" });
        expect(screen.getByTestId("language")).toHaveTextContent("th");
        const user = userEvent.setup();
        if (locale === "en") {
            await user.click(screen.getByRole("button", { name: "English" }));
            await waitFor(() => expect(screen.getByTestId("language")).toHaveTextContent("en"));
        }
        await user.type(email, "driver@example.test");
        await user.click(screen.getByRole("button", { name: "Send reset" }));
        expect(await screen.findByRole("status")).toHaveTextContent("Sent");
        expect(web.calls).toEqual([
            expect.objectContaining({
                method: "POST",
                url: "/api/go/v1/auth/password/forgot",
                body: { email: "driver@example.test", locale },
            }),
        ]);
    });
});
