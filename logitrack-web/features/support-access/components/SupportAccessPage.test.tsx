import React from "react";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("next/navigation", () => ({ usePathname: () => "/app/settings/support-access", useRouter: () => ({ push: vi.fn(), replace: vi.fn(), refresh: vi.fn() }) }));

import { RouteNamespaces } from "@/context/locales/RouteNamespaces";
import { fakeWeb, goErr, json, makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { SupportAccessPage } from "./SupportAccessPage";
import type { SupportAccessDTO, SupportMemberDTO } from "../api/supportAccess";
import { supportStateKey, supportAccessKey } from "../api/supportAccess";

const path = "/api/go/v1/tenants/t-own/support-access";
const membersPath = "/api/go/v1/tenants/t-own/members";
const ratifyPath = `${membersPath}/u-new/ratify`;
const me = makeMe({ capabilities: ["users:assign_role", "security:view_audit"] });
const grant: SupportMemberDTO = { user: { id: "u-new", displayName: "New administrator", email: "new@example.test" }, role: "tenant_admin", status: "active", grantedByPlatform: true, ratifiedAt: null, grantVersion: "9007199254740993", eligibilityBasis: "pending", eligibleToRatify: true };

function setup(options: { enabled?: boolean; eligible?: boolean; rows?: SupportMemberDTO[]; principal?: typeof me; changedBy?: string } = {}) {
    const web = fakeWeb();
    const state: SupportAccessDTO = { enabled: options.enabled ?? false, version: "9007199254740995", changedAt: options.changedBy ? "2026-10-16T00:00:00Z" : null, changedBy: options.changedBy ?? null, eligibleToToggle: options.eligible ?? true };
    const rows = options.rows ?? [structuredClone(grant)];
    web.on("GET", "/api/go/v1/me", () => json(200, { data: options.principal ?? me }));
    web.on("GET", path, () => json(200, { data: state }));
    web.on("GET", membersPath, () => json(200, { data: rows }));
    const view = renderWithProviders(<RouteNamespaces pathname="/app/settings/support-access"><SupportAccessPage /></RouteNamespaces>);
    return { web, state, rows, ...view };
}

afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe("Support access", () => {
    it.each([false, true])("renders authoritative enabled=%s and the disclosure policy", async (enabled) => {
        setup({ enabled });
        expect(await screen.findByTestId("support-state")).toHaveTextContent(enabled ? "ON" : "OFF");
        expect(screen.getByRole("switch")).toHaveAttribute("aria-checked", String(enabled));
        expect(screen.getByText(/The team is always read-only/)).toBeVisible();
        expect(screen.getByText(/There is no expiry/)).toBeVisible();
        if (!enabled) expect(screen.getByText(/masked as \*\*\*\*/)).toBeVisible();
        expect(screen.getByRole("link", { name: "View security events" })).toHaveAttribute("href", "/app/security-center/audit");
    });

    it("confirms before PUT, sends the string CAS version, and never updates optimistically", async () => {
        const { web, state } = setup();
        let finish!: () => void;
        web.on("PUT", path, () => new Promise((resolve) => { finish = () => { state.enabled = true; state.version = "9007199254740996"; resolve(json(200, { data: state })); }; }));
        const user = userEvent.setup();
        await user.click(await screen.findByRole("switch"));
        const dialog = await screen.findByRole("alertdialog");
        expect(web.count("PUT", path)).toBe(0);
        expect(within(dialog).getByRole("button", { name: "Cancel" })).toHaveFocus();
        await user.click(within(dialog).getByRole("button", { name: "Confirm change" }));
        await waitFor(() => expect(web.count("PUT", path)).toBe(1));
        expect(web.calls.find((call) => call.method === "PUT")?.body).toEqual({ enabled: true, expectedVersion: "9007199254740995" });
        expect(screen.getByTestId("support-state")).toHaveTextContent("OFF");
        expect(within(dialog).getByRole("button", { name: "Saving…" })).toBeDisabled();
        await act(async () => finish());
        await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
        expect(screen.getByTestId("support-state")).toHaveTextContent("ON");
        expect(screen.getByRole("switch")).toHaveFocus();
    });

    it("cancel restores focus and sends no mutation", async () => {
        const { web } = setup();
        const user = userEvent.setup();
        const control = await screen.findByRole("switch");
        await user.click(control);
        await user.click(screen.getByRole("button", { name: "Cancel" }));
        expect(web.count("PUT", path)).toBe(0);
        expect(control).toHaveFocus();
    });

    it("409 reloads the winning state and requires a new confirmation with its version", async () => {
        const { web, state } = setup();
        web.on("PUT", path, () => { state.enabled = true; state.version = "22"; return goErr(409, "support_access_changed"); });
        const user = userEvent.setup();
        await user.click(await screen.findByRole("switch"));
        await user.click(screen.getByRole("button", { name: "Confirm change" }));
        expect(await screen.findByText(/Another administrator changed/)).toBeVisible();
        await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
        expect(screen.getByTestId("support-state")).toHaveTextContent("ON");
        expect(web.count("GET", path)).toBe(2);
        await user.click(screen.getByRole("switch"));
        await user.click(screen.getByRole("button", { name: "Confirm change" }));
        await waitFor(() => expect(web.count("PUT", path)).toBe(2));
        expect(web.calls.filter((call) => call.method === "PUT")[1].body).toEqual({ enabled: false, expectedVersion: "22" });
    });

    it("hides an ineligible switch and explains the credential/ratification requirements", async () => {
        setup({ eligible: false });
        await screen.findByTestId("support-state");
        expect(screen.queryByRole("switch")).toBeNull();
        expect(screen.getByText(/If the platform set your password/)).toBeVisible();
        expect(screen.getByRole("link", { name: "My account" })).toHaveAttribute("href", "/my-account");
    });

    it("403 refreshes live eligibility and removes the switch", async () => {
        const { web, state } = setup();
        web.on("PUT", path, () => { state.eligibleToToggle = false; return goErr(403, "support_access_required"); });
        const user = userEvent.setup();
        await user.click(await screen.findByRole("switch"));
        await user.click(screen.getByRole("button", { name: "Confirm change" }));
        await waitFor(() => expect(screen.queryByRole("switch")).toBeNull());
        expect(screen.getByRole("alert")).toHaveTextContent("If the platform set your password");
        expect(web.count("GET", path)).toBe(2);
        await waitFor(() => expect(screen.getByRole("heading", { name: "Pending ratification" })).toHaveFocus());
    });

    it("ratifies only after confirmation, sends grantVersion, then reloads members", async () => {
        const { web, rows } = setup();
        web.on("POST", ratifyPath, () => { rows[0].ratifiedAt = "2026-10-16T00:00:00Z"; rows[0].eligibleToRatify = false; return json(200, { data: { grantVersion: "2", ratifiedAt: rows[0].ratifiedAt } }); });
        const user = userEvent.setup();
        await user.click(await screen.findByRole("button", { name: "Ratify New administrator" }));
        const dialog = screen.getByRole("alertdialog");
        expect(dialog).toHaveTextContent("New administrator");
        expect(dialog).toHaveTextContent("Tenant admin");
        expect(web.count("POST", ratifyPath)).toBe(0);
        await user.click(within(dialog).getByRole("button", { name: "Ratify" }));
        expect(await screen.findByText("No memberships are awaiting ratification.")).toBeVisible();
        expect(web.calls.find((call) => call.method === "POST")?.body).toEqual({ expectedGrantVersion: "9007199254740993" });
        expect(web.count("GET", membersPath)).toBe(2);
        expect(web.count("PUT", path)).toBe(0);
        await waitFor(() => expect(screen.getByRole("heading", { name: "Pending ratification" })).toHaveFocus());
    });

    it("shows a localized administrator label when the changer is not in loaded members", async () => {
        const id = "0199c000-0000-7000-8000-000000000001";
        setup({ changedBy: id });
        expect(await screen.findByText(/Last changed by a company administrator/)).toBeVisible();
        expect(screen.queryByText(new RegExp(id))).toBeNull();
    });

    it.each([true, false])("never prints a loaded changer's id (has name: %s)", async (hasName) => {
        setup({ changedBy: grant.user.id, rows: [{ ...grant, user: { ...grant.user, displayName: hasName ? "Named admin" : null, email: null } }] });
        expect(await screen.findByText(new RegExp(`Last changed by ${hasName ? "Named admin" : "a company administrator"}`))).toBeVisible();
        expect(screen.queryByText(new RegExp(`Last changed by ${grant.user.id}`))).toBeNull();
    });

    it.each(["toggle", "ratify"])("keeps %s confirmation open and disabled while eligibility reloads", async (kind) => {
        const { client, web, state, rows } = setup();
        const user = userEvent.setup();
        const trigger = await screen.findByRole(kind === "toggle" ? "switch" : "button", kind === "ratify" ? { name: "Ratify New administrator" } : {});
        await user.click(trigger);
        const dialog = screen.getByRole("alertdialog");
        const confirm = within(dialog).getByRole("button", { name: kind === "toggle" ? "Confirm change" : "Ratify" });
        let finish!: () => void;
        web.handlers.unshift((call) => call.method === "GET" && call.url.split("?")[0] === (kind === "toggle" ? path : membersPath)
            ? new Promise((resolve) => { finish = () => resolve(json(200, { data: kind === "toggle" ? state : rows })); }) : undefined);
        let reload!: Promise<void>;
        act(() => { reload = client.invalidateQueries({ queryKey: kind === "toggle" ? supportStateKey("t-own") : [...supportAccessKey("t-own"), "members"] }); });
        await waitFor(() => expect(confirm).toBeDisabled());
        await user.click(confirm);
        expect(dialog).toBeVisible();
        expect(web.count("PUT", path) + web.count("POST", ratifyPath)).toBe(0);
        await act(async () => { finish(); await reload; });
        await waitFor(() => expect(confirm).toBeEnabled());
        expect(dialog).toBeVisible();
    });

    it.each(["toggle", "ratify"])("keeps %s copy through the closing animation", async (kind) => {
        // Give Radix Presence the exit animation that the browser's stylesheet supplies.
        const computedStyle = window.getComputedStyle.bind(window);
        vi.spyOn(window, "getComputedStyle").mockImplementation((element, pseudo) => {
            const style = computedStyle(element, pseudo);
            if (element.getAttribute("data-slot") === "dialog-content") {
                Object.defineProperty(style, "animationName", { configurable: true, get: () => element.getAttribute("data-state") === "closed" ? "exit" : "enter" });
            }
            return style;
        });
        setup();
        const user = userEvent.setup();
        await user.click(await screen.findByRole(kind === "toggle" ? "switch" : "button", kind === "ratify" ? { name: "Ratify New administrator" } : {}));
        const dialog = screen.getByRole("alertdialog");
        const copy = dialog.textContent;
        await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
        expect(dialog).toHaveAttribute("data-state", "closed");
        expect(dialog).toBeInTheDocument();
        expect(dialog.textContent).toBe(copy);
        fireEvent.animationEnd(dialog, { animationName: "exit" });
    });

    it("describes ratification of a non-admin without promising support access management", async () => {
        setup({ rows: [{ ...grant, role: "manager" }] });
        await userEvent.setup().click(await screen.findByRole("button", { name: "Ratify New administrator" }));
        const dialog = screen.getByRole("alertdialog");
        expect(dialog).toHaveTextContent("Manager");
        expect(dialog).toHaveTextContent("Ratification confirms this membership grant");
        expect(dialog).not.toHaveTextContent("may then manage support access");
    });

    it.each([[409, "membership_grant_changed", /This membership grant changed/], [403, "ratification_not_allowed", /You cannot ratify this membership/], [500, "internal", /The change could not be confirmed/]] as const)("ratification %s reloads and explains the error", async (status, code, message) => {
        const { web, rows } = setup();
        web.on("POST", ratifyPath, () => { rows[0].eligibleToRatify = false; return goErr(status, code); });
        const user = userEvent.setup();
        await user.click(await screen.findByRole("button", { name: "Ratify New administrator" }));
        await user.click(within(screen.getByRole("alertdialog")).getByRole("button", { name: "Ratify" }));
        expect(await screen.findByText(message)).toBeVisible();
        await waitFor(() => expect(screen.queryByRole("alertdialog")).toBeNull());
        expect(web.count("GET", membersPath)).toBe(2);
        expect(screen.queryByRole("button", { name: "Ratify New administrator" })).toBeNull();
    });

    it("hides self/ineligible actions even if a malformed self row says eligible", async () => {
        setup({ rows: [{ ...grant, user: { ...grant.user, id: me.id } }, { ...grant, user: { ...grant.user, id: "other" }, eligibleToRatify: false }] });
        await screen.findAllByTestId("pending-member");
        expect(screen.queryByRole("button", { name: /^Ratify/ })).toBeNull();
        expect(screen.getByText("You cannot ratify your own membership.")).toBeVisible();
    });

    it("does not label holder-activated first admin as pending", async () => {
        setup({ rows: [{ ...grant, user: { ...grant.user, id: me.id }, eligibilityBasis: "first_admin_self_chosen" }] });
        expect(await screen.findByText(/Your first onboarding administrator grant qualifies/)).toBeVisible();
        expect(screen.queryByTestId("pending-member")).toBeNull();
    });

    it.each([
        makeMe({ tenant: null, tenants: [], platformRoles: ["platform_admin"] }),
        makeMe({ tenant: { ...me.tenant!, role: "manager" } }),
        makeMe({ tenant: { ...me.tenant!, role: "support_readonly" } }),
        makeMe({ tenants: [] }),
    ])("makes no consent or member request without a real admin membership", async (principal) => {
        const { web } = setup({ principal });
        expect(await screen.findByText(/Open this page as an administrator/)).toBeVisible();
        expect(web.count("GET", path)).toBe(0);
        expect(web.count("GET", membersPath)).toBe(0);
    });
});
