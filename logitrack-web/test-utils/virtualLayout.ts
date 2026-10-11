/**
 * Layout for the virtualised lists in jsdom (TW8). jsdom lays nothing out, so every `offsetHeight` is
 * 0; TanStack Virtual reads the viewport's and the rows' `offsetHeight`. `stubVirtualLayout` gives the
 * scroll viewports of `components/virtual-table.tsx` and `components/filter-combobox.tsx` a height, and
 * every virtual row (`[data-index]`) its row height, until the returned function restores jsdom.
 *
 * Scrolling: set the viewport's `scrollTop` and fire `scroll` on it (`scrollViewport`).
 */
import { act, fireEvent } from "@testing-library/react";

const VIEWPORT_SLOTS = new Set(["virtual-table-viewport", "filter-combobox-virtual-list"]);

export interface VirtualLayout {
    /** Height of every viewport, px. */
    viewport: number;
    /** Height of every virtual row, px. */
    row: number;
    /** Width of everything, px. */
    width?: number;
}

export function stubVirtualLayout({ viewport, row, width = 1024 }: VirtualLayout): () => void {
    const proto = HTMLElement.prototype;
    const height = Object.getOwnPropertyDescriptor(proto, "offsetHeight");
    const widthDesc = Object.getOwnPropertyDescriptor(proto, "offsetWidth");
    Object.defineProperty(proto, "offsetHeight", {
        configurable: true,
        get(this: HTMLElement) {
            if (VIEWPORT_SLOTS.has(this.dataset?.slot ?? "")) return viewport;
            if (this.hasAttribute?.("data-index")) return row;
            return 0;
        },
    });
    Object.defineProperty(proto, "offsetWidth", { configurable: true, get: () => width });
    return () => {
        const own = proto as unknown as Record<string, unknown>;
        if (height) Object.defineProperty(proto, "offsetHeight", height);
        else delete own.offsetHeight;
        if (widthDesc) Object.defineProperty(proto, "offsetWidth", widthDesc);
        else delete own.offsetWidth;
    };
}

/** Scrolls a virtualised viewport to `top` px (the end when `top` is `"end"`). */
export function scrollViewport(viewport: HTMLElement, top: number | "end", rowHeight: number, rowCount: number) {
    act(() => {
        viewport.scrollTop = top === "end" ? rowHeight * rowCount : top;
        fireEvent.scroll(viewport);
    });
}
