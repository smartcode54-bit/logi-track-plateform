import "@testing-library/jest-dom/vitest";

if (typeof window !== "undefined") {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    value: (query: string) => ({
      matches: false,
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
