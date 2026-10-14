import "@testing-library/jest-dom/vitest";

if (typeof window !== "undefined") {
  // jsdom ships neither; Radix (Popover, Dialog) and cmdk (the searchable select's list, UI2) use them.
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver;
  Element.prototype.scrollIntoView ??= () => {};
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: (query: string) => ({
      get matches() {
        const maxWidth = query.match(/max-width:\s*(\d+)px/);
        if (maxWidth) return window.innerWidth <= Number(maxWidth[1]);

        const minWidth = query.match(/min-width:\s*(\d+)px/);
        if (minWidth) return window.innerWidth >= Number(minWidth[1]);

        return false;
      },
      media: query,
      onchange: null,
      addListener: () => {},
      removeListener: () => {},
      addEventListener: () => {},
      removeEventListener: () => {},
      dispatchEvent: () => true,
    }),
  });
}

// Node 25+ defines its own localStorage/sessionStorage globals (undefined without
// --localstorage-file), which shadow jsdom's under the vitest jsdom environment; restore jsdom's.
// CI runs Node 22, where this is a no-op.
const dom = (globalThis as { jsdom?: { window: Window } }).jsdom;
if (typeof window !== "undefined" && dom) {
    for (const name of ["localStorage", "sessionStorage"] as const) {
        if (window[name] === undefined) {
            Object.defineProperty(globalThis, name, { value: dom.window[name], configurable: true, writable: true });
        }
    }
}
