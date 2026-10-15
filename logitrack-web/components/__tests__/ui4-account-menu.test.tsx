import React from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClientProvider } from "@tanstack/react-query";

import { LanguageProvider } from "@/context/language";
import { createQueryClient } from "@/lib/queryClient";
import { fakeWeb, json, makeMe } from "@/test-utils/fakeWeb";
import { UserMenu } from "../user-menu";

beforeEach(() => window.localStorage.setItem("language", "en"));
afterEach(() => vi.unstubAllGlobals());

const partner = { id: "carrier", nameTh: "รถร่วม", nameEn: "Carrier", kind: "carrier", role: "tenant_admin" };
const PROFILES = {
    staff: makeMe(),
    customer: makeMe({ tenant: null, tenants: [], steward: false, customerScopes: [{ billingPartyId: "bp-cjsf", name: "CJSF" }] }),
    partner: makeMe({ tenant: partner, tenants: [partner], steward: false }),
};

describe("account link in the console user menu", () => {
    it.each(Object.entries(PROFILES))("is after identity and before language for %s", async (_profile, me) => {
        const web = fakeWeb();
        web.on("GET", "/api/go/v1/me", () => json(200, { data: me }));
        render(<QueryClientProvider client={createQueryClient()}><LanguageProvider><UserMenu /></LanguageProvider></QueryClientProvider>);
        const user = userEvent.setup();
        const trigger = await screen.findByTestId("user-menu-trigger");
        // A closed dropdown adds no stop before its trigger.
        expect(screen.queryByRole("menuitem", { name: "My account" })).toBeNull();
        await user.tab();
        expect(trigger).toHaveFocus();
        await user.keyboard("{Enter}");
        const item = await screen.findByRole("menuitem", { name: "My account" });
        expect(item.tagName).toBe("A");
        expect(item).toHaveAttribute("href", "/my-account");
        expect(item.className).toContain("max-md:min-h-11");
        const menu = screen.getByRole("menu");
        const identity = within(menu).getByText(me.email!);
        const language = within(menu).getByRole("group", { name: "Language" });
        expect(identity.compareDocumentPosition(item) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        expect(item.compareDocumentPosition(language) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        expect(within(menu).getAllByRole("menuitem")[0]).toBe(item);
        expect(item).toHaveFocus();
    });
});
