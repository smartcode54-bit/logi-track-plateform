// T54R (issue #74; R44, R77): the waitlist form and the join-network form post only to the anonymous BFF forms
// (/api/forms/*), with no cookie, and import no Firebase module (the web has none since TW7;
// features/dashboard/firebaseImports.test.ts walks the import graph). UI3C: the landing's waitlist dialog is gone (the
// landing links to /waitlist), and both forms speak Thai as well as English.
import React from "react";
import { readFileSync } from "fs";
import path from "path";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";

import { render, screen, waitFor } from "@/components/__tests__/test-utils";
import { ApiError } from "@/lib/apiError";
import { postPublicForm, PUBLIC_FORM_PATHS, submitPartnerInterest, submitWaitlist } from "@/lib/publicForms";
import WaitlistForm from "@/features/auth/components/WaitlistForm";
import JoinNetworkPage from "@/app/join-network/page";

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
afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
});

interface Sent {
    url: string;
    init: RequestInit;
}

function stubFetch(answer: () => Response = () => new Response(JSON.stringify({ data: { received: true } }), { status: 201 })) {
    const sent: Sent[] = [];
    vi.stubGlobal(
        "fetch",
        vi.fn(async (input: RequestInfo | URL, init: RequestInit = {}) => {
            sent.push({ url: String(input), init });
            return answer();
        })
    );
    return sent;
}

const root = path.resolve(__dirname, "../../..");

describe("lib/publicForms", () => {
    it("posts JSON same-origin to /api/forms/* with no credentials", async () => {
        const sent = stubFetch();
        await submitWaitlist({ email: "a@example.test", name: "A", countryCode: "+66", phone: "0812345678", website: "" });
        await submitPartnerInterest({ companyName: "Co", contractNumber: "", contactName: "A", email: "a@example.test", serviceArea: "", fleetSize: "", vehicleType: "", website: "" });
        expect(sent.map((s) => s.url)).toEqual(["/api/forms/waitlist", "/api/forms/partner-interest"]);
        for (const s of sent) {
            expect(s.init.method).toBe("POST");
            expect(s.init.credentials).toBe("omit");
            expect(new Headers(s.init.headers).get("Content-Type")).toBe("application/json");
        }
        expect(JSON.parse(String(sent[0].init.body))).toEqual({ email: "a@example.test", name: "A", countryCode: "+66", phone: "0812345678", website: "" });
    });

    it("throws Go's refusal as an ApiError, and a lost connection as network_error", async () => {
        stubFetch(() => new Response(JSON.stringify({ error: { code: "resource_exhausted", message: "x", details: {}, requestId: "r" } }), { status: 429 }));
        await expect(postPublicForm(PUBLIC_FORM_PATHS.waitlist, {})).rejects.toMatchObject({ status: 429, code: "resource_exhausted" });
        vi.stubGlobal("fetch", vi.fn(async () => Promise.reject(new TypeError("offline"))));
        const error = await postPublicForm(PUBLIC_FORM_PATHS.waitlist, {}).catch((e: unknown) => e);
        expect(error).toBeInstanceOf(ApiError);
        expect((error as ApiError).code).toBe("network_error");
    });
});

describe("the landing forms (AC: they post only to /api/forms/*)", () => {
    it("the waitlist page posts to /api/forms/waitlist with the honeypot left empty", async () => {
        const sent = stubFetch();
        const user = userEvent.setup();
        render(<WaitlistForm />);
        await user.type(await screen.findByLabelText(/^Full name/), "  Somchai  ");
        await user.type(screen.getByLabelText(/^Email/), "somchai@example.test");
        await user.type(screen.getByLabelText(/^Phone number/), "812345678");
        await user.click(screen.getByRole("button", { name: "Request access" }));
        await waitFor(() => expect(sent).toHaveLength(1));
        expect(sent[0].url).toBe("/api/forms/waitlist");
        expect(JSON.parse(String(sent[0].init.body))).toEqual({ email: "somchai@example.test", name: "Somchai", countryCode: "+66", phone: "812345678", website: "" });
        expect(await screen.findByText("You are on the waitlist")).toBeInTheDocument();
    });

    it("the waitlist page posts to /api/forms/waitlist and shows Go's refusal", async () => {
        const sent = stubFetch(() => new Response(JSON.stringify({ error: { code: "resource_exhausted", message: "x", details: {}, requestId: "r" } }), { status: 429 }));
        const error = vi.spyOn(toast, "error");
        const user = userEvent.setup();
        render(<WaitlistForm />);
        await user.type(await screen.findByLabelText(/^Full name/), "Somchai");
        await user.type(screen.getByLabelText(/^Email/), "somchai@example.test");
        await user.type(screen.getByLabelText(/^Phone number/), "812345678");
        await user.click(screen.getByRole("button", { name: "Request access" }));
        await waitFor(() => expect(error).toHaveBeenCalledWith("Too many requests. Please wait and try again."));
        expect(sent.map((s) => s.url)).toEqual(["/api/forms/waitlist"]);
    });

    it("the join-network form posts to /api/forms/partner-interest", async () => {
        const sent = stubFetch();
        const user = userEvent.setup();
        render(<JoinNetworkPage />);
        await user.type(await screen.findByLabelText(/^Company name/), "Somchai Transport");
        await user.type(screen.getByLabelText(/^Contact name/), "Somchai");
        await user.type(screen.getByLabelText(/^Email/), "somchai@example.test");
        await user.type(screen.getByLabelText("Phone number"), "0812345678");
        await user.click(screen.getByRole("button", { name: "Send application" }));
        expect(await screen.findByTestId("join-success")).toBeInTheDocument();
        expect(sent).toHaveLength(1);
        expect(sent[0].url).toBe("/api/forms/partner-interest");
        expect(JSON.parse(String(sent[0].init.body))).toMatchObject({
            companyName: "Somchai Transport",
            contactName: "Somchai",
            email: "somchai@example.test",
            contractNumber: "0812345678",
            website: "",
        });
    });

    it("no former Firestore writer imports Firebase any more", () => {
        for (const file of ["features/auth/components/WaitlistForm.tsx", "features/auth/hooks/useWaitlist.ts", "app/join-network/page.tsx"]) {
            const src = readFileSync(path.join(root, file), "utf8");
            expect(src, file).not.toMatch(/from ["'](firebase\/|@\/firebase\/)/);
            expect(src, file).not.toMatch(/addDoc|collection\(/);
        }
    });
});
