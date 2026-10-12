"use client";

import { useEffect, useState } from "react";

/** The search debounce of the server-searched lists (T25). */
export const SEARCH_DEBOUNCE_MS = 300;

/** `value`, updated `delayMs` after it last changed (search boxes that query the server). */
export function useDebouncedValue<T>(value: T, delayMs: number = SEARCH_DEBOUNCE_MS): T {
    const [debounced, setDebounced] = useState(value);
    useEffect(() => {
        const id = setTimeout(() => setDebounced(value), delayMs);
        return () => clearTimeout(id);
    }, [value, delayMs]);
    return debounced;
}
