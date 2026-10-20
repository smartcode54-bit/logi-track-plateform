import React from "react";
import { flushSync } from "react-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

import { LanguageProvider } from "@/context/language";
import { fakeWeb, goErr } from "@/test-utils/fakeWeb";
import * as invalidHandler from "@/lib/formInvalidHandler";
import { AccountPasswordPanel } from "./AccountPasswordPanel";

const PATH = "/api/go/v1/auth/password/change";

beforeEach(() => {
    window.localStorage.setItem("language", "en");
    window.history.replaceState(null, "", "/my-account");
});
afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
});

async function setup(reply: () => Response | Promise<Response> = () => new Response(null, { status: 204 }), language: "en" | "th" = "en") {
    window.localStorage.setItem("language", language);
    const web = fakeWeb();
    web.on("POST", PATH, reply);
    render(<LanguageProvider><AccountPasswordPanel /></LanguageProvider>);
    const current = await screen.findByLabelText(language === "en" ? /^Current password/ : /^รหัสผ่านปัจจุบัน/);
    const next = screen.getByLabelText(language === "en" ? /^New password/ : /^รหัสผ่านใหม่/);
    const confirm = screen.getByLabelText(language === "en" ? /^Confirm new password/ : /^ยืนยันรหัสผ่านใหม่/);
    const user = userEvent.setup();
    return { web, user, current, next, confirm, submit: () => user.click(screen.getByRole("button", { name: language === "en" ? "Change password" : "เปลี่ยนรหัสผ่าน" })) };
}

async function fill(view: Awaited<ReturnType<typeof setup>>, password = "A-Better-Secret") {
    await view.user.type(view.current, "Current-Secret");
    await view.user.type(view.next, password);
    await view.user.type(view.confirm, password);
}

describe("AccountPasswordPanel", () => {
    it("focuses client errors after inputs re-enable when focus timers run before the final commit", async () => {
        const originalInvalidHandler = invalidHandler.createInvalidHandler;
        vi.spyOn(invalidHandler, "createInvalidHandler").mockImplementation((form, t) => {
            const onInvalid = originalInvalidHandler(form, t);
            return (errors) => {
                flushSync(() => undefined);
                expect(screen.getByLabelText(/^Current password/)).toBeDisabled();
                onInvalid(errors);
            };
        });
        const originalSetTimeout = globalThis.setTimeout;
        vi.spyOn(globalThis, "setTimeout").mockImplementation((...args) => {
            const current = document.querySelector<HTMLInputElement>('input[name="currentPassword"]');
            if (typeof args[0] === "function" && !args[1] && current?.disabled) {
                args[0]();
                return originalSetTimeout(() => undefined, 0);
            }
            return originalSetTimeout(...args);
        });
        const view = await setup();
        await view.submit();
        await waitFor(() => expect(view.current).toBeEnabled());
        await waitFor(() => expect(view.current).toHaveFocus());
        await view.user.click(view.confirm);
        await view.user.type(view.confirm, "a");
        expect(view.confirm).toHaveFocus();
        expect(view.web.calls).toHaveLength(0);
    });

    it("shows required labels, associated errors and shared feedback, sending nothing for empty fields", async () => {
        const summary = vi.spyOn(toast, "error");
        const view = await setup();
        await view.submit();
        for (const input of [view.current, view.next, view.confirm]) {
            expect(input).toHaveAttribute("aria-invalid", "true");
            expect(input).toHaveAttribute("aria-required", "true");
            expect(input).toHaveAccessibleDescription("Enter a password.");
            expect(document.querySelector(`label[for="${input.id}"] [aria-hidden=true]`)).toHaveTextContent("*");
        }
        expect(view.current).toHaveAttribute("autocomplete", "current-password");
        expect(view.next).toHaveAttribute("autocomplete", "new-password");
        expect(view.confirm).toHaveAttribute("autocomplete", "new-password");
        await waitFor(() => expect(view.current).toHaveFocus());
        expect(summary).toHaveBeenCalledWith("Check the highlighted fields (3). First: Enter a password.");
        expect(view.web.calls).toHaveLength(0);
        expect(screen.getByRole("link", { name: "Request a password reset link" })).toHaveAttribute("href", "/forgot-password");
    });

    it("uses the shared invalid feedback for mismatched confirmation and sends nothing", async () => {
        const summary = vi.spyOn(toast, "error");
        const view = await setup();
        await fill(view);
        await view.user.type(view.confirm, "different");
        await view.submit();
        expect(view.confirm).toHaveAccessibleDescription("The two passwords do not match.");
        expect(summary).toHaveBeenCalledWith("Check the highlighted fields (1). First: The two passwords do not match.");
        expect(view.web.calls).toHaveLength(0);
    });

    it("maps a current-password mismatch to that field without signing out or refreshing", async () => {
        const view = await setup(() => goErr(422, "invalid_argument", { fields: [{ field: "currentPassword", reason: "mismatch" }] }));
        await fill(view);
        await view.submit();
        await waitFor(() => expect(view.current).toHaveAccessibleDescription("The current password is incorrect."));
        expect(screen.getByRole("alert")).toHaveTextContent("The current password is incorrect.");
        await waitFor(() => expect(view.current).toHaveFocus());
        expect(view.next).toHaveAttribute("aria-invalid", "false");
        expect(view.web.calls.map((call) => call.url)).toEqual([PATH]);
        expect(view.current).toHaveValue("Current-Secret");
    });

    it.each([
        ["too_short", { min: 10 }, "Use at least 10 characters."],
        ["invalid_utf8", {}, "Use valid text characters in your password."],
    ])("maps the new-password policy reason %s", async (reason, params, text) => {
        const view = await setup(() => goErr(422, "invalid_argument", { fields: [{ field: "newPassword", reason, params }] }));
        await fill(view, "short");
        await view.submit();
        await waitFor(() => expect(view.next).toHaveAccessibleDescription(text));
        expect(screen.getByRole("alert")).toHaveTextContent(text);
        await waitFor(() => expect(view.next).toHaveFocus());
        expect(view.current).toHaveAttribute("aria-invalid", "false");
        expect(view.web.count("POST", PATH)).toBe(1);
    });

    it("focuses the first invalid field in form order once a pending 422 finishes", async () => {
        let finish!: (response: Response) => void;
        const view = await setup(() => new Promise<Response>((resolve) => { finish = resolve; }));
        await fill(view);
        await view.submit();
        for (const input of [view.current, view.next, view.confirm]) expect(input).toBeDisabled();
        finish(goErr(422, "invalid_argument", { fields: [
            { field: "newPassword", reason: "too_short", params: { min: 10 } },
            { field: "currentPassword", reason: "mismatch" },
        ] }));
        await waitFor(() => expect(view.current).toHaveFocus());
        expect(view.current).toBeEnabled();
        expect(screen.getAllByRole("alert").map((alert) => alert.textContent)).toEqual([
            "The current password is incorrect.",
            "Use at least 10 characters.",
        ]);
        expect(view.web.count("POST", PATH)).toBe(1);
    });

    it.each([
        ["en", 30, "Too many requests. Please wait and try again. You can try again in 30 s."],
        ["en", 1, "Too many requests. Please wait and try again. You can try again in 1 s."],
        ["th", 30, "มีคำขอมากเกินไป กรุณารอสักครู่แล้วลองใหม่ ลองได้อีกครั้งใน 30 วินาที"],
        ["th", 1, "มีคำขอมากเกินไป กรุณารอสักครู่แล้วลองใหม่ ลองได้อีกครั้งใน 1 วินาที"],
    ] as const)("shows the shared 429 text and a concise %s retry delay of %s", async (language, seconds, text) => {
        const view = await setup(() => goErr(429, "resource_exhausted", { retryAfterSeconds: seconds }), language);
        await fill(view);
        await view.submit();
        expect(await screen.findByRole("alert")).toHaveTextContent(text);
        expect(view.web.count("POST", PATH)).toBe(1);
    });

    it("shows shared API text for other failures", async () => {
        const view = await setup(() => goErr(503, "unavailable"));
        await fill(view);
        await view.submit();
        expect(await screen.findByRole("alert")).toHaveTextContent(/^The service is temporarily unavailable\. Please try again\.$/);
        expect(view.current).toHaveValue("Current-Secret");
    });

    it("posts once, preserves spaces, disables while pending and clears all fields after 204", async () => {
        let finish!: (response: Response) => void;
        const web = fakeWeb();
        web.on("POST", PATH, () => new Promise<Response>((resolve) => { finish = resolve; }));
        render(<LanguageProvider><AccountPasswordPanel /></LanguageProvider>);
        const user = userEvent.setup();
        const current = await screen.findByLabelText(/^Current password/);
        const next = screen.getByLabelText(/^New password/);
        const confirm = screen.getByLabelText(/^Confirm new password/);
        await user.type(current, " old ");
        await user.type(next, " new secret ");
        await user.type(confirm, " new secret ");
        await user.click(screen.getByRole("button", { name: "Change password" }));
        expect(screen.getByRole("button", { name: "Changing password..." })).toBeDisabled();
        expect(web.calls).toHaveLength(1);
        expect(web.calls[0].body).toEqual({ currentPassword: " old ", newPassword: " new secret " });
        finish(new Response(null, { status: 204 }));
        expect(await screen.findByRole("status")).toHaveTextContent("Other devices were signed out; this session stays active.");
        for (const input of [current, next, confirm]) expect(input).toHaveValue("");
        expect(web.calls.map((call) => call.url)).toEqual([PATH]);
    });
});
