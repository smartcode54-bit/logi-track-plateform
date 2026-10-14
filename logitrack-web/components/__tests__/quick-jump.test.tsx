import React, { createRef } from "react";
import { act, fireEvent, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const navigation = vi.hoisted(() => ({ pathname: "/app/support", push: vi.fn() }));
vi.mock("next/navigation", () => ({
    usePathname: () => navigation.pathname,
    useRouter: () => ({ push: navigation.push }),
}));

import { QuickJump, quickJumpLinks } from "@/components/quick-jump";
import { QuickJumpDialog } from "@/components/quick-jump-dialog";
import { menuFor, menuLinksFor, securityMenuFor } from "@/components/app-nav";
import { useLanguage } from "@/context/language";
import { homeRouteOf } from "@/features/auth/api/homeRoute";
import type { MeDTO } from "@/features/auth/api/me";
import { navigationProfileOf } from "@/features/auth/utils/navigationProfile";
import { createQueryClient } from "@/lib/queryClient";
import { makeMe } from "@/test-utils/fakeWeb";
import { renderWithProviders } from "@/test-utils/renderWithProviders";
import { ROLE_DEFAULT_CAPABILITIES, STEWARD_CAPABILITIES } from "shared-docs/schemas/capabilities";

const customer = makeMe({
    tenant: null, tenants: [], steward: false, customerScopes: [{ billingPartyId: "customer", name: "Customer" }],
    capabilities: ["operations:view_driver_monitor", "operations:manage_tasks", "operations:view_incidents", "operations:create_standby"],
});
const staff = makeMe({ capabilities: [...ROLE_DEFAULT_CAPABILITIES.find((role) => role.role === "tenant_admin")!.capabilities] });
const partner = makeMe({ ...staff, capabilities: staff.capabilities.filter((cap) => !(STEWARD_CAPABILITIES as readonly string[]).includes(cap)),
    platformRoles: [], tenant: { id: "carrier", nameTh: "รถร่วม", nameEn: "Carrier", kind: "carrier", role: "tenant_admin" } });

function LanguageSwitch() {
    const { setLanguage } = useLanguage();
    return <button onClick={() => setLanguage("th")}>Thai</button>;
}

function mount(me: MeDTO = customer) {
    const client = createQueryClient();
    client.setQueryData(["me"], me);
    const content = <><button>Invoker</button><input aria-label="Editor" /><LanguageSwitch /><QuickJump /><main id="main-content" tabIndex={-1}>Page</main></>;
    const view = renderWithProviders(content, client);
    return { ...view, content };
}

const trigger = () => screen.getByTestId("quick-jump-trigger");
const search = () => screen.getByRole("combobox", { name: "Search pages" });
const shortcut = (init: KeyboardEventInit = {}) => fireEvent.keyDown(document.activeElement ?? window, { key: "k", ctrlKey: true, ...init });

beforeEach(() => {
    navigation.pathname = "/app/support";
    navigation.push.mockReset();
    vi.spyOn(navigator, "userAgent", "get").mockReturnValue("Linux");
});
afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    vi.doUnmock("@/components/quick-jump-dialog");
});

describe("palette destinations", () => {
    it.each([customer, partner, staff])("equals the resolved active main menu, including collapsed groups and Support", (me) => {
        const expected = menuLinksFor(menuFor(new Set(me.capabilities), homeRouteOf(me), navigationProfileOf(me)));
        expect(quickJumpLinks(me, "/app/support")).toEqual(expected);
        expect(new Set(expected.map((entry) => entry.href)).size).toBe(expected.length);
        expect(expected.at(-1)?.href).toBe("/app/support");
    });

    it("uses Security Center's allowed destinations, Home and Support in order", () => {
        expect(quickJumpLinks(staff, "/app/security-center/users")).toEqual(menuLinksFor(securityMenuFor(new Set(staff.capabilities), homeRouteOf(staff))));
        expect(quickJumpLinks(null, "/app/support")).toEqual([]);
    });

    it("excludes hidden-but-allowed and forbidden pages", () => {
        const hrefs = quickJumpLinks(customer, "/app/support").map((entry) => entry.href);
        expect(hrefs).not.toContain("/app/standby-records");
        expect(hrefs).not.toContain("/app/trucks");
        expect(quickJumpLinks(partner, "/app/support").map((entry) => entry.href)).not.toContain("/app/payroll");
    });
});

describe("shortcuts and lifecycle", () => {
    it.each([false, true])("opens with the platform modifier (Apple: %s), focuses search and restores the invoker", async (apple) => {
        vi.spyOn(navigator, "userAgent", "get").mockReturnValue(apple ? "Macintosh" : "Linux");
        mount();
        await screen.findByTestId("quick-jump-trigger");
        expect(trigger()).toHaveAttribute("aria-keyshortcuts", apple ? "Meta+K" : "Control+K");
        expect(trigger()).toHaveTextContent(apple ? "⌘K" : "Ctrl+K");
        const invoker = screen.getByRole("button", { name: "Invoker" });
        invoker.focus();
        shortcut({ ctrlKey: apple, metaKey: !apple });
        expect(screen.queryByRole("dialog")).toBeNull();
        shortcut({ ctrlKey: !apple, metaKey: apple });
        await waitFor(() => expect(search()).toHaveFocus());
        const dialog = screen.getByRole("dialog", { name: "Jump to a page" });
        expect(dialog).toHaveAccessibleDescription("Find a page in your menu.");
        expect(within(dialog).getByRole("listbox", { name: "Search pages" })).toBeInTheDocument();
        await userEvent.setup().keyboard("{Escape}");
        await waitFor(() => expect(invoker).toHaveFocus());
    });

    it("ignores ordinary, extra-modifier, repeated, composing, handled and editable keystrokes", async () => {
        mount();
        await screen.findByTestId("quick-jump-trigger");
        for (const init of [{ ctrlKey: false }, { shiftKey: true }, { altKey: true }, { metaKey: true }, { repeat: true }, { isComposing: true }, { keyCode: 229 }]) shortcut(init);
        const handled = new KeyboardEvent("keydown", { key: "k", ctrlKey: true, bubbles: true, cancelable: true });
        handled.preventDefault();
        window.dispatchEvent(handled);
        screen.getByRole("textbox", { name: "Editor" }).focus();
        shortcut();
        const editor = document.createElement("div");
        editor.setAttribute("contenteditable", "true");
        document.body.append(editor);
        fireEvent.keyDown(editor, { key: "k", ctrlKey: true });
        editor.remove();
        expect(screen.queryByRole("dialog")).toBeNull();
        expect(trigger()).toHaveAttribute("aria-expanded", "false");
    });

    it.each(["dialog", "alertdialog", "menu", "listbox"])("does not open over another %s", async (role) => {
        mount();
        await screen.findByTestId("quick-jump-trigger");
        const overlay = document.createElement("div");
        overlay.setAttribute("role", role);
        document.body.append(overlay);
        trigger().focus();
        shortcut();
        expect(trigger()).toHaveAttribute("aria-expanded", "false");
        overlay.remove();
    });

    it("keeps Tab trapped, refocuses search on another shortcut, and falls back to the trigger for a removed invoker", async () => {
        mount();
        await screen.findByTestId("quick-jump-trigger");
        const invoker = document.createElement("button");
        document.body.append(invoker);
        invoker.focus();
        shortcut();
        await waitFor(() => expect(search()).toHaveFocus());
        invoker.remove();
        const user = userEvent.setup();
        for (let index = 0; index < 6; index++) {
            await user.tab();
            expect(screen.getByRole("dialog")).toContainElement(document.activeElement as HTMLElement);
        }
        shortcut();
        expect(search()).toHaveFocus();
        await user.keyboard("{Escape}");
        await waitFor(() => expect(trigger()).toHaveFocus());
    });

    it("fits the visual viewport after its portal mounts and cleans up viewport listeners", async () => {
        const viewport = Object.assign(new EventTarget(), { width: 360, height: 300, offsetTop: 40, offsetLeft: 0 });
        const remove = vi.spyOn(viewport, "removeEventListener");
        vi.stubGlobal("visualViewport", viewport);
        const view = mount();
        await userEvent.setup().click(await screen.findByTestId("quick-jump-trigger"));
        const dialog = await screen.findByRole("dialog");
        await waitFor(() => expect(dialog.style.getPropertyValue("--quick-jump-height")).toBe("300px"));
        expect(dialog.style.getPropertyValue("--quick-jump-top")).toBe("190px");
        viewport.height = 250;
        act(() => viewport.dispatchEvent(new Event("resize")));
        expect(dialog.style.getPropertyValue("--quick-jump-height")).toBe("250px");
        view.unmount();
        expect(remove.mock.calls.map(([name]) => name)).toEqual(["resize", "scroll"]);
    });

    it("registers one listener across rerenders and removes it on unmount", async () => {
        const add = vi.spyOn(window, "addEventListener");
        const remove = vi.spyOn(window, "removeEventListener");
        const view = mount();
        await screen.findByTestId("quick-jump-trigger");
        const listeners = add.mock.calls.filter(([name]) => name === "keydown");
        expect(listeners).toHaveLength(1);
        act(() => view.client.setQueryData(["me"], { ...customer, displayName: "Changed" }));
        await userEvent.setup().click(trigger());
        await screen.findByRole("dialog");
        expect(add.mock.calls.filter(([name]) => name === "keydown")).toHaveLength(1);
        view.unmount();
        expect(remove.mock.calls.some(([name, listener]) => name === "keydown" && listener === listeners[0][1])).toBe(true);
    });

    it.each(["principal", "tenant"])("closes and resets the query on a %s change", async (change) => {
        const view = mount(staff);
        await screen.findByTestId("quick-jump-trigger");
        const user = userEvent.setup();
        await user.click(trigger());
        await user.type(await screen.findByRole("combobox"), "support");
        act(() => view.client.setQueryData(["me"], change === "principal" ? { ...staff, id: "other" } : { ...staff, tenant: { ...staff.tenant!, id: "other" } }));
        await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
        await user.click(trigger());
        expect(await screen.findByRole("combobox")).toHaveValue("");
    });
});

describe("search and selection", () => {
    it("matches normalized current-language labels, group names and routes without changing order", async () => {
        const view = mount(staff);
        const user = userEvent.setup();
        await user.click(await screen.findByTestId("quick-jump-trigger"));
        const input = await screen.findByRole("combobox");
        const entries = quickJumpLinks(staff, navigation.pathname);
        expect(screen.getAllByRole("option").map((node) => node.getAttribute("data-value"))).toEqual(entries.map((entry) => entry.href));
        await user.type(input, "ＢＩＬＬＩＮＧ");
        expect(screen.getAllByRole("option").map((node) => node.getAttribute("data-value"))).toEqual(entries.filter((entry) => entry.groupLabelKey === "nav.billing" || entry.href.includes("billing")).map((entry) => entry.href));
        await user.clear(input);
        await user.type(input, "/APP/TRUCKS");
        expect(screen.getAllByRole("option")).toHaveLength(1);
        await user.clear(input);
        await user.type(input, "absent-page");
        expect(screen.getByText("No matching pages")).toBeVisible();
        expect(screen.getByRole("status")).toHaveTextContent("0 pages found");
        await user.clear(input);
        act(() => view.client.setQueryData(["me"], { ...staff }));
    });

    it("updates open labels and matching when the language changes, folding Thai tone marks", async () => {
        mount(staff);
        const user = userEvent.setup();
        await screen.findByTestId("quick-jump-trigger");
        await user.click(screen.getByRole("button", { name: "Thai" }));
        await waitFor(() => expect(trigger()).toHaveAccessibleName("ไปยังหน้า"));
        await user.click(trigger());
        const input = await screen.findByRole("combobox", { name: "ค้นหาหน้า" });
        await user.type(input, "ตังคา");
        expect(screen.getAllByRole("option")).toHaveLength(2);
        expect(screen.getByRole("dialog")).toHaveAccessibleName("ไปยังหน้า");
    });

    it("handles arrows and Enter, closes, and focuses main only when the route commits", async () => {
        const view = mount();
        const user = userEvent.setup();
        await user.click(await screen.findByTestId("quick-jump-trigger"));
        await screen.findByRole("combobox");
        await user.keyboard("{ArrowDown}{Enter}");
        expect(navigation.push).toHaveBeenCalledTimes(1);
        expect(screen.queryByRole("dialog")).toBeNull();
        expect(screen.getByRole("main")).not.toHaveFocus();
        navigation.pathname = navigation.push.mock.calls[0][0];
        act(() => view.client.setQueryData(["me"], { ...customer, displayName: "Route committed" }));
        await waitFor(() => expect(screen.getByRole("main")).toHaveFocus());
        await new Promise((resolve) => setTimeout(resolve, 20));
        expect(screen.getByRole("main")).toHaveFocus();
    });

    it("selects the current route without pushing and focuses main immediately", async () => {
        mount();
        const user = userEvent.setup();
        await user.click(await screen.findByTestId("quick-jump-trigger"));
        await user.type(await screen.findByRole("combobox"), "support");
        await user.keyboard("{Enter}");
        await waitFor(() => expect(screen.getByRole("main")).toHaveFocus());
        expect(navigation.push).not.toHaveBeenCalled();
    });

    it("rejects a stale selection against the current cache before the component rerenders", async () => {
        const view = mount(staff);
        const user = userEvent.setup();
        await user.click(await screen.findByTestId("quick-jump-trigger"));
        await user.type(await screen.findByRole("combobox"), "/app/trucks");
        const result = screen.getByRole("option");
        act(() => {
            view.client.setQueryData(["me"], { ...staff, capabilities: [] });
            fireEvent.click(result);
        });
        expect(navigation.push).not.toHaveBeenCalled();
        expect(screen.getByRole("dialog")).toBeInTheDocument();
    });

    it("retranslates an already open palette without resetting its query", async () => {
        const client = createQueryClient();
        client.setQueryData(["me"], staff);
        function LanguageDialog() {
            const { setLanguage } = useLanguage();
            return <><button onClick={() => setLanguage("th")}>Thai</button><QuickJumpDialog entries={quickJumpLinks(staff, "/app/support")}
                dialogRef={createRef()} onClose={() => {}} onSelect={() => {}} onRestoreFocus={() => {}} /></>;
        }
        renderWithProviders(<LanguageDialog />, client);
        const input = await screen.findByRole("combobox");
        fireEvent.change(input, { target: { value: "/app/trucks" } });
        fireEvent.click(screen.getByRole("button", { name: "Thai", hidden: true }));
        await waitFor(() => expect(screen.getByRole("dialog")).toHaveAccessibleName("ไปยังหน้า"));
        expect(screen.getByRole("combobox")).toHaveValue("/app/trucks");
        expect(screen.getByRole("option")).toHaveTextContent("รถ");
    });

    it("offers a localized retry after a lazy chunk failure and recovers on retry", async () => {
        vi.doMock("@/components/quick-jump-dialog", () => { throw new Error("chunk unavailable"); });
        mount();
        const user = userEvent.setup();
        await user.click(await screen.findByTestId("quick-jump-trigger"));
        expect(await screen.findByRole("alert")).toHaveTextContent("This dialog could not be opened");
        vi.doUnmock("@/components/quick-jump-dialog");
        await user.click(screen.getByRole("button", { name: "Try again" }));
        await waitFor(() => expect(search()).toHaveFocus());
    });
});
