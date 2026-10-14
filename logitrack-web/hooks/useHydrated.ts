import { useSyncExternalStore } from "react";

const subscribe = () => () => {};

/**
 * False during the server render and the hydration pass, true after it. For UI that depends on browser-only
 * state such as the stored theme (next-themes), so the server HTML and the first client render agree.
 */
export function useHydrated(): boolean {
    return useSyncExternalStore(
        subscribe,
        () => true,
        () => false
    );
}
