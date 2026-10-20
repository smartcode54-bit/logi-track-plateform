/* eslint-disable @next/next/no-html-link-for-pages -- tests exercise document click interception before router handlers. */
import React from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useUnsavedChanges } from "./useUnsavedChanges";
import { confirmNavigation } from "@/lib/navigationGuard";

function Editor({ dirty = true, discard = vi.fn() }) {
    useUnsavedChanges(dirty, "Discard permissions?", discard);
    return <><a href="/app/trucks">Fleet</a><a href="#matrix">Matrix</a><a href="/app/trucks" target="_blank">New tab</a></>;
}
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });
describe("unsaved permissions navigation", () => {
    it("cancels link and programmatic navigation, preserving the draft; allows same-page and new-tab links", () => {
        const confirm = vi.spyOn(window, "confirm").mockReturnValue(false);
        const discard = vi.fn();
        render(<Editor discard={discard} />);
        expect(fireEvent.click(screen.getByText("Fleet"))).toBe(false);
        expect(confirmNavigation()).toBe(false);
        fireEvent.click(screen.getByText("Matrix"));
        fireEvent.click(screen.getByText("New tab"));
        expect(confirm).toHaveBeenCalledTimes(2);
        expect(discard).not.toHaveBeenCalled();
    });
    it("asks once on confirmed navigation and removes the guard after save, discard or unmount", () => {
        const confirm = vi.spyOn(window, "confirm").mockReturnValue(true);
        const discard = vi.fn();
        const view = render(<Editor discard={discard} />);
        expect(confirmNavigation()).toBe(true);
        expect(confirmNavigation()).toBe(true);
        expect(confirm).toHaveBeenCalledTimes(1);
        expect(discard).toHaveBeenCalledTimes(1);
        view.rerender(<Editor dirty={false} discard={discard} />);
        expect(confirmNavigation()).toBe(true);
        view.unmount();
        expect(confirmNavigation()).toBe(true);
        expect(confirm).toHaveBeenCalledTimes(1);
    });
    it("uses the browser unload prompt only while changes remain", () => {
        const view = render(<Editor />);
        const unload = new Event("beforeunload", { cancelable: true });
        window.dispatchEvent(unload);
        expect(unload.defaultPrevented).toBe(true);
        view.rerender(<Editor dirty={false} />);
        const saved = new Event("beforeunload", { cancelable: true });
        window.dispatchEvent(saved);
        expect(saved.defaultPrevented).toBe(false);
    });
    it("cancels native back/forward traversal before the router sees it", () => {
        const navigation = new EventTarget();
        vi.stubGlobal("navigation", navigation);
        vi.spyOn(window, "confirm").mockReturnValue(false);
        render(<Editor />);
        const event = new Event("navigate", { cancelable: true });
        Object.assign(event, { navigationType: "traverse", destination: { url: new URL("/app/trucks", window.location.href).href } });
        act(() => { navigation.dispatchEvent(event); });
        expect(event.defaultPrevented).toBe(true);
    });
    it("restores the editor before popstate reaches the router on browsers without Navigation API", () => {
        vi.stubGlobal("navigation", undefined);
        window.history.replaceState({ page: "matrix" }, "", "/app/security-center/roles");
        vi.spyOn(window, "confirm").mockReturnValue(false);
        render(<Editor />);
        const nextRouter = vi.fn();
        window.addEventListener("popstate", nextRouter);
        window.history.replaceState({ page: "fleet" }, "", "/app/trucks");
        fireEvent.popState(window, { state: { page: "fleet" } });
        expect(window.location.pathname).toBe("/app/security-center/roles");
        expect(window.history.state).toEqual({ page: "matrix" });
        expect(nextRouter).not.toHaveBeenCalled();
        window.removeEventListener("popstate", nextRouter);
    });
});
