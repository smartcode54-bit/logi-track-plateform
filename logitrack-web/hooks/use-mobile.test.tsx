import React from "react";
import { renderToString } from "react-dom/server";
import { act, cleanup, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useIsLg, useIsMobile } from "./use-mobile";

type MediaQueryListStub = MediaQueryList & {
    listeners: Set<(event: MediaQueryListEvent) => void>;
};

let width = 375;
const mediaQueryLists: MediaQueryListStub[] = [];

function matchesQuery(query: string) {
    const maxWidth = query.match(/max-width:\s*(\d+)px/);
    if (maxWidth) return width <= Number(maxWidth[1]);

    const minWidth = query.match(/min-width:\s*(\d+)px/);
    if (minWidth) return width >= Number(minWidth[1]);

    return false;
}

function setViewport(nextWidth: number) {
    const previousMatches = mediaQueryLists.map((mediaQueryList) => mediaQueryList.matches);
    width = nextWidth;

    mediaQueryLists.forEach((mediaQueryList, index) => {
        if (previousMatches[index] === mediaQueryList.matches) return;

        const event = { matches: mediaQueryList.matches, media: mediaQueryList.media } as MediaQueryListEvent;
        mediaQueryList.listeners.forEach((listener) => listener(event));
    });
}

function MobileValue() {
    return <span>{String(useIsMobile())}</span>;
}

beforeEach(() => {
    width = 375;
    vi.spyOn(window, "matchMedia").mockImplementation((query) => {
        const listeners = new Set<(event: MediaQueryListEvent) => void>();
        const mediaQueryList = {
            media: query,
            onchange: null,
            get matches() {
                return matchesQuery(query);
            },
            addEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => listeners.add(listener),
            removeEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener),
            addListener: () => {},
            removeListener: () => {},
            dispatchEvent: () => true,
            listeners,
        } as unknown as MediaQueryListStub;
        mediaQueryLists.push(mediaQueryList);
        return mediaQueryList;
    });
});

afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
});

describe("viewport hooks", () => {
    it("reads a phone viewport as true on the first render", () => {
        const { result } = renderHook(() => useIsMobile());

        expect(result.current).toBe(true);
    });

    it("reads desktop viewport matches on the first render", () => {
        setViewport(1440);
        const { result } = renderHook(() => [useIsMobile(), useIsLg()] as const);

        expect(result.current).toEqual([false, true]);
    });

    it("updates both values when media query change events fire", () => {
        const { result } = renderHook(() => [useIsMobile(), useIsLg()] as const);

        expect(result.current).toEqual([true, false]);

        act(() => setViewport(1024));
        expect(result.current).toEqual([false, true]);

        act(() => setViewport(767));
        expect(result.current).toEqual([true, false]);
    });

    it("unsubscribes from the media query on unmount", () => {
        const { unmount } = renderHook(() => useIsMobile());
        const mediaQueryList = mediaQueryLists.find(({ media }) => media === "(max-width: 767px)");

        expect(mediaQueryList?.listeners.size).toBe(1);
        unmount();
        expect(mediaQueryList?.listeners.size).toBe(0);
    });

    it("uses false as the server snapshot", () => {
        width = 375;

        expect(renderToString(<MobileValue />)).toBe("<span>false</span>");
    });
});
