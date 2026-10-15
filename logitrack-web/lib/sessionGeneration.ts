let generation = 0;
let storageBlocked = false;
const SESSION_GENERATION_KEY = "lt:sessionGeneration";

/** Shared across tabs, with an in-tab fallback when storage is blocked; never contains token material. */
export function sessionGeneration(): number {
    if (storageBlocked) return generation;
    try {
        const stored = window.localStorage.getItem(SESSION_GENERATION_KEY);
        const value = Number(stored);
        if (stored !== null && Number.isSafeInteger(value) && value >= 0) generation = value;
    } catch {
        storageBlocked = true;
        // The in-tab generation still protects this tab when storage is unavailable.
    }
    return generation;
}

/** Called under the session lock only after the BFF has accepted a new sign-in. */
export function advanceSessionGeneration(): void {
    generation = sessionGeneration() + 1;
    if (storageBlocked) return;
    try {
        window.localStorage.setItem(SESSION_GENERATION_KEY, String(generation));
    } catch {
        storageBlocked = true;
        // Keep the in-tab fallback.
    }
}

export function assertSessionGeneration(expected: number): void {
    if (expected !== sessionGeneration()) throw new DOMException("Session changed", "AbortError");
}
